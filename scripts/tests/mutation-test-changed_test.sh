#!/usr/bin/env bash
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

# stub_gremlins <bin_dir> <body>: installs an executable `gremlins` in
# <bin_dir> that runs <body> instead of the real tool. The body sees gremlins'
# own arguments plus $report_path, the value passed to gremlins' -o flag.
stub_gremlins() {
  local bin_dir="$1" body="$2"
  mkdir -p "$bin_dir"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'report_path=""\n'
    printf 'while [[ $# -gt 0 ]]; do\n'
    printf '  case "$1" in\n'
    printf '    -o) report_path="$2"; shift 2 ;;\n'
    printf '    *) shift ;;\n'
    printf '  esac\n'
    printf 'done\n'
    printf '%s\n' "$body"
  } > "$bin_dir/gremlins"
  chmod +x "$bin_dir/gremlins"
}

# run_script <repo> <bin_dir>: runs the script under test inside the fixture
# repo with the stub first on PATH, capturing output and exit status.
run_script() {
  local repo="$1" bin_dir="$2"
  script_output=""
  script_status=0
  script_output=$(cd "$repo" && env -u GITHUB_STEP_SUMMARY \
    GITHUB_BASE_REF=main PATH="$bin_dir:$PATH" \
    bash "$SCRIPT_UNDER_TEST" 2>&1) || script_status=$?
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

  echo ""
  if [[ "$tests_failed" -gt 0 ]]; then
    echo "$tests_failed of $tests_run test(s) failed."
    exit 1
  fi
  echo "$tests_run test(s) passed."
}

main "$@"
