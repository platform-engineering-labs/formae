#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/.mutation-report"
mkdir -p "$REPORT_DIR"

# Run gremlins on a single package and save JSON output.
# Usage: run_package <pkg> [coverpkg]
# When coverpkg is provided, gremlins runs in integration mode with cross-package
# coverage tracking. This is needed for packages whose tests live in a parent
# package (e.g. internal/datastore/sqlite is tested by internal/datastore/).
run_package() {
  local pkg="$1"
  local coverpkg="${2:-}"
  local safe_name="${pkg//\//_}"
  local output_file="$REPORT_DIR/${safe_name}.json"

  local -a extra_flags=()
  if [[ -n "$coverpkg" ]]; then
    extra_flags+=(--integration --coverpkg "$coverpkg")
  fi

  local label="$pkg"
  [[ -n "$coverpkg" ]] && label="$pkg (integration, coverpkg=$coverpkg)"
  echo "Running: $label"
  gremlins unleash \
    --tags unit \
    --timeout-coefficient 10 \
    --workers 4 \
    "${extra_flags[@]}" \
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

# Packages whose tests live in a parent package. Each entry is "pkg:coverpkg".
# These are run with --integration + --coverpkg so gremlins uses cross-package
# coverage to find the parent's tests that exercise the implementation code.
CROSS_PKG_TESTED=(
  "internal/datastore/sqlite:./internal/datastore/..."
  "internal/datastore/postgres:./internal/datastore/..."
  "internal/datastore/aurora:./internal/datastore/..."
)

# Find all packages with no test files at all
find_untested_packages() {
  local all_pkgs
  all_pkgs=$(go list ./internal/... ./cmd/... \
    | sed "s|github.com/platform-engineering-labs/formae/||")
  local tested_pkgs
  tested_pkgs=$(find "$REPO_ROOT" -name "*_test.go" | xargs -I{} dirname {} | sort -u | sed "s|$REPO_ROOT/||")

  # Also exclude cross-package-tested packages (tested via parent)
  local cross_pkgs
  cross_pkgs=$(printf '%s\n' "${CROSS_PKG_TESTED[@]}" | cut -d: -f1 | sort)

  comm -23 <(echo "$all_pkgs" | sort) \
    <(sort -u <(echo "$tested_pkgs") <(echo "$cross_pkgs"))
}

# Usage: ./scripts/mutation-test.sh [package-path]
if [[ $# -eq 1 ]]; then
  run_package "$1"
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

# Run cross-package-tested packages (tests live in parent package)
echo ""
echo "=== Running cross-package-tested packages ==="
for entry in "${CROSS_PKG_TESTED[@]}"; do
  pkg="${entry%%:*}"
  coverpkg="${entry#*:}"
  echo "[cross-pkg] $pkg"
  run_package "$pkg" "$coverpkg"
done

echo ""
echo "Done. Results in $REPORT_DIR"
