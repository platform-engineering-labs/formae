#!/usr/bin/env bash
# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2
#
set -euo pipefail

# Tests for the version-semver make target.
#
# Each test copies the Makefile into a throwaway git repository fixture, tags it
# as the fixture needs, runs the target there and asserts on the file it wrote
# and on the exit status. No network, no Go toolchain and no build are involved:
# the point of the target is that a job needing the stamp does not need a build.

TESTS_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$TESTS_DIR/../.." && pwd)
MAKEFILE_UNDER_TEST="$REPO_ROOT/Makefile"

TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT

tests_run=0
tests_failed=0
current_test_failed=0
make_output=""
make_status=0

# ── 1. Assertions ───────────────────────────────────────────────────────────
fail() {
  local message="$1"
  current_test_failed=1
  echo "  $message"
}

# assert_stamp <repo> <expected-contents> <description>
assert_stamp() {
  local repo="$1" expected="$2" description="$3" actual
  if [[ ! -f "$repo/version.semver" ]]; then
    fail "$description (no version.semver was written)"
    return
  fi
  actual=$(< "$repo/version.semver")
  if [[ "$actual" != "$expected" ]]; then
    fail "$description (want '$expected', got '$actual')"
  fi
}

# assert_no_stamp <repo> <description>
assert_no_stamp() {
  local repo="$1" description="$2"
  if [[ -f "$repo/version.semver" ]]; then
    fail "$description (version.semver was written: '$(< "$repo/version.semver")')"
  fi
}

# assert_status <expected-status> <description>
assert_status() {
  local expected="$1" description="$2"
  if [[ "$make_status" != "$expected" ]]; then
    fail "$description (want exit status $expected, got $make_status)"
  fi
}

# assert_status_nonzero <description>
assert_status_nonzero() {
  local description="$1"
  if [[ "$make_status" == "0" ]]; then
    fail "$description (want a non-zero exit status, got 0)"
  fi
}

# assert_output_matches <extended-regex> <description>
assert_output_matches() {
  local pattern="$1" description="$2"
  if ! grep -qE "$pattern" <<< "$make_output"; then
    fail "$description (no line matching /$pattern/)"
  fi
}

# ── 2. Fixtures ─────────────────────────────────────────────────────────────
# make_fixture_repo [tag]: creates a git repository holding the Makefile under
# test, tagged with <tag> when one is given, and prints its path.
make_fixture_repo() {
  local tag="${1:-}" repo
  repo=$(mktemp -d "$TMP_ROOT/repo.XXXXXX")
  cp "$MAKEFILE_UNDER_TEST" "$repo/Makefile"
  git -C "$repo" init -q -b main
  git -C "$repo" add -A
  git -C "$repo" \
    -c user.name=fixture -c user.email=fixture@example.invalid \
    -c commit.gpgsign=false \
    commit -q -m "base"
  if [[ -n "$tag" ]]; then
    git -C "$repo" tag "$tag"
  fi
  echo "$repo"
}

# run_target <repo>: runs the target in the fixture repo, capturing output and
# exit status.
run_target() {
  local repo="$1"
  make_output=""
  make_status=0
  make_output=$(make -C "$repo" version-semver 2>&1) || make_status=$?
}

# ── 3. Tests ────────────────────────────────────────────────────────────────
# The stamp is the PKL package version, which has to be a bare semver: a
# pre-release tag carries a channel suffix the schema project cannot use.
test_a_pre_release_tag_is_stamped_without_its_channel() {
  local repo
  repo=$(make_fixture_repo "0.90.0-dev.3")
  run_target "$repo"
  assert_status 0 "stamping a pre-release version must succeed"
  assert_stamp "$repo" "0.90.0" "the channel suffix must not reach the stamp"
}

test_a_stable_tag_is_stamped_verbatim() {
  local repo
  repo=$(make_fixture_repo "0.90.0")
  run_target "$repo"
  assert_status 0 "stamping a stable version must succeed"
  assert_stamp "$repo" "0.90.0" "a stable tag is already the stamp"
}

# A checkout without tags cannot derive a version. Writing the empty string
# would leave the schema project resolvable-looking but versionless, so the
# failure has to be the target's, where it names itself, rather than a Pkl
# error several steps later.
test_an_underivable_version_fails_instead_of_stamping_an_empty_file() {
  local repo
  repo=$(make_fixture_repo)
  run_target "$repo"
  assert_status_nonzero "a version that cannot be derived must fail the target"
  assert_no_stamp "$repo" "an underivable version must not be stamped"
  assert_output_matches "cannot derive a version" \
    "the failure must say what it could not derive"
}

test_a_stale_stamp_is_replaced() {
  local repo
  repo=$(make_fixture_repo "0.90.0")
  echo "0.1.0" > "$repo/version.semver"
  run_target "$repo"
  assert_status 0 "restamping must succeed"
  assert_stamp "$repo" "0.90.0" "the stamp must carry the current version"
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
  echo "FAIL $test_name (make exit status $make_status), make output:"
  sed 's/^/  | /' <<< "$make_output"
}

main() {
  run_test test_a_pre_release_tag_is_stamped_without_its_channel
  run_test test_a_stable_tag_is_stamped_verbatim
  run_test test_an_underivable_version_fails_instead_of_stamping_an_empty_file
  run_test test_a_stale_stamp_is_replaced

  echo ""
  if [[ "$tests_failed" -gt 0 ]]; then
    echo "$tests_failed of $tests_run test(s) failed."
    exit 1
  fi
  echo "$tests_run test(s) passed."
}

main "$@"
