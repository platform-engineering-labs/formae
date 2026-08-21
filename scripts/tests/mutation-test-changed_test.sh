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

# Bounds for the signalled runs, so a regression fails a test instead of
# hanging the suite. A healthy run reaches readiness in milliseconds. The kill
# delay is what makes the timeout a bound: the script under test ignores further
# signals while its handler runs, so a SIGTERM alone would never end the run.
READY_INTERVAL=0.01
READY_ATTEMPTS=1000
RUN_TIMEOUT=10
RUN_KILL_DELAY=2

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

# assert_output_count <extended-regex> <expected-count> <description>
assert_output_count() {
  local pattern="$1" expected="$2" description="$3" actual
  actual=$(grep -cE "$pattern" <<< "$script_output" || true)
  if [[ "$actual" != "$expected" ]]; then
    fail "$description (want $expected line(s) matching /$pattern/, got $actual)"
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

# add_base_package <repo> <pkg>: adds a Go source file in <pkg> and folds it
# into origin/main, so the package is in the tree the script runs over but out
# of the diff the script reads. Moving origin/main takes every commit made so
# far with it, so this comes before the changed packages a test wants in view.
add_base_package() {
  local repo="$1" pkg="$2"
  add_untested_package "$repo" "$pkg"
  git -C "$repo" update-ref refs/remotes/origin/main HEAD
}

# stub_gremlins <bin_dir> <body>: installs an executable `gremlins` in
# <bin_dir> that runs <body> instead of the real tool. The body sees gremlins'
# own arguments plus $report_path, the value passed to gremlins' -o flag,
# $target, the ./package argument, and $exclude_files, the value passed to
# gremlins' --exclude-files flag, so a stub can behave differently per package.
stub_gremlins() {
  local bin_dir="$1" body="$2"
  mkdir -p "$bin_dir"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'report_path=""\n'
    printf 'target=""\n'
    printf 'exclude_files=""\n'
    printf 'while [[ $# -gt 0 ]]; do\n'
    printf '  case "$1" in\n'
    printf '    -o) report_path="$2"; shift 2 ;;\n'
    printf '    --exclude-files) exclude_files="$2"; shift 2 ;;\n'
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

# stub_gremlins_breaking_summary_for <bin_dir> <report> <target>: installs a stub
# that writes <report> for every package and, at <target>, also takes write
# permission away from $GITHUB_STEP_SUMMARY — the shape of a job summary that
# stops accepting writes part way through the run.
stub_gremlins_breaking_summary_for() {
  local bin_dir="$1" report="$2" target="$3" body
  body="case \"\$target\" in
  $target) chmod 000 \"\$GITHUB_STEP_SUMMARY\" ;;
esac
printf '%s' '$report' > \"\$report_path\"
exit 0"
  stub_gremlins "$bin_dir" "$body"
}

# stub_gremlins_waiting <bin_dir> <report> <ready_file> <target>: installs a
# stub that writes <report> for every package except <target>, where it prints a
# line, creates <ready_file> and then waits — the shape of a run still in
# progress when the job is cancelled.
stub_gremlins_waiting() {
  local bin_dir="$1" report="$2" ready_file="$3" target="$4" body
  body="case \"\$target\" in
  $target)
    echo 'in-flight gremlins output'
    : > '$ready_file'
    sleep 30
    exit 0
    ;;
esac
printf '%s' '$report' > \"\$report_path\"
exit 0"
  stub_gremlins "$bin_dir" "$body"
}

# stub_gremlins_walking <bin_dir>: installs a stub that emulates gremlins' file
# selection — it walks the whole directory subtree below the target, drops every
# walked path matching the --exclude-files regex it was given, names what is
# left on stdout and writes a report carrying one killed mutant per named file.
# The report names the files the stub selected, so it builds its own body rather
# than taking one from mutation_report. The emulation goes as far as a single
# pattern that means the same thing to grep as it does to the tool's own regex
# engine, which is what the script passes and all this needs to tell apart.
stub_gremlins_walking() {
  local bin_dir="$1"
  stub_gremlins "$bin_dir" '
walked=$(cd "$target" && find . -name "*.go" ! -name "*_test.go" -printf "%P\n" | sort)
if [[ -n "$exclude_files" ]]; then
  walked=$(grep -Ev "$exclude_files" <<< "$walked" || true)
fi
entries=""
separator=""
while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  echo "mutating $file"
  entries+="$separator{\"file_name\": \"$file\", \"mutations\": [{\"status\": \"KILLED\"}]}"
  separator=", "
done <<< "$walked"
printf "{\"files\": [%s]}" "$entries" > "$report_path"
exit 0'
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

# wait_for_file <path>: waits for <path> to appear, bounded by READY_ATTEMPTS.
wait_for_file() {
  local path="$1" attempt=0
  while [[ ! -e "$path" ]]; do
    attempt=$((attempt + 1))
    if [[ "$attempt" -gt "$READY_ATTEMPTS" ]]; then
      return 1
    fi
    sleep "$READY_INTERVAL"
  done
}

# kill_run_group <pgid_file>: kills every process in the session a signalled run
# was started in, so neither the script under test nor its stub gremlins can
# outlive the test. The run's own timeout escalates to the process it started,
# not to the session that process created, so a run that is killed for taking
# too long can still leave the session behind.
kill_run_group() {
  local pgid_file="$1" pgid
  pgid=$(cat "$pgid_file" 2>/dev/null || echo)
  if [[ -n "$pgid" ]]; then
    kill -s KILL -- "-$pgid" 2>/dev/null || true
  fi
}

# run_script_signalled <repo> <bin_dir> <ready_file> <signal>: runs the script
# under test in a session of its own, waits for the stub gremlins to report that
# it is running, then sends <signal> to the whole process group the way a
# cancelled job does — so the stub takes the signal directly and bash runs the
# script's deferred handler. The run is capped by RUN_TIMEOUT, and the script is
# the session leader, so the status waited on is the script's own. The session is
# killed once the run is over however it ended.
run_script_signalled() {
  local repo="$1" bin_dir="$2" ready_file="$3" signal="$4"
  local work out_file pgid_file pgid runner_pid
  work=$(new_workdir)
  out_file="$work/output"
  pgid_file="$work/pgid"
  script_output=""
  script_status=0
  rm -f "$ready_file"

  timeout -k "$RUN_KILL_DELAY" "$RUN_TIMEOUT" setsid --wait bash -c '
    echo "$$" > "$1"
    cd "$2" || exit 1
    exec env -u GITHUB_STEP_SUMMARY GITHUB_BASE_REF=main PATH="$3:$PATH" \
      bash "$4"
  ' _ "$pgid_file" "$repo" "$bin_dir" "$SCRIPT_UNDER_TEST" > "$out_file" 2>&1 &
  runner_pid=$!

  if wait_for_file "$ready_file"; then
    pgid=$(cat "$pgid_file")
    kill -s "$signal" -- "-$pgid" 2>/dev/null || true
  else
    fail "the stub gremlins never reported that it was running"
    kill_run_group "$pgid_file"
  fi

  wait "$runner_pid" || script_status=$?
  kill_run_group "$pgid_file"
  script_output=$(cat "$out_file")
}

# classify_report_with_path <path> <report> [exit-status]: writes <report> to a
# throwaway file and puts classify_result's verdict for it in $classify_output,
# with $PATH set to <path> for the classification. Pass the literal ABSENT for a
# report gremlins never wrote, and <exit-status> for a run that ended with
# anything other than the 0 assumed here. The script is sourced in a subshell,
# so its own definitions cannot leak into the test run.
classify_report_with_path() {
  local path="$1" report="$2" exit_status="${3:-0}" report_file
  report_file="$(new_workdir)/report.json"
  if [[ "$report" != "ABSENT" ]]; then
    printf '%s' "$report" > "$report_file"
  fi
  classify_output=""
  # shellcheck disable=SC1090
  classify_output=$(
    PATH="$path"
    source "$SCRIPT_UNDER_TEST"
    classify_result "$report_file" "$exit_status"
  ) || true
}

# classify_report <report> [exit-status]: classify_report_with_path on the
# test's own PATH.
classify_report() {
  classify_report_with_path "$PATH" "$@"
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

# A gremlins that dies leaves no report either, but it exits non-zero doing it.
# The row names the status, so a run that died is not read as a cancelled one.
test_a_non_zero_exit_without_a_report_names_the_status() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins "$bin" 'echo "Gathering coverage"; exit 2'

  run_script "$repo" "$bin"

  assert_output_matches \
    '\| `example/pkg` \| failed \| gremlins exited 2 without a report \|' \
    "the package row names the exit status"
  assert_output_matches 'gremlins exit status: 2' \
    "the exit status is recorded in the step log"
  assert_status_nonzero "a run that produced no report fails the run"
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

# A changed package that is its own module root (it holds a go.mod) is invoked
# on itself: gremlins runs in that directory targeting the directory, not a
# path re-rooted below it.
test_a_module_root_package_is_invoked_on_itself() {
  local work repo bin invocation
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"
  invocation="$work/invocation"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "pkg/plugin"
  printf 'module example/plugin\n\ngo 1.26\n' > "$repo/pkg/plugin/go.mod"
  fixture_commit "$repo" "make pkg/plugin its own module"

  stub_gremlins "$bin" "pwd > '$invocation'; echo \"\$target\" >> '$invocation'
printf '%s' '$(mutation_report KILLED)' > \"\$report_path\"; exit 0"

  run_script "$repo" "$bin"

  assert_status 0 "a module-root package produces a usable result"
  assert_output_matches '\| `pkg/plugin` \| ok \|' \
    "the module-root package row is ok"
  if [[ "$(sed -n 1p "$invocation" 2>/dev/null)" != "$repo/pkg/plugin" ]]; then
    fail "gremlins did not run in the package's own module root (got '$(sed -n 1p "$invocation" 2>/dev/null)')"
  fi
  if [[ "$(sed -n 2p "$invocation" 2>/dev/null)" != "./." ]]; then
    fail "gremlins was not invoked on the module root itself (got '$(sed -n 2p "$invocation" 2>/dev/null)')"
  fi
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

# gremlins prints the coverage run's own output when coverage fails, so a unit
# test that panics puts panic text in gremlins' output without gremlins having
# died. The output is never read for a verdict: a run that wrote a report is a
# result whatever it printed and whatever it exited with.
test_panic_text_beside_a_report_is_still_ok() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins "$bin" "echo 'panic: send on closed channel'
echo 'goroutine 1 [running]:'
printf '%s' '$(mutation_report KILLED)' > \"\$report_path\"
exit 1"

  run_script "$repo" "$bin"

  assert_output_matches '\| `example/pkg` \| ok \| - \| 100\.0% \| 1 \| 0 \| 0 \|' \
    "a panicking test does not cost the package its result"
  assert_output_matches 'panic: send on closed channel' \
    "the panic text is still there for a reader to find"
  assert_status 0 "panic text in the output does not fail the run"
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

# With no report the tool on PATH is checked before the exit status, because a
# gremlins that is not installed leaves the shell exiting 127 and that status
# says nothing about a run that never happened. An installed gremlins is named
# from its status: 0 is the shape of a cancelled run, non-zero one that ended
# before it could write.
test_a_missing_report_is_named_from_the_tool_and_the_exit_status() {
  local work bin
  work=$(new_workdir)
  bin="$work/bin"
  stub_gremlins "$bin" 'exit 0'

  classify_report_with_path "$bin" ABSENT
  assert_classification "failed|no output|n/a|0|0|0" \
    "a gremlins that wrote nothing is a failure"

  classify_report_with_path "$bin" ABSENT 2
  assert_classification "failed|gremlins exited 2 without a report|n/a|0|0|0" \
    "a gremlins that ended without writing is named by its status"

  classify_report_with_path "" ABSENT
  assert_classification "failed|gremlins not found|n/a|0|0|0" \
    "an uninstalled gremlins is named"

  classify_report_with_path "" ABSENT 127
  assert_classification "failed|gremlins not found|n/a|0|0|0" \
    "an uninstalled gremlins is named whatever status the shell left behind"
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
  assert_output_count '^## Mutation Testing' 0 \
    "a no-op run prints no table"
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
  assert_output_count '^## Mutation Testing' 0 \
    "a no-op run prints no table"
  assert_status 0 "a package with no unit tests passes"
}

# The filter is what puts a package in front of gremlins, so a package carrying
# its own unit-tagged test has to come out of it and be run.
test_a_package_with_its_own_unit_tests_is_selected() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 "$(mutation_report KILLED)"

  run_script "$repo" "$bin"

  assert_output_matches '^Packages to test: example/pkg$' \
    "the package with its own unit-tagged test is selected"
  assert_output_matches '\| `example/pkg` \| ok \| - \| 100\.0% \| 1 \| 0 \| 0 \|' \
    "the selected package is run and reported"
  assert_status 0 "a package with its own unit tests passes"
}

# The unit-tagged tests that make a package mutable are the ones beside its own
# source: a sub-package's tests run its code, not its parent's, so they must not
# put the parent in front of gremlins.
test_a_sub_packages_unit_tests_do_not_select_its_parent() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_untested_package "$repo" "example/pkg"
  add_changed_package "$repo" "example/pkg/sub"
  stub_gremlins_writing "$bin" 0 "$(mutation_report KILLED)"

  run_script "$repo" "$bin"

  assert_output_matches '^Packages to test: example/pkg/sub$' \
    "only the package with its own unit-tagged test is selected"
  assert_output_count '^\| `example/pkg` \|' 0 \
    "the parent package is not run"
  assert_output_matches '\| `example/pkg/sub` \| ok \| - \| 100\.0% \| 1 \| 0 \| 0 \|' \
    "the sub-package is still run"
  assert_status 0 "a parent package left unrun does not fail the run"
}

# A run with nothing to mutate has no table for the job summary to take, so it
# must not report one either. Both ways of having nothing to mutate are checked,
# because each leaves the run at a different point.
test_a_no_op_run_reports_no_job_summary() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  stub_gremlins "$bin" 'echo "gremlins must not run"; exit 1'

  run_script "$repo" "$bin" "$work/no-changes.md"

  assert_output_count 'Summary written to job summary' 0 \
    "a run with no changed Go files reports no job summary"
  assert_status 0 "a run with no changed Go files passes"

  add_untested_package "$repo" "example/pkg"

  run_script "$repo" "$bin" "$work/no-unit-tests.md"

  assert_output_count 'Summary written to job summary' 0 \
    "a run with no unit-tagged tests reports no job summary"
  assert_status 0 "a run with no unit-tagged tests passes"
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

# A summary that takes the first rows and then stops accepting writes still owes
# the step log a whole table: the header and the rows it did take have to come
# back too, not just the rows written after the failure.
test_a_summary_write_that_fails_part_way_keeps_the_whole_table() {
  local work repo bin summary
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"
  summary="$work/summary.md"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/a"
  add_changed_package "$repo" "example/b"
  stub_gremlins_breaking_summary_for "$bin" "$(mutation_report KILLED)" ./example/b

  run_script "$repo" "$bin" "$summary"

  assert_output_matches '^## Mutation Testing' \
    "the fallback table keeps its header"
  assert_output_matches '^\| `example/a` \| ok \| - \| 100\.0% \|' \
    "the row the summary took is in the fallback table"
  assert_output_matches '^\| `example/b` \| ok \| - \| 100\.0% \|' \
    "the row the summary refused is in the fallback table"
  assert_output_matches 'The job summary could not be written' \
    "the script says the summary was lost"
  assert_status_nonzero "a summary that stops accepting writes fails the run"
}

# A job summary that took the table is where the table belongs: repeating it in
# the step log would print it twice on every healthy run.
test_a_written_summary_is_not_repeated_in_the_step_log() {
  local work repo bin summary
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"
  summary="$work/summary.md"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_writing "$bin" 0 "$(mutation_report KILLED)"

  run_script "$repo" "$bin" "$summary"

  assert_output_matches 'Summary written to job summary' \
    "the script says where the table went"
  assert_output_count '^\| `example/pkg` \|' 0 \
    "the table is not repeated in the step log"
  if ! grep -qE '^\| `example/pkg` \| ok \|' "$summary"; then
    fail "the row is missing from the job summary"
  fi
  assert_status 0 "a written summary passes"
}

# The end of the run and a signal arriving before it exits both ask for the
# table, so the second ask has to print nothing.
test_the_table_is_printed_once() {
  script_output=$(
    unset GITHUB_STEP_SUMMARY
    # shellcheck disable=SC1090
    source "$SCRIPT_UNDER_TEST"
    summary_rows=("## Mutation Testing (changed packages)" "| \`example/a\` | ok |")
    flush_summary
    flush_summary
  )
  script_status=0

  assert_output_count '^## Mutation Testing' 1 \
    "the table header is printed once"
  assert_output_count '^\| `example/a` \|' 1 \
    "the rows are printed once"
}

# assert_interrupted_by <signal> <expected-status>: drives one cancelled run and
# asserts what the annotation has to say about it.
assert_interrupted_by() {
  local signal="$1" expected_status="$2"
  local work repo bin ready
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"
  ready="$work/ready"

  make_fixture_repo "$repo"
  add_changed_package "$repo" "example/a"
  add_changed_package "$repo" "example/b"
  stub_gremlins_waiting "$bin" "$(mutation_report KILLED)" "$ready" ./example/b

  run_script_signalled "$repo" "$bin" "$ready" "$signal"

  assert_output_matches "\\| \`example/b\` \\| interrupted \\| signal SIG$signal \\|" \
    "SIG$signal: the package that was running is marked interrupted"
  assert_output_matches 'in-flight gremlins output' \
    "SIG$signal: the output of the running package reaches the log"
  assert_output_matches '\| `example/a` \| ok \| - \| 100\.0% \|' \
    "SIG$signal: the package that finished keeps its result"
  assert_output_count '^\| `example/a` \|' 1 \
    "SIG$signal: a package that already reported gets no second row"
  assert_output_count '^\| `example/b` \|' 1 \
    "SIG$signal: the interrupted package gets exactly one row"
  assert_status "$expected_status" "SIG$signal: the script exits 128 plus the signal"
}

# A cancelled job signals the whole process group. The package that was running
# is the one that lost its result, so it is the one annotated — with the output
# it produced before it died, which lives in a directory the script is about to
# delete. A package that already reported keeps its single row.
test_an_interrupted_package_is_annotated_and_its_output_kept() {
  assert_interrupted_by INT 130
  assert_interrupted_by TERM 143
}

# gremlins walks the whole directory subtree below the package it is invoked on,
# so a package that has sub-packages under it would be charged with mutants from
# code the run was never asked about. The run has to cover the invoked package's
# own files and nothing below them.
test_only_the_invoked_package_is_mutated() {
  local work repo bin
  work=$(new_workdir)
  repo="$work/repo"
  bin="$work/bin"

  make_fixture_repo "$repo"
  add_base_package "$repo" "example/pkg/sub"
  add_changed_package "$repo" "example/pkg"
  stub_gremlins_walking "$bin"

  run_script "$repo" "$bin"

  assert_output_matches '^mutating code\.go$' \
    "the invoked package's own file is mutated"
  assert_output_count '^mutating sub/code\.go$' 0 \
    "the sub-package's file is not mutated"
  assert_output_matches '\| `example/pkg` \| ok \| - \| 100\.0% \| 1 \| 0 \| 0 \|' \
    "the report counts one mutant, from the one file that was selected"
  assert_status 0 "a run confined to the invoked package passes"
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
  run_test test_a_non_zero_exit_without_a_report_names_the_status
  run_test test_a_report_with_a_zero_exit_is_ok
  run_test test_a_module_root_package_is_invoked_on_itself
  run_test test_surviving_mutants_are_not_a_failure
  run_test test_an_unknown_non_zero_exit_with_a_report_is_ok
  run_test test_panic_text_beside_a_report_is_still_ok
  run_test test_a_zero_mutant_report_is_ok_without_a_score
  run_test test_a_report_with_no_scored_mutants_says_why_it_has_no_score
  run_test test_an_n_a_score_always_names_its_cause
  run_test test_an_unrecognized_mutation_status_is_named_in_the_reason
  run_test test_many_unrecognized_statuses_are_capped_in_the_reason
  run_test test_a_missing_gremlins_is_reported_by_name
  run_test test_invalid_reports_are_classified_as_invalid_output
  run_test test_a_report_that_is_not_a_gremlins_report_fails_the_package
  run_test test_a_missing_report_is_named_from_the_tool_and_the_exit_status
  run_test test_a_missing_interpreter_is_reported_by_name
  run_test test_every_package_runs_when_the_first_one_fails
  run_test test_two_failures_produce_two_rows
  run_test test_colliding_package_paths_do_not_share_a_report
  run_test test_no_changed_go_files_is_a_no_op
  run_test test_packages_without_unit_tests_are_a_no_op
  run_test test_a_package_with_its_own_unit_tests_is_selected
  run_test test_a_sub_packages_unit_tests_do_not_select_its_parent
  run_test test_a_no_op_run_reports_no_job_summary
  run_test test_a_failing_summary_write_fails_the_run
  run_test test_a_summary_write_that_fails_part_way_keeps_the_whole_table
  run_test test_a_written_summary_is_not_repeated_in_the_step_log
  run_test test_the_table_is_printed_once
  run_test test_an_interrupted_package_is_annotated_and_its_output_kept
  run_test test_only_the_invoked_package_is_mutated

  echo ""
  if [[ "$tests_failed" -gt 0 ]]; then
    echo "$tests_failed of $tests_run test(s) failed."
    exit 1
  fi
  echo "$tests_run test(s) passed."
}

main "$@"
