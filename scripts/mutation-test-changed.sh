#!/usr/bin/env bash
set -euo pipefail

# Runs gremlins mutation testing on Go packages changed in the current PR.
# Writes a markdown summary table to $GITHUB_STEP_SUMMARY (or stdout when local).
#
# The contract: green means gremlins completed on every changed package —
# surviving mutants are allowed and the scores are advisory. Red means at least
# one changed package produced no usable result.
#
# A package counts as done when gremlins wrote a schema-valid report for it, not
# when gremlins exited 0: a cancelled run exits 0 and writes nothing, while a run
# that leaves mutants alive exits non-zero after writing a perfectly good report.
# The script exits non-zero when any changed package produced no usable result.
#
# These rules are coupled to the observed failure semantics of the pinned
# gremlins version — a cancelled run exits 0 and writes no report, and the report
# is a single write after the run — so a version bump means re-checking them.

# ── 1. Classify one package's result ────────────────────────────────────────
# classify_result <report-path> <exit-status>
# Prints "status|reason|score|killed|lived|timed_out" for a single package.
#
# Only the report decides the verdict. With no report, gremlins' exit status
# names the reason, because it is all there is to tell a cancelled run — which
# exits 0 and writes nothing — from one that ended before it could write.
# Nothing is read from gremlins' output: it carries the coverage run's own
# output when coverage fails, so a unit test that panics puts panic text there
# without gremlins having died. A report that is missing, unreadable, or not
# shaped like a gremlins report is a classification outcome, never an abort.
classify_result() {
  local report_path="$1" status="$2" result

  if [[ ! -f "$report_path" ]]; then
    # A gremlins that is not installed leaves the shell exiting 127, so the tool
    # is checked before the status of the run that never happened.
    if ! command -v gremlins > /dev/null 2>&1; then
      echo "failed|gremlins not found|n/a|0|0|0"
    elif [[ "$status" == 0 ]]; then
      echo "failed|no output|n/a|0|0|0"
    else
      echo "failed|gremlins exited $status without a report|n/a|0|0|0"
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

# ── 2. Helpers ──────────────────────────────────────────────────────────────
# find_module_root <pkg>: finds the nearest go.mod by walking up from a
# directory.
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

# has_own_unit_tests <dir>: true when a test file directly in <dir> carries the
# unit build tag. The directory's own test files are listed and handed to grep
# by name rather than letting grep walk the tree, because tests under a
# sub-directory belong to another package and cannot make this one mutable. A
# directory with no test files of its own — or no directory at all — is answered
# before grep is reached, since a grep given a pattern and no file to read would
# take the run's stdin. The glob runs in a subshell, the way the gremlins run
# scopes its own cd, so nullglob stays out of the rest of the script.
has_own_unit_tests() {
  (
    shopt -s nullglob
    local test_files=("$1"/*_test.go)
    [[ ${#test_files[@]} -gt 0 ]] \
      && grep -q '//go:build unit' "${test_files[@]}" 2>/dev/null
  )
}

# ── 3. Summary plumbing ─────────────────────────────────────────────────────
# On a runner, rows are appended as they are produced so a run that dies
# mid-way still shows what it managed to do. Locally they are buffered and the
# table is printed once at the end, because the per-package output shares
# stdout and would interleave into an invalid table.
#
# A summary that cannot be written is itself a reporting failure, so it is
# recorded and turned into a non-zero exit at the end rather than aborting the
# loop: the remaining packages still have to run. Every row is buffered whatever
# the job summary does with it, so a write that starts failing part way through
# leaves a whole table to fall back on rather than the rows after the failure.
declare -a summary_rows=()
summary_failed=0

summary_line() {
  local line="$1"
  summary_rows+=("$line")
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]] && ! echo "$line" >> "$GITHUB_STEP_SUMMARY" 2>/dev/null; then
    summary_failed=1
  fi
  return 0
}

# flush_summary: says where the table went, or prints the buffered one when the
# job summary never took it. The end of the run and the signal handler can both
# ask for the table — a signal arriving after the run flushed but before it
# exited reaches the handler — so only the first ask prints it.
summary_flushed=0

flush_summary() {
  if [[ "$summary_flushed" -ne 0 ]]; then
    return 0
  fi
  summary_flushed=1

  if [[ -n "${GITHUB_STEP_SUMMARY:-}" && "$summary_failed" -eq 0 ]]; then
    echo "Summary written to job summary."
    return 0
  fi
  echo ""
  printf '%s\n' "${summary_rows[@]}"
}

# ── 4. Interrupt handling ───────────────────────────────────────────────────
# A cancelled job signals the whole process group, so gremlins takes the signal
# directly, and bash defers this handler until the foreground child returns: the
# script neither forwards the signal nor reaps separately. The handler annotates
# the package that was running, keeps the output it had produced, flushes the
# table so what did complete stays legible, and exits 128+signal.
#
# This is opportunistic annotation, not a guarantee. A runner that escalates to
# SIGKILL leaves nothing to run, so the red check stays the authoritative signal
# whenever this script cannot finish.
in_flight_pkg=""
in_flight_log=""

# emit_in_flight_log: prints the running package's output once, before whatever
# is about to delete the directory it lives in. A package that already reported
# its own output leaves nothing here to print.
emit_in_flight_log() {
  local log="$in_flight_log"
  in_flight_log=""
  if [[ -z "$log" || ! -f "$log" ]]; then
    return 0
  fi
  echo "::group::$in_flight_pkg gremlins output (interrupted)"
  tail -n 50 "$log" 2>/dev/null || true
  echo "::endgroup::"
}

# cleanup: the run directory goes away last, so an interrupted package's log is
# always emitted before it is removed. The table is flushed here too, so a run
# that ends without reaching the end of the loop still reports what it managed
# to do; a run that already printed its table prints nothing more.
cleanup() {
  emit_in_flight_log
  flush_summary
  if [[ -n "${run_dir:-}" ]]; then
    rm -rf "$run_dir"
  fi
}

# on_signal <name> <number>: annotates the interrupted package and exits
# 128+signal. Further signals are ignored so a second one cannot re-enter the
# handler, and only a package that has not reported yet is given a row.
on_signal() {
  local name="$1" number="$2"
  trap '' INT TERM

  echo ""
  echo "Interrupted by SIG$name."

  if [[ -n "$in_flight_pkg" ]]; then
    echo "$in_flight_pkg: interrupted"
    emit_in_flight_log
    summary_line "| \`$in_flight_pkg\` | interrupted | signal SIG$name | n/a | 0 | 0 | 0 |"
    in_flight_pkg=""
  fi

  flush_summary
  exit $((128 + number))
}

# ── 5. Main ─────────────────────────────────────────────────────────────────
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

  # Filter to packages that have //go:build unit test files of their own
  testable_packages=()
  while IFS= read -r pkg; do
    if has_own_unit_tests "$REPO_ROOT/$pkg"; then
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
  trap cleanup EXIT
  trap 'on_signal INT 2' INT
  trap 'on_signal TERM 15' TERM

  failed_packages=0
  pkg_index=0

  for pkg in "${testable_packages[@]}"; do
    pkg_index=$((pkg_index + 1))
    module_root=$(find_module_root "$pkg")
    # Compute relative package path from the module root. A package that is
    # itself the module root has nothing to strip — the prefix pattern below
    # carries a trailing slash and would leave the absolute path intact — so
    # it is addressed as the current directory instead.
    rel_pkg="${REPO_ROOT}/${pkg#/}"
    if [[ "$rel_pkg" == "$module_root" ]]; then
      rel_pkg="."
    else
      rel_pkg="${rel_pkg#"$module_root"/}"
    fi

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

    # Named while gremlins holds the foreground, so a signal arriving before the
    # row is written finds the package that lost its result and its output.
    in_flight_pkg="$pkg"
    in_flight_log="$log_file"

    # gremlins mutates the whole directory subtree below the package it is
    # invoked on, so a package with sub-packages under it is charged with
    # mutants from code the run was never asked about. --exclude-files takes a
    # regex matched unanchored against every path gremlins walks below the
    # invoked directory, and those paths are always slash-separated whatever the
    # platform — so a path holding a separator is by construction in a
    # sub-directory, and excluding '/' leaves exactly the invoked package. This
    # narrows what is mutated, not what is covered: coverage is still gathered
    # over the whole subtree, and gremlins has no flag to narrow that.
    #
    # No pipeline, so the exit status is gremlins' own and set -e cannot end the
    # run here. gremlins may exit non-zero when mutants survive — that's expected
    # and not a failure; the status never overrules a report, it only says how a
    # run that wrote none ended.
    status=0
    (cd "$module_root" && gremlins unleash \
      --tags unit \
      --timeout-coefficient 10 \
      --workers 4 \
      --exclude-files '/' \
      -o "$report_file" \
      "./$rel_pkg") > "$log_file" 2>&1 || status=$?

    result=$(classify_result "$report_file" "$status")
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
    # The log has been shown, so an interrupt from here on must not repeat it.
    in_flight_log=""

    summary_line "| \`$pkg\` | $result_status | $reason | $score | $killed | $lived | $timed_out |"
    in_flight_pkg=""
  done

  # Emit the summary and the verdict. A table that could not be written to the
  # job summary is printed here instead of being lost.
  flush_summary

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
