#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/.plans/mutation-report"
mkdir -p "$REPORT_DIR"

UNIT_PROFILE="$REPORT_DIR/coverage-unit.out"
WORKFLOW_PROFILE="$REPORT_DIR/coverage-workflow.out"
DIFF_OUTPUT="$REPORT_DIR/workflow-only-coverage.txt"

echo "Generating unit test coverage profile..."
go test -tags=unit -coverprofile="$UNIT_PROFILE" ./... > /dev/null 2>&1 \
  || echo "WARNING: unit tests had failures; coverage profile may be incomplete"

echo "Generating workflow test coverage profile..."
go test -tags=unit -coverprofile="$WORKFLOW_PROFILE" \
  ./internal/workflow_tests/... > /dev/null 2>&1 \
  || echo "WARNING: workflow tests had failures; coverage profile may be incomplete"

echo "Computing diff (lines only in workflow coverage)..."

python3 - "$UNIT_PROFILE" "$WORKFLOW_PROFILE" > "$DIFF_OUTPUT" <<'PYEOF'
import sys

def parse_coverage(filepath):
    """Returns a set of (file, startline) tuples that are covered (count > 0)."""
    covered = set()
    try:
        with open(filepath) as f:
            for line in f:
                line = line.strip()
                if line.startswith('mode:') or not line:
                    continue
                # format: pkg/file.go:10.5,15.3 2 1
                parts = line.rsplit(' ', 2)
                if len(parts) != 3:
                    continue
                loc, _, count = parts
                if int(count) > 0:
                    file_part, range_part = loc.rsplit(':', 1)
                    start = range_part.split(',')[0].split('.')[0]
                    covered.add((file_part, start))
    except FileNotFoundError:
        pass
    return covered

unit = parse_coverage(sys.argv[1])
workflow = parse_coverage(sys.argv[2])

workflow_only = workflow - unit

if not workflow_only:
    print("No lines found that are exclusively covered by workflow tests.")
else:
    print(f"{len(workflow_only)} statement(s) only covered by workflow tests:\n")
    by_file = {}
    for (f, line) in sorted(workflow_only):
        by_file.setdefault(f, []).append(line)
    for f, lines in sorted(by_file.items()):
        print(f"  {f}: lines {', '.join(sorted(lines, key=int))}")
PYEOF

cat "$DIFF_OUTPUT"
echo ""
echo "Diff written to $DIFF_OUTPUT"
