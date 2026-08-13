#!/usr/bin/env bash
# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2
#
set -euo pipefail

# Tests for scripts/mutation-test-changed.sh.
#
# Each test builds a throwaway git repository fixture and a stub `gremlins`
# first on PATH, runs the script inside the fixture, and asserts on the
# captured output and exit status. No network, no Go toolchain and no real
# gremlins are involved.

TESTS_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SCRIPT_UNDER_TEST="$TESTS_DIR/../mutation-test-changed.sh"

TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT

tests_run=0
tests_failed=0
current_test_failed=0
script_output=""
script_status=0
classify_output=""

# ── 1. Assertions ───────────────────────────────────────────────────────────
fail() {
  local message="$1"
  current_test_failed=1
  echo "  $message"
}

# assert_output_matches <extended-regex> <description>
assert_output_matches() {
  local pattern="$1" description="$2"
  if ! grep -qE "$pattern" <<< "$script_output"; then
    fail "$description (no line matching /$pattern/)"
  fi
}

# assert_status <expected-status> <description>
assert_status() {
  local expected="$1" description="$2"
  if [[ "$script_status" != "$expected" ]]; then
    fail "$description (want exit status $expected, got $script_status)"
  fi
}

# assert_status_nonzero <description>
assert_status_nonzero() {
  local description="$1"
  if [[ "$script_status" == "0" ]]; then
    fail "$description (want a non-zero exit status, got 0)"
  fi
}

# assert_classification <expected-result> <description>
assert_classification() {
  local expected="$1" description="$2"
  if [[ "$classify_output" != "$expected" ]]; then
    fail "$description (want '$expected', got '$classify_output')"
  fi
}

# ── 2. Fixtures ─────────────────────────────────────────────────────────────
# new_workdir: prints a fresh empty directory for a single test.
new_workdir() {
  mktemp -d "$TMP_ROOT/work.XXXXXX"
}

# fixture_commit <repo> <message>: commits everything in the fixture repo with
# an identity supplied per invocation, so the test needs no global git config.
fixture_commit() {
  local repo="$1" message="$2"
  git -C "$repo" add -A
  git -C "$repo" \
    -c user.name=fixture -c user.email=fixture@example.invalid \
    -c commit.gpgsign=false \
    commit -q -m "$message"
}

# make_fixture_repo <repo>: creates a git repository whose origin/main ref
# points at a commit with no Go packages, so anything added afterwards shows up
# as changed in `git diff origin/main...HEAD`.
make_fixture_repo() {
  local repo="$1"
  mkdir -p "$repo"
  git -C "$repo" init -q -b main
  printf 'module example\n\ngo 1.26\n' > "$repo/go.mod"
  fixture_commit "$repo" "base"
  git -C "$repo" update-ref refs/remotes/origin/main HEAD
}

# add_changed_package <repo> <pkg>: adds a Go source file and a unit-tagged
# test file in <pkg> and commits them on top of origin/main.
add_changed_package() {
  local repo="$1" pkg="$2" name
  name=$(basename "$pkg")
  mkdir -p "$repo/$pkg"
  printf 'package %s\n\nfunc Add(a, b int) int { return a + b }\n' "$name" \
    > "$repo/$pkg/code.go"
  printf '//go:build unit\n\npackage %s\n' "$name" > "$repo/$pkg/code_test.go"
  fixture_commit "$repo" "change $pkg"
}

# add_untested_package <repo> <pkg>: adds a Go source file in <pkg> with no
# unit-tagged test beside it and commits it on top of origin/main.
add_untested_package() {
  local repo="$1" pkg="$2" name
  name=$(basename "$pkg")
  mkdir -p "$repo/$pkg"
  printf 'package %s\n\nfunc Add(a, b int) int { return a + b }\n' "$name" \
    > "$repo/$pkg/code.go"
  fixture_commit "$repo" "change $pkg"
}

# stub_gremlins <bin_dir> <body>: installs an executable `gremlins` in
# <bin_dir> that runs <body> instead of the real tool. The body sees gremlins'
# own arguments plus $report_path, the value passed to gremlins' -o flag, and
# $target, the ./package argument, so a stub can behave differently per package.
stub_gremlins() {
  local bin_dir="$1" body="$2"
  mkdir -p "$bin_dir"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'report_path=""\n'
    printf 'target=""\n'
    printf 'while [[ $# -gt 0 ]]; do\n'
    printf '  case "$1" in\n'
    printf '    -o) report_path="$2"; shift 2 ;;\n'
    printf '    ./*) target="$1"; shift ;;\n'
    printf '    *) shift ;;\n'
    printf '  esac\n'
    printf 'done\n'
    printf '%s\n' "$body"
  } > "$bin_dir/gremlins"
  chmod +x "$bin_dir/gremlins"
}

# mutation_report <status>...: prints a gremlins-shaped report carrying one
# mutation per status given. With no arguments it is a zero-mutant report.
mutation_report() {
  local status separator=""
  printf '{"files": [{"file_name": "code.go", "mutations": ['
  for status in "$@"; do
    printf '%s{"status": "%s"}' "$separator" "$status"
    separator=", "
  done
  printf ']}]}'
}

# stub_gremlins_writing <bin_dir> <exit-status> <report>: installs a stub that
# writes <report> to gremlins' -o path and then exits with <exit-status>.
stub_gremlins_writing() {
  local bin_dir="$1" exit_status="$2" report="$3"
  stub_gremlins "$bin_dir" \
    "printf '%s' '$report' > \"\$report_path\"; exit $exit_status"
}

# stub_gremlins_failing_for <bin_dir> <report> <target>...: installs a stub that
# writes <report> for every package except the given ./targets, which it leaves
# without a report after exiting 0 — the shape of a cancelled run.
stub_gremlins_failing_for() {
  local bin_dir="$1" report="$2" target body
  shift 2
  body='case "$target" in'
  for target in "$@"; do
    body+=" $target) exit 0 ;;"
  done
  body+=' esac'
  body+="
printf '%s' '$report' > \"\$report_path\"
exit 0"
  stub_gremlins "$bin_dir" "$body"
}

# run_script <repo> <bin_dir> [summary_file]: runs the script under test inside
# the fixture repo with the stub first on PATH, capturing output and exit
# status. With <summary_file> the script sees it as $GITHUB_STEP_SUMMARY.
run_script() {
  local repo="$1" bin_dir="$2" summary_file="${3:-}"
  script_output=""
  script_status=0
  if [[ -n "$summary_file" ]]; then
    script_output=$(cd "$repo" && env GITHUB_STEP_SUMMARY="$summary_file" \
      GITHUB_BASE_REF=main PATH="$bin_dir:$PATH" \
      bash "$SCRIPT_UNDER_TEST" 2>&1) || script_status=$?
    return 0
  fi
  script_output=$(cd "$repo" && env -u GITHUB_STEP_SUMMARY \
    GITHUB_BASE_REF=main PATH="$bin_dir:$PATH" \
    bash "$SCRIPT_UNDER_TEST" 2>&1) || script_status=$?
}

# classify_report_with_path <path> <report>: writes <report> to a throwaway
# file and puts classify_result's verdict for it in $classify_output, with
# $PATH set to <path> for the classification. Pass the literal ABSENT for a
# report gremlins never wrote. The script is sourced in a subshell, so its own
# definitions cannot leak into the test run.
classify_report_with_path() {
  local path="$1" report="$2" report_file
  report_file="$(new_workdir)/report.json"
  if [[ "$report" != "ABSENT" ]]; then
    printf '%s' "$report" > "$report_file"
  fi
  classify_output=""
  # shellcheck disable=SC1090
  classify_output=$(
    PATH="$path"
    source "$SCRIPT_UNDER_TEST"
    classify_result "$report_file"
  ) || true
}

# classify_report <report>: classify_report_with_path on the test's own PATH.
classify_report() {
  classify_report_with_path "$PATH" "$1"
}

# ── 3. Tests ────────────────────────────────────────────────────────────────
# A cancelled gremlins run prints progress, exits 0 and never writes its
# report: the package produced no result and the script must say so.
test_exit_zero_without_report_is_a_failure() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins "$bin" 'echo "Gathering coverage"; echo "Mutating source"; exit 0'

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| failed \| no output \|' \
    "the package row reports the failure"
  assert_status_nonzero "the script fails when a package produced no result"
}

# A completed run is reported with its score whatever gremlins exited with.
test_a_report_with_a_zero_exit_is_ok() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 "$(mutation_report KILLED KILLED LIVED)"

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| ok \| - \| 66\.7% \| 2 \| 1 \| 0 \|' \
    "the package row carries the score"
  assert_status 0 "a completed run passes"
}

# Surviving mutants make gremlins exit non-zero: advisory content, not a crash.
test_surviving_mutants_are_not_a_failure() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 1 "$(mutation_report KILLED LIVED 'TIMED OUT')"

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| ok \| - \| 50\.0% \| 1 \| 1 \| 1 \|' \
    "survivors are still a usable result"
  assert_status 0 "surviving mutants do not fail the run"
}

# Any other non-zero exit is a diagnostic in the step log, never the verdict.
test_an_unknown_non_zero_exit_with_a_report_is_ok() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 42 "$(mutation_report KILLED)"

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| ok \| - \| 100\.0% \| 1 \| 0 \| 0 \|' \
    "an unknown exit status does not change the result"
  assert_output_matches 'gremlins exit status: 42' \
    "the exit status is recorded in the step log"
  assert_status 0 "an unknown exit status alone does not fail the run"
}

# A package with no mutants is the only legitimate source of an n/a score.
test_a_zero_mutant_report_is_ok_without_a_score() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 "$(mutation_report)"

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| ok \| no mutants \| n/a \| 0 \| 0 \| 0 \|' \
    "an empty report says why it has no score"
  assert_status 0 "a package with no mutants passes"
}

# Mutants the unit tests never reach score nothing, and the row says so instead
# of leaving the reason blank beside an n/a score.
test_a_report_with_no_scored_mutants_says_why_it_has_no_score() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 "$(mutation_report 'NOT COVERED' 'NOT COVERED')"

  run_script "$repo" "$bin"

  assert_output_matches \
    '\| `example/pkg` \| ok \| no scored mutants \(2 not covered\) \| n/a \| 0 \| 0 \| 0 \|' \
    "an uncovered package says why it has no score"
  assert_status 0 "a package whose mutants are all uncovered passes"
}

# Every n/a score names the mutants that produced it, whatever their status.
test_an_n_a_score_always_names_its_cause() {
  classify_report "$(mutation_report)"
  assert_classification "ok|no mutants|n/a|0|0|0" \
    "a report with no mutants at all says so"

  classify_report "$(mutation_report 'NOT COVERED' 'NOT COVERED')"
  assert_classification "ok|no scored mutants (2 not covered)|n/a|0|0|0" \
    "uncovered mutants are counted in the reason"

  classify_report "$(mutation_report 'TIMED OUT' 'TIMED OUT' 'TIMED OUT')"
  assert_classification "ok|no scored mutants (3 timed out)|n/a|0|0|3" \
    "timed out mutants are counted in the reason"

  classify_report "$(mutation_report 'NOT COVERED' 'TIMED OUT' 'TIMED OUT')"
  assert_classification "ok|no scored mutants (1 not covered, 2 timed out)|n/a|0|0|2" \
    "a mix of unscored mutants is counted by status"
}

# A status the script does not know is counted and named, never dropped.
test_an_unrecognized_mutation_status_is_named_in_the_reason() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 "$(mutation_report KILLED SKIPPED SKIPPED)"

  run_script "$repo" "$bin"

  assert_output_matches \
    '\| `example/pkg` \| ok \| unrecognized status: SKIPPED \(2\) \| 100\.0% \| 1 \| 0 \| 0 \|' \
    "the unrecognized status is counted and named"
  assert_status 0 "an unrecognized status does not fail the run"
}

# The reason shares a table cell with the rest of the row, so a report full of
# unknown statuses names a few of them and counts the rest.
test_many_unrecognized_statuses_are_capped_in_the_reason() {
  classify_report "$(mutation_report KILLED EXTRA1 EXTRA2 EXTRA3 EXTRA4 EXTRA5)"

  assert_classification \
    "ok|unrecognized status: EXTRA1 (1), EXTRA2 (1), EXTRA3 (1) (+2 more)|100.0%|1|0|0" \
    "the reason names a few statuses and counts the remainder"
}

# Without gremlins on PATH every package is unrunnable, and says so.
test_a_missing_gremlins_is_reported_by_name() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  mkdir -p "$bin"

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| failed \| gremlins not found \|' \
    "the row names the missing tool"
  assert_status_nonzero "a missing gremlins fails the run"
}

# Reports are validated, not just parsed: anything not shaped like a gremlins
# report is a classification outcome, never an abort.
test_invalid_reports_are_classified_as_invalid_output() {
  local invalid="failed|invalid output|n/a|0|0|0"

  classify_report ""
  assert_classification "$invalid" "an empty report is invalid"

  classify_report '{"files": [{"mutations": [{"status": "KIL'
  assert_classification "$invalid" "a truncated report is invalid"

  classify_report '{}'
  assert_classification "$invalid" "an empty object is invalid"

  classify_report 'null'
  assert_classification "$invalid" "a null document is invalid"

  classify_report '{"error": "no test files found"}'
  assert_classification "$invalid" "an error object is invalid"

  classify_report '{"files": {}}'
  assert_classification "$invalid" "files that is not a list is invalid"

  classify_report '{"files": ["code.go"]}'
  assert_classification "$invalid" "files entries that are not objects are invalid"

  classify_report '{"files": [{"mutations": [{"status": 7}]}]}'
  assert_classification "$invalid" "a mutation status that is not a string is invalid"
}

# A report that parses but says nothing about mutations is not a result the run
# can be called done on.
test_a_report_that_is_not_a_gremlins_report_fails_the_package() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 '{}'

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| failed \| invalid output \|' \
    "the row reports the unusable report"
  assert_status_nonzero "an unusable report fails the run"
}

# With no report it is the presence of the tool, not the exit status of the run,
# that names the reason: a gremlins that ran and wrote nothing produced no
# output, and one that is not installed at all is named.
test_a_missing_report_is_named_from_the_tool_on_path() {
  local work bin
  work=$(new_workdir)
  bin="$work/bin"
  stub_gremlins "$bin" 'exit 0'

  classify_report_with_path "$bin" ABSENT
  assert_classification "failed|no output|n/a|0|0|0" \
    "a gremlins that wrote nothing is a failure"

  classify_report_with_path "" ABSENT
  assert_classification "failed|gremlins not found|n/a|0|0|0" \
    "an uninstalled gremlins is named"
}

# A report the script cannot read for want of an interpreter is a broken
# environment, not a broken report, and is named as such.
test_a_missing_interpreter_is_reported_by_name() {
  classify_report_with_path "" "$(mutation_report KILLED)"

  assert_classification "failed|python3 not found|n/a|0|0|0" \
    "the missing interpreter is named"
}

# One dead package must not cost the others their result.
test_every_package_runs_when_the_first_one_fails() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/a"
  add_changed_package "$repo" "example/b"
  add_changed_package "$repo" "example/c"
  stub_gremlins_failing_for "$bin" "$(mutation_report KILLED)" ./example/a

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/a` \| failed \| no output \|' \
    "the first package reports its failure"
  assert_output_matches '\| `example/b` \| ok \| - \| 100\.0% \|' \
    "the second package still ran"
  assert_output_matches '\| `example/c` \| ok \| - \| 100\.0% \|' \
    "the third package still ran"
  assert_status_nonzero "one dead package fails the run"
}

# Two dead packages must not collapse into a single row.
test_two_failures_produce_two_rows() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/a"
  add_changed_package "$repo" "example/b"
  add_changed_package "$repo" "example/c"
  stub_gremlins_failing_for "$bin" "$(mutation_report KILLED)" ./example/a ./example/c

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/a` \| failed \| no output \|' \
    "the first failure has its own row"
  assert_output_matches '\| `example/b` \| ok \| - \| 100\.0% \|' \
    "the package between the failures still ran"
  assert_output_matches '\| `example/c` \| failed \| no output \|' \
    "the second failure has its own row"
  assert_output_matches '2 of 3 package\(s\) produced no usable result' \
    "both failures are counted"
  assert_status_nonzero "two dead packages fail the run"
}

# Two package paths that differ only in a separator flatten to the same name, so
# one must never be handed the other's report.
test_colliding_package_paths_do_not_share_a_report() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "a/b_c"
  add_changed_package "$repo" "a_b/c"
  stub_gremlins_failing_for "$bin" "$(mutation_report KILLED)" ./a_b/c

  run_script "$repo" "$bin"

  assert_output_matches '\| `a/b_c` \| ok \| - \| 100\.0% \|' \
    "the package that ran keeps its own result"
  assert_output_matches '\| `a_b/c` \| failed \| no output \|' \
    "the package that produced nothing does not inherit a result"
  assert_status_nonzero "a package that produced no result fails the run"
}

# Nothing to mutate is not a failure.
test_no_changed_go_files_is_a_no_op() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  stub_gremlins "$bin" 'echo "gremlins must not run"; exit 1'

  run_script "$repo" "$bin"

  assert_output_matches 'No Go source files changed' \
    "the script says there is nothing to mutate"
  assert_status 0 "an unrelated change passes"
}

test_packages_without_unit_tests_are_a_no_op() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_untested_package "$repo" "example/pkg"
  stub_gremlins "$bin" 'echo "gremlins must not run"; exit 1'

  run_script "$repo" "$bin"

  assert_output_matches 'no unit-tagged tests' \
    "the script says the packages are not mutable"
  assert_status 0 "a package with no unit tests passes"
}

# A summary that cannot be written is a reporting failure of its own. It still
# has to let every package run, and the table it could not write has to reach
# the step log instead of being lost.
test_a_failing_summary_write_fails_the_run() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/a"
  add_changed_package "$repo" "example/b"
  stub_gremlins_writing "$bin" 0 "$(mutation_report KILLED)"

  run_script "$repo" "$bin" "$work/no-such-directory/summary.md"

  assert_output_matches 'example/a: ok' "the first package still ran"
  assert_output_matches 'example/b: ok' "the second package still ran"
  assert_output_matches '\| `example/a` \| ok \| - \| 100\.0% \|' \
    "the first package's row survives the failed write"
  assert_output_matches '\| `example/b` \| ok \| - \| 100\.0% \|' \
    "the second package's row survives the failed write"
  assert_output_matches 'The job summary could not be written' \
    "the script says the summary was lost"
  assert_status_nonzero "an unwritable summary fails the run"
}

# ── 4. Runner ───────────────────────────────────────────────────────────────
run_test() {
  local test_name="$1"
  current_test_failed=0
  tests_run=$((tests_run + 1))
  echo "RUN  $test_name"
  "$test_name"
  if [[ "$current_test_failed" == "0" ]]; then
    echo "PASS $test_name"
    return
  fi
  tests_failed=$((tests_failed + 1))
  echo "FAIL $test_name (script exit status $script_status), script output:"
  sed 's/^/  | /' <<< "$script_output"
}

main() {
  run_test test_exit_zero_without_report_is_a_failure
  run_test test_a_report_with_a_zero_exit_is_ok
  run_test test_surviving_mutants_are_not_a_failure
  run_test test_an_unknown_non_zero_exit_with_a_report_is_ok
  run_test test_a_zero_mutant_report_is_ok_without_a_score
  run_test test_a_report_with_no_scored_mutants_says_why_it_has_no_score
  run_test test_an_n_a_score_always_names_its_cause
  run_test test_an_unrecognized_mutation_status_is_named_in_the_reason
  run_test test_many_unrecognized_statuses_are_capped_in_the_reason
  run_test test_a_missing_gremlins_is_reported_by_name
  run_test test_invalid_reports_are_classified_as_invalid_output
  run_test test_a_report_that_is_not_a_gremlins_report_fails_the_package
  run_test test_a_missing_report_is_named_from_the_tool_on_path
  run_test test_a_missing_interpreter_is_reported_by_name
  run_test test_every_package_runs_when_the_first_one_fails
  run_test test_two_failures_produce_two_rows
  run_test test_colliding_package_paths_do_not_share_a_report
  run_test test_no_changed_go_files_is_a_no_op
  run_test test_packages_without_unit_tests_are_a_no_op
  run_test test_a_failing_summary_write_fails_the_run

  echo ""
  if [[ "$tests_failed" -gt 0 ]]; then
    echo "$tests_failed of $tests_run test(s) failed."
    exit 1
  fi
  echo "$tests_run test(s) passed."
}

main "$@"
