#!/usr/bin/env bash
set -euo pipefail

# Runs gremlins mutation testing on Go packages changed in the current PR.
# Writes a markdown summary table to $GITHUB_STEP_SUMMARY (or stdout when local).
#
# A package counts as done when gremlins wrote a schema-valid report for it, not
# when gremlins exited 0: a cancelled run exits 0 and writes nothing, while a run
# that leaves mutants alive exits non-zero after writing a perfectly good report.
# The script exits non-zero when any changed package produced no usable result.

# ── 1. Classify one package's result ────────────────────────────────────────
# classify_result <report-path>
# Prints "status|reason|score|killed|lived|timed_out" for a single package.
#
# Only the report decides the verdict; gremlins' exit status is a diagnostic the
# caller logs. A report that is missing, unreadable, or not shaped like a
# gremlins report is a classification outcome, never an abort.
classify_result() {
  local report_path="$1" result

  if [[ ! -f "$report_path" ]]; then
    if ! command -v gremlins > /dev/null 2>&1; then
      echo "failed|gremlins not found|n/a|0|0|0"
    else
      echo "failed|no output|n/a|0|0|0"
    fi
    return 0
  fi

  if ! command -v python3 > /dev/null 2>&1; then
    echo "failed|python3 not found|n/a|0|0|0"
    return 0
  fi

  result=$(python3 - "$report_path" <<'PYEOF'
import json
import sys

# The status vocabulary gremlins reports, as read by generate-mutation-report.sh.
INVALID = "failed|invalid output|n/a|0|0|0"

# The reason shares a markdown cell with the rest of the row, so only the first
# few unrecognized statuses are named and the remainder is counted.
NAMED_STATUSES = 3


def bail():
    print(INVALID)
    sys.exit(0)


def displayable(text):
    """Names a status so it cannot break the markdown table it lands in."""
    cleaned = "".join(c if c.isprintable() and c != "|" else " " for c in text)
    return cleaned.strip()[:40] or "(empty)"


try:
    with open(sys.argv[1]) as report:
        data = json.load(report)
except Exception:
    bail()

if not isinstance(data, dict):
    bail()

files = data.get("files")
if not isinstance(files, list):
    bail()

killed = lived = timed_out = not_covered = 0
unrecognized = {}

for file_entry in files:
    if not isinstance(file_entry, dict):
        bail()
    mutations = file_entry.get("mutations")
    if mutations is None:
        mutations = []
    if not isinstance(mutations, list):
        bail()
    for mutation in mutations:
        if not isinstance(mutation, dict):
            bail()
        status = mutation.get("status")
        if not isinstance(status, str):
            bail()
        if status == "KILLED":
            killed += 1
        elif status == "LIVED":
            lived += 1
        elif status == "TIMED OUT":
            timed_out += 1
        elif status == "NOT COVERED":
            not_covered += 1
        else:
            unrecognized[status] = unrecognized.get(status, 0) + 1

total = killed + lived
score = f"{killed / total * 100:.1f}%" if total > 0 else "n/a"

reasons = []
if unrecognized:
    ordered = sorted(unrecognized.items())
    named = ", ".join(
        f"{displayable(status)} ({count})"
        for status, count in ordered[:NAMED_STATUSES]
    )
    if len(ordered) > NAMED_STATUSES:
        named += f" (+{len(ordered) - NAMED_STATUSES} more)"
    reasons.append(f"unrecognized status: {named}")

# An n/a score always says which mutants produced it: a package whose mutants
# were all left unscored is not the same thing as one with no mutants at all.
if total == 0:
    unscored = []
    if not_covered:
        unscored.append(f"{not_covered} not covered")
    if timed_out:
        unscored.append(f"{timed_out} timed out")
    if unscored:
        reasons.append(f"no scored mutants ({', '.join(unscored)})")
    elif not unrecognized:
        reasons.append("no mutants")
reason = "; ".join(reasons) or "-"

print(f"ok|{reason}|{score}|{killed}|{lived}|{timed_out}")
PYEOF
  ) || result="failed|invalid output|n/a|0|0|0"

  echo "$result"
}

# ── 2. Helper: find nearest go.mod by walking up from a directory ───────────
find_module_root() {
  local dir="$REPO_ROOT/$1"
  while [[ "$dir" != "$REPO_ROOT" && "$dir" != "/" ]]; do
    if [[ -f "$dir/go.mod" ]]; then
      echo "$dir"
      return
    fi
    dir=$(dirname "$dir")
  done
  # Fall back to repo root
  echo "$REPO_ROOT"
}

# ── 3. Summary plumbing ─────────────────────────────────────────────────────
# On a runner, rows are appended as they are produced so a run that dies
# mid-way still shows what it managed to do. Locally they are buffered and the
# table is printed once at the end, because the per-package output shares
# stdout and would interleave into an invalid table.
#
# A summary that cannot be written is itself a reporting failure, so it is
# recorded and turned into a non-zero exit at the end rather than aborting the
# loop: the remaining packages still have to run. A row that could not be
# written is buffered so the table still reaches the step log.
declare -a summary_rows=()
summary_failed=0

summary_line() {
  local line="$1"
  if [[ -z "${GITHUB_STEP_SUMMARY:-}" ]]; then
    summary_rows+=("$line")
    return 0
  fi
  if ! echo "$line" >> "$GITHUB_STEP_SUMMARY" 2>/dev/null; then
    summary_failed=1
    summary_rows+=("$line")
  fi
  return 0
}

# ── 4. Main ─────────────────────────────────────────────────────────────────
main() {
  REPO_ROOT=$(git rev-parse --show-toplevel)
  BASE_REF="${GITHUB_BASE_REF:-main}"

  # Find changed Go source files (exclude tests)
  changed_files=$(git diff --name-only "origin/${BASE_REF}...HEAD" -- '*.go' \
    | grep -v '_test\.go$' || true)

  if [[ -z "$changed_files" ]]; then
    echo "No Go source files changed — nothing to mutate."
    exit 0
  fi

  # Map to unique package directories
  changed_packages=$(echo "$changed_files" | xargs -I{} dirname {} | sort -u)

  # Filter to packages that have //go:build unit test files
  testable_packages=()
  while IFS= read -r pkg; do
    pkg_abs="$REPO_ROOT/$pkg"
    if [[ -d "$pkg_abs" ]] && grep -qlr '//go:build unit' --include='*_test.go' "$pkg_abs" 2>/dev/null; then
      testable_packages+=("$pkg")
    fi
  done <<< "$changed_packages"

  if [[ ${#testable_packages[@]} -eq 0 ]]; then
    echo "Changed packages have no unit-tagged tests — nothing to mutate."
    exit 0
  fi

  echo "Packages to test: ${testable_packages[*]}"

  summary_line "## Mutation Testing (changed packages)"
  summary_line ""
  summary_line "| Package | Result | Reason | Score | Killed | Lived | Timed Out |"
  summary_line "|---------|--------|--------|-------|--------|-------|-----------|"

  # Run gremlins per package, classify results
  run_dir=$(mktemp -d)
  trap 'rm -rf "$run_dir"' EXIT

  failed_packages=0
  pkg_index=0

  for pkg in "${testable_packages[@]}"; do
    pkg_index=$((pkg_index + 1))
    module_root=$(find_module_root "$pkg")
    # Compute relative package path from the module root
    rel_pkg="${REPO_ROOT}/${pkg#/}"
    rel_pkg="${rel_pkg#"$module_root"/}"

    echo ""
    echo "=== $pkg (module: ${module_root#"$REPO_ROOT"/}) ==="

    # The index keeps the file names unique: two package paths can differ only
    # in a separator that flattening to underscores collapses.
    safe_name="${pkg_index}-${pkg//\//_}"
    report_file="$run_dir/${safe_name}.json"
    log_file="$run_dir/${safe_name}.log"
    # gremlins has to create the report itself: a file that is already there
    # would read as a completed run.
    rm -f "$report_file"

    # No pipeline, so the exit status is gremlins' own and set -e cannot end the
    # run here. gremlins may exit non-zero when mutants survive — that's expected
    # and not a failure; the status is a diagnostic, never the verdict.
    status=0
    (cd "$module_root" && gremlins unleash \
      --tags unit \
      --timeout-coefficient 10 \
      --workers 4 \
      -o "$report_file" \
      "./$rel_pkg") > "$log_file" 2>&1 || status=$?

    result=$(classify_result "$report_file")
    IFS='|' read -r result_status reason score killed lived timed_out <<< "$result"

    if [[ "$result_status" == "ok" ]]; then
      echo "$pkg: ok (score $score)"
      echo "::group::$pkg gremlins output"
    else
      failed_packages=$((failed_packages + 1))
      echo "$pkg: $result_status ($reason)"
      echo "::group::$pkg gremlins output ($reason)"
    fi
    echo "gremlins exit status: $status"
    tail -n 50 "$log_file" 2>/dev/null || true
    echo "::endgroup::"

    summary_line "| \`$pkg\` | $result_status | $reason | $score | $killed | $lived | $timed_out |"
  done

  # Emit the summary and the verdict. A table that could not be written to the
  # job summary is printed here instead of being lost.
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" && "$summary_failed" -eq 0 ]]; then
    echo "Summary written to job summary."
  else
    echo ""
    printf '%s\n' "${summary_rows[@]}"
  fi

  exit_status=0

  if [[ "$failed_packages" -gt 0 ]]; then
    echo ""
    echo "$failed_packages of ${#testable_packages[@]} package(s) produced no usable result."
    exit_status=1
  fi

  if [[ "$summary_failed" -ne 0 ]]; then
    echo ""
    echo "The job summary could not be written."
    exit_status=1
  fi

  exit "$exit_status"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
