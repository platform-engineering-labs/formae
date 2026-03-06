#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/.mutation-report"
mkdir -p "$REPORT_DIR"

# Track packages where tests fail (gremlins can't gather coverage)
FAILED_PACKAGES=()

# Check whether a package belongs to the root Go module.
# Packages with their own go.mod (like pkg/plugin) are separate modules
# and cannot be tested with `go test ./<path>` from the root.
is_root_module_package() {
  local pkg="$1"
  local dir="$pkg"
  # Walk up from the package dir looking for a go.mod that isn't the root one
  while [[ "$dir" != "." ]]; do
    if [[ -f "$REPO_ROOT/$dir/go.mod" && "$dir" != "." ]]; then
      return 1  # has its own go.mod, not a root module package
    fi
    dir=$(dirname "$dir")
  done
  return 0
}

# Run gremlins on a single package and save JSON output.
run_package() {
  local pkg="$1"
  local safe_name="${pkg//\//_}"
  local output_file="$REPORT_DIR/${safe_name}.json"

  echo "Running: $pkg"

  # Pre-flight: verify tests pass before running mutation testing.
  # This catches real test failures early instead of burying them
  # in gremlins' "failed to gather coverage" output.
  # Only run pre-flight for root module packages — packages with their own
  # go.mod (like pkg/plugin) can't be tested from the root and gremlins
  # handles them correctly on its own.
  if is_root_module_package "$pkg"; then
    local preflight_log="$REPORT_DIR/${safe_name}.preflight.log"
    if ! go test -tags unit -count=1 -failfast "./$pkg" > "$preflight_log" 2>&1; then
      echo "  -> TESTS FAILED (skipping mutation testing)"
      cat "$preflight_log"
      FAILED_PACKAGES+=("$pkg")
      return
    fi
  fi

  gremlins unleash \
    --tags unit \
    --timeout-coefficient 10 \
    --workers 4 \
    -o "$output_file" \
    "./$pkg" 2>&1 || true

  echo "$pkg" > "$REPORT_DIR/${safe_name}.pkg"
  echo "  -> saved"
}

# Find all packages that have at least one *_test.go file with //go:build unit
find_unit_test_packages() {
  grep -rl "//go:build unit" --include="*_test.go" "$REPO_ROOT" \
    | xargs -I{} dirname {} \
    | sort -u \
    | sed "s|$REPO_ROOT/||"
}

# Find all packages with no test files at all
find_untested_packages() {
  local all_pkgs
  all_pkgs=$(go list ./internal/... ./cmd/... \
    | sed "s|github.com/platform-engineering-labs/formae/||")
  local tested_pkgs
  tested_pkgs=$(find "$REPO_ROOT" -name "*_test.go" | xargs -I{} dirname {} | sort -u | sed "s|$REPO_ROOT/||")

  comm -23 <(echo "$all_pkgs" | sort) <(echo "$tested_pkgs" | sort)
}

# Usage: ./scripts/mutation-test.sh [package-path]
if [[ $# -eq 1 ]]; then
  run_package "$1"
  if [[ ${#FAILED_PACKAGES[@]} -gt 0 ]]; then
    echo "ERROR: tests failed for $1"
    exit 1
  fi
  echo "Done. Result in $REPORT_DIR"
  exit 0
fi

# Full run
echo "=== Mutation Testing ==="
echo "Report dir: $REPORT_DIR"
echo ""

# Save list of untested packages
echo "=== Finding untested packages ==="
find_untested_packages > "$REPORT_DIR/untested-packages.txt"
echo "$(wc -l < "$REPORT_DIR/untested-packages.txt") packages have no tests"

# Run gremlins per package
echo ""
echo "=== Running gremlins per package ==="
packages=$(find_unit_test_packages)
total=$(echo "$packages" | wc -l)
count=0

while IFS= read -r pkg; do
  count=$((count + 1))
  echo "[$count/$total] $pkg"
  run_package "$pkg"
done <<< "$packages"

echo ""

# Report and fail on test failures
if [[ ${#FAILED_PACKAGES[@]} -gt 0 ]]; then
  echo "=== FAILED PACKAGES ==="
  for pkg in "${FAILED_PACKAGES[@]}"; do
    echo "  - $pkg"
  done
  echo ""
  echo "ERROR: ${#FAILED_PACKAGES[@]} package(s) had failing tests."
  echo "Fix the test failures above before mutation testing can cover these packages."
  exit 1
fi

echo "Done. Results in $REPORT_DIR"
