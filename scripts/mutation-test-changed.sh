#!/usr/bin/env bash
set -euo pipefail

# Runs gremlins mutation testing on Go packages changed in the current PR.
# Writes a markdown summary table to $GITHUB_STEP_SUMMARY (or stdout when local).
#
# A package counts as done when gremlins wrote a report for it, not when
# gremlins exited 0: a cancelled run exits 0 and writes nothing. The script
# exits non-zero when any changed package produced no usable result.

REPO_ROOT=$(git rev-parse --show-toplevel)
BASE_REF="${GITHUB_BASE_REF:-main}"

# ── 1. Find changed Go source files (exclude tests) ────────────────────────
changed_files=$(git diff --name-only "origin/${BASE_REF}...HEAD" -- '*.go' \
  | grep -v '_test\.go$' || true)

if [[ -z "$changed_files" ]]; then
  echo "No Go source files changed — nothing to mutate."
  exit 0
fi

# ── 2. Map to unique package directories ────────────────────────────────────
changed_packages=$(echo "$changed_files" | xargs -I{} dirname {} | sort -u)

# ── 3. Filter to packages that have //go:build unit test files ──────────────
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

# ── 4. Helper: find nearest go.mod by walking up from a directory ───────────
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

# ── 5. Summary plumbing ─────────────────────────────────────────────────────
# On a runner, rows are appended as they are produced so a run that dies
# mid-way still shows what it managed to do. Locally they are buffered and the
# table is printed once at the end, because the per-package output shares
# stdout and would interleave into an invalid table.
declare -a summary_rows=()

summary_line() {
  local line="$1"
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    echo "$line" >> "$GITHUB_STEP_SUMMARY"
  else
    summary_rows+=("$line")
  fi
}

summary_line "## Mutation Testing (changed packages)"
summary_line ""
summary_line "| Package | Result | Reason | Score | Killed | Lived | Timed Out |"
summary_line "|---------|--------|--------|-------|--------|-------|-----------|"

# ── 6. Run gremlins per package, classify results ───────────────────────────
run_dir=$(mktemp -d)
trap 'rm -rf "$run_dir"' EXIT

failed_packages=0

for pkg in "${testable_packages[@]}"; do
  module_root=$(find_module_root "$pkg")
  # Compute relative package path from the module root
  rel_pkg="${REPO_ROOT}/${pkg#/}"
  rel_pkg="${rel_pkg#"$module_root"/}"

  echo ""
  echo "=== $pkg (module: ${module_root#"$REPO_ROOT"/}) ==="

  safe_name="${pkg//\//_}"
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

  if [[ ! -f "$report_file" ]]; then
    result="failed|no output|n/a|0|0|0"
  else
    # A report that cannot be read is a classification, not an abort.
    result=$(python3 - "$report_file" <<'PYEOF'
import json
import sys

killed = lived = timed_out = 0
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
    for file_entry in data.get("files", []):
        for m in file_entry.get("mutations", []):
            status = m.get("status", "")
            if status == "KILLED":
                killed += 1
            elif status == "LIVED":
                lived += 1
            elif status == "TIMED OUT":
                timed_out += 1
except Exception:
    print("failed|invalid output|n/a|0|0|0")
    sys.exit(0)

total = killed + lived
score = f"{killed / total * 100:.1f}%" if total > 0 else "n/a"
print(f"ok|-|{score}|{killed}|{lived}|{timed_out}")
PYEOF
    ) || result="failed|invalid output|n/a|0|0|0"
  fi

  IFS='|' read -r result_status reason score killed lived timed_out <<< "$result"

  if [[ "$result_status" == "ok" ]]; then
    echo "$pkg: ok (score $score)"
    echo "::group::$pkg gremlins output"
    cat "$log_file"
    echo "::endgroup::"
  else
    failed_packages=$((failed_packages + 1))
    echo "$pkg: $result_status ($reason)"
    echo "::group::$pkg gremlins output ($reason)"
    echo "gremlins exit status: $status"
    tail -n 50 "$log_file" 2>/dev/null || true
    echo "::endgroup::"
  fi

  summary_line "| \`$pkg\` | $result_status | $reason | $score | $killed | $lived | $timed_out |"
done

# ── 7. Emit the summary and the verdict ─────────────────────────────────────
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "Summary written to job summary."
else
  echo ""
  printf '%s\n' "${summary_rows[@]}"
fi

if [[ "$failed_packages" -gt 0 ]]; then
  echo ""
  echo "$failed_packages of ${#testable_packages[@]} package(s) produced no usable result."
  exit 1
fi
