#!/usr/bin/env bash
# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2
#
set -euo pipefail

# Tests for scripts/go_modules.sh.
#
# Each test builds a throwaway git repository fixture and runs the script
# against it, asserting on the captured stdout, stderr and exit status. No
# network and no Go toolchain are involved: the script only reads Git's index.

TESTS_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$TESTS_DIR/../.." && pwd)
SCRIPT_UNDER_TEST="$TESTS_DIR/../go_modules.sh"

TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT

tests_run=0
tests_failed=0
current_test_failed=0
script_stdout=""
script_stderr=""
script_status=0

# ── 1. Assertions ───────────────────────────────────────────────────────────
fail() {
  local message="$1"
  current_test_failed=1
  echo "  $message"
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

# assert_stdout_equals <expected> <description>
assert_stdout_equals() {
  local expected="$1" description="$2"
  if [[ "$script_stdout" != "$expected" ]]; then
    fail "$description (want:
$expected
got:
$script_stdout)"
  fi
}

# assert_stdout_empty <description>
assert_stdout_empty() {
  local description="$1"
  if [[ -n "$script_stdout" ]]; then
    fail "$description (stdout was not empty: '$script_stdout')"
  fi
}

# assert_stderr_nonempty <description>
assert_stderr_nonempty() {
  local description="$1"
  if [[ -z "$script_stderr" ]]; then
    fail "$description (stderr was empty)"
  fi
}

# assert_has_line <line> <description>
assert_has_line() {
  local line="$1" description="$2"
  if ! grep -qxF "$line" <<< "$script_stdout"; then
    fail "$description (no line equal to '$line')"
  fi
}

# assert_lacks_line <line> <description>
assert_lacks_line() {
  local line="$1" description="$2"
  if grep -qxF "$line" <<< "$script_stdout"; then
    fail "$description (found a line equal to '$line')"
  fi
}

# assert_sorted_and_deduplicated <description>
assert_sorted_and_deduplicated() {
  local description="$1" sorted duplicates
  sorted=$(LC_ALL=C sort <<< "$script_stdout")
  if [[ "$script_stdout" != "$sorted" ]]; then
    fail "$description (output is not sorted under LC_ALL=C)"
  fi
  duplicates=$(LC_ALL=C sort <<< "$script_stdout" | uniq -d)
  if [[ -n "$duplicates" ]]; then
    fail "$description (duplicate line(s): $duplicates)"
  fi
}

# ── 2. Fixtures ─────────────────────────────────────────────────────────────
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

# make_fixture_repo: creates a git repository with a root go.mod, commits it
# and prints the repository's path.
make_fixture_repo() {
  local repo
  repo=$(mktemp -d "$TMP_ROOT/repo.XXXXXX")
  git -C "$repo" init -q -b main
  printf 'module example\n\ngo 1.26\n' > "$repo/go.mod"
  fixture_commit "$repo" "base"
  echo "$repo"
}

# add_module <repo> <dir>: adds a go.mod file in <dir> and commits it.
add_module() {
  local repo="$1" dir="$2"
  mkdir -p "$repo/$dir"
  printf 'module example/%s\n\ngo 1.26\n' "$dir" > "$repo/$dir/go.mod"
  fixture_commit "$repo" "add $dir"
}

# run_script <dir> [locale]: runs the script under test with cwd <dir>,
# optionally under the given LC_ALL, capturing stdout, stderr and exit status.
run_script() {
  local dir="$1" locale="${2:-C}" stdout_file stderr_file
  stdout_file=$(mktemp "$TMP_ROOT/stdout.XXXXXX")
  stderr_file=$(mktemp "$TMP_ROOT/stderr.XXXXXX")
  if (cd "$dir" && LC_ALL="$locale" "$SCRIPT_UNDER_TEST") \
      >"$stdout_file" 2>"$stderr_file"; then
    script_status=0
  else
    script_status=$?
  fi
  script_stdout=$(<"$stdout_file")
  script_stderr=$(<"$stderr_file")
  rm -f "$stdout_file" "$stderr_file"
}

# ── 3. Tests ────────────────────────────────────────────────────────────────
test_nested_modules_are_enumerated_including_root() {
  local repo
  repo=$(make_fixture_repo)
  add_module "$repo" "pkg/a"
  add_module "$repo" "pkg/b/sub"
  run_script "$repo"
  assert_status 0 "enumerating a fixture with nested modules must succeed"
  assert_stdout_equals "$(printf '.\npkg/a\npkg/b/sub')" \
    "every module must be listed, with the root rendered as '.'"
}

# A module the fixture gains after the last edit to the enumeration itself
# must appear with no other change: the script derives the list from Git
# rather than carrying its own inventory.
test_a_module_added_later_is_picked_up_with_no_other_edit() {
  local repo
  repo=$(make_fixture_repo)
  run_script "$repo"
  assert_stdout_equals "." "a fresh fixture must only report the root module"

  add_module "$repo" "pkg/new"
  run_script "$repo"
  assert_status 0 "enumerating after a module is added must succeed"
  assert_has_line "pkg/new" "a newly added module must be picked up"
  assert_has_line "." "the root module must still be reported"
}

test_untracked_and_gitignored_go_mod_are_not_listed() {
  local repo
  repo=$(make_fixture_repo)
  printf 'ignored/\n' > "$repo/.gitignore"
  mkdir -p "$repo/ignored"
  printf 'module example/ignored\n\ngo 1.26\n' > "$repo/ignored/go.mod"
  fixture_commit "$repo" "ignore ignored/"

  mkdir -p "$repo/untracked"
  printf 'module example/untracked\n\ngo 1.26\n' > "$repo/untracked/go.mod"

  run_script "$repo"
  assert_status 0 "enumerating with untracked and ignored go.mod files must succeed"
  assert_lacks_line "ignored" "a gitignored go.mod must not be listed"
  assert_lacks_line "untracked" "an untracked go.mod must not be listed"
}

test_vendor_and_testdata_are_pruned_as_exact_path_components() {
  local repo
  repo=$(make_fixture_repo)
  add_module "$repo" "vendor"
  add_module "$repo" "testdata"
  add_module "$repo" "pkg/vendor/nested"
  add_module "$repo" "pkg/testdata/nested"
  add_module "$repo" "vendor-tools"
  add_module "$repo" "testdata2"

  run_script "$repo"
  assert_status 0 "enumerating with vendor and testdata modules must succeed"
  assert_lacks_line "vendor" "a top-level vendor module must be pruned"
  assert_lacks_line "testdata" "a top-level testdata module must be pruned"
  assert_lacks_line "pkg/vendor/nested" "a nested vendor component must be pruned"
  assert_lacks_line "pkg/testdata/nested" "a nested testdata component must be pruned"
  assert_has_line "vendor-tools" "a directory merely prefixed 'vendor' must survive"
  assert_has_line "testdata2" "a directory merely prefixed 'testdata' must survive"
}

test_output_is_sorted_deduplicated_and_locale_independent() {
  local repo stdout_under_c
  repo=$(make_fixture_repo)
  add_module "$repo" "pkg/zeta"
  add_module "$repo" "pkg/alpha"
  add_module "$repo" "pkg/a/b"

  run_script "$repo" "C"
  assert_status 0 "enumerating must succeed under LC_ALL=C"
  assert_sorted_and_deduplicated "the listing must be sorted and duplicate-free"
  stdout_under_c="$script_stdout"

  run_script "$repo" "C.UTF-8"
  assert_status 0 "enumerating must succeed under a different locale"
  if [[ "$script_stdout" != "$stdout_under_c" ]]; then
    fail "the listing must not depend on the caller's locale (got '$script_stdout' vs '$stdout_under_c')"
  fi
}

test_invoked_from_a_subdirectory() {
  local repo
  repo=$(make_fixture_repo)
  add_module "$repo" "pkg/a"
  add_module "$repo" "pkg/b/sub"

  run_script "$repo/pkg/b/sub"
  assert_status 0 "enumerating from a subdirectory must succeed"
  assert_stdout_equals "$(printf '.\npkg/a\npkg/b/sub')" \
    "the listing from a subdirectory must match the listing from the root"
}

test_outside_a_git_repository_fails_cleanly() {
  local dir
  dir=$(mktemp -d "$TMP_ROOT/notgit.XXXXXX")
  run_script "$dir"
  assert_status_nonzero "running outside a git repository must fail"
  assert_stdout_empty "a failed run must not print a partial list"
  assert_stderr_nonempty "a failed run must explain itself on stderr"
}

# A corrupted index makes `git ls-files` itself fail; the script must not
# mistake that failure for "no modules" and report an empty list with a
# zero exit status.
test_a_corrupted_index_aborts_instead_of_reporting_an_empty_list() {
  local repo
  repo=$(make_fixture_repo)
  add_module "$repo" "pkg/a"
  head -c 64 /dev/urandom > "$repo/.git/index"

  run_script "$repo"
  assert_status_nonzero \
    "a corrupted index must abort rather than report an empty list"
  assert_stdout_empty "an aborted run must not print a partial list"
}

test_real_repo_smoke() {
  run_script "$REPO_ROOT"
  assert_status 0 "enumerating the real repository must succeed"
  if [[ -z "$script_stdout" ]]; then
    fail "the real repository must report at least one module"
  fi
  assert_has_line "." "the real repository's root module must be reported"
  assert_has_line "pkg/auth" "a known nested module must be reported"
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
  echo "FAIL $test_name (script exit status $script_status), script stderr:"
  sed 's/^/  | /' <<< "$script_stderr"
}

main() {
  run_test test_nested_modules_are_enumerated_including_root
  run_test test_a_module_added_later_is_picked_up_with_no_other_edit
  run_test test_untracked_and_gitignored_go_mod_are_not_listed
  run_test test_vendor_and_testdata_are_pruned_as_exact_path_components
  run_test test_output_is_sorted_deduplicated_and_locale_independent
  run_test test_invoked_from_a_subdirectory
  run_test test_outside_a_git_repository_fails_cleanly
  run_test test_a_corrupted_index_aborts_instead_of_reporting_an_empty_list
  run_test test_real_repo_smoke

  echo ""
  if [[ "$tests_failed" -gt 0 ]]; then
    echo "$tests_failed of $tests_run test(s) failed."
    exit 1
  fi
  echo "$tests_run test(s) passed."
}

main "$@"
