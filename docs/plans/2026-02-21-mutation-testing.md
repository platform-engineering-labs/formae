# Mutation Testing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a diagnostic toolchain that runs mutation testing across the entire formae codebase and generates a ranked report of test quality gaps per package.

**Architecture:** gremlins is run per-package from the repo root using `--timeout-coefficient 10` (required due to compilation overhead). A shell script iterates all packages with unit-tagged tests, collects JSON results, and generates a summary. Two coverage profiles (unit tests vs workflow tests) are diffed to flag lines reachable only via workflow tests — these would show artificially low mutation scores when tested in isolation.

**Tech Stack:** gremlins (Go mutation testing), bash, jq (JSON processing), standard Go coverage tooling (`go test -coverprofile`)

---

## Prerequisites

- gremlins installed: `go install github.com/go-gremlins/gremlins/cmd/gremlins@latest`
- jq installed: `which jq` (standard on most Linux/macOS)
- Run from the worktree root: `/home/jeroen/dev/pel/formae/.worktrees/mutation-testing`
- Plugins must be built first: `make build`

### Known behaviour

- gremlins must be run from the repo root (directory containing `go.mod`)
- `--timeout-coefficient 10` is required; default timeout causes all mutants to time out due to compilation overhead
- `TestDatastore_DeleteStack_ThenRecreate` is a known flaky test in `internal/metastructure/datastore` — results for that package may be slightly unreliable
- gremlins handles its own coverage filtering internally — it only mutates covered code

---

## Task 1: Add gremlins install target to Makefile

**Files:**
- Modify: `Makefile`

**Step 1: Add the install-gremlins target**

Add after the `build-tools` target in `Makefile`:

```makefile
## install-gremlins: Install the gremlins mutation testing tool
install-gremlins:
	go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
```

Also add `install-gremlins` to the `.PHONY` list at the bottom of the Makefile.

**Step 2: Verify it works**

```bash
make install-gremlins
gremlins --version
```

Expected: gremlins version printed without errors.

**Step 3: Commit**

```bash
git add Makefile
git commit -m "chore: add install-gremlins Makefile target"
```

---

## Task 2: Write the mutation test script (single-package proof of concept)

**Files:**
- Create: `scripts/mutation-test.sh`

**Step 1: Create the script with single-package support**

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/docs/plans/mutation-report"
mkdir -p "$REPORT_DIR"

# Run gremlins on a single package and save JSON output
run_package() {
  local pkg="$1"
  local safe_name="${pkg//\//_}"
  local output_file="$REPORT_DIR/${safe_name}.json"

  echo "Running: $pkg"
  gremlins unleash \
    --tags unit \
    --timeout-coefficient 10 \
    --workers 4 \
    -o "$output_file" \
    "./$pkg" 2>&1 || true  # don't fail on threshold errors

  echo "  -> $output_file"
}

# Usage: ./scripts/mutation-test.sh [package-path]
if [[ $# -eq 1 ]]; then
  run_package "$1"
  echo "Done. Results in $REPORT_DIR"
  exit 0
fi

echo "Usage: $0 <package-path>"
echo "Example: $0 internal/metastructure/patch"
exit 1
```

Make it executable:

```bash
chmod +x scripts/mutation-test.sh
```

**Step 2: Run on the patch package to verify**

```bash
./scripts/mutation-test.sh internal/metastructure/patch
```

Expected output (last lines):
```
Running: internal/metastructure/patch
  -> docs/plans/mutation-report/internal_metastructure_patch.json
Done. Results in .../docs/plans/mutation-report
```

**Step 3: Verify the JSON file exists and has content**

```bash
cat docs/plans/mutation-report/internal_metastructure_patch.json | jq '.files | length'
```

Expected: a number > 0.

**Step 4: Commit**

```bash
git add scripts/mutation-test.sh
git commit -m "chore: add mutation-test.sh script (single-package mode)"
```

---

## Task 3: Expand the script to run all packages

**Files:**
- Modify: `scripts/mutation-test.sh`

**Step 1: Replace the script with the full version**

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/docs/plans/mutation-report"
mkdir -p "$REPORT_DIR"

# Run gremlins on a single package and save JSON output
run_package() {
  local pkg="$1"
  local safe_name="${pkg//\//_}"
  local output_file="$REPORT_DIR/${safe_name}.json"

  echo "Running: $pkg"
  gremlins unleash \
    --tags unit \
    --timeout-coefficient 10 \
    --workers 4 \
    -o "$output_file" \
    "./$pkg" 2>&1 || true

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
  all_pkgs=$(go list ./... | sed "s|github.com/platform-engineering-labs/formae/||")
  local tested_pkgs
  tested_pkgs=$(find "$REPO_ROOT" -name "*_test.go" | xargs -I{} dirname {} | sort -u | sed "s|$REPO_ROOT/||")

  comm -23 <(echo "$all_pkgs" | sort) <(echo "$tested_pkgs" | sort)
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

echo ""
echo "Done. Results in $REPORT_DIR"
```

**Step 2: Dry-run the package discovery to verify it finds the right packages**

```bash
grep -rl "//go:build unit" --include="*_test.go" . \
  | xargs -I{} dirname {} \
  | sort -u \
  | sed "s|$(pwd)/||"
```

Expected: a list of ~20 packages matching what we saw earlier.

**Step 3: Commit**

```bash
git add scripts/mutation-test.sh
git commit -m "chore: expand mutation-test.sh to run all unit-tested packages"
```

---

## Task 4: Add coverage profile diff for workflow-test-only analysis

**Files:**
- Modify: `scripts/mutation-test.sh`
- Create: `scripts/coverage-diff.sh`

**Step 1: Create coverage-diff.sh**

This script generates two coverage profiles and identifies lines covered only by workflow tests.

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/docs/plans/mutation-report"
mkdir -p "$REPORT_DIR"

UNIT_PROFILE="$REPORT_DIR/coverage-unit.out"
WORKFLOW_PROFILE="$REPORT_DIR/coverage-workflow.out"
DIFF_OUTPUT="$REPORT_DIR/workflow-only-coverage.txt"

echo "Generating unit test coverage profile..."
go test -tags=unit -coverprofile="$UNIT_PROFILE" ./... > /dev/null 2>&1 || true

echo "Generating workflow test coverage profile..."
go test -tags=unit -coverprofile="$WORKFLOW_PROFILE" \
  ./internal/workflow_tests/... > /dev/null 2>&1 || true

echo "Computing diff (lines only in workflow coverage)..."

# Coverage profile lines format: file:startline.startcol,endline.endcol numstmts count
# A line is "covered" if count > 0
# We want lines with count > 0 in workflow but count == 0 in unit

python3 - <<'PYEOF'
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

echo ""
echo "Diff complete."
PYEOF

chmod +x scripts/coverage-diff.sh
```

**Step 2: Run the diff to verify it works**

```bash
./scripts/coverage-diff.sh
```

Expected: output listing files/lines covered only by workflow tests, or "No lines found..." if unit tests are comprehensive.

**Step 3: Commit**

```bash
git add scripts/coverage-diff.sh
git commit -m "chore: add coverage-diff.sh to identify workflow-test-only coverage"
```

---

## Task 5: Write the report generator

**Files:**
- Create: `scripts/generate-mutation-report.sh`

**Step 1: Create the report generator**

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/docs/plans/mutation-report"
SUMMARY="$REPORT_DIR/summary.md"

echo "Generating mutation test report..."

python3 - "$REPORT_DIR" <<'PYEOF'
import json
import os
import sys
from pathlib import Path

report_dir = Path(sys.argv[1])
results = []

for json_file in sorted(report_dir.glob("*.json")):
    # Convert filename back to package path
    pkg = json_file.stem.replace("_", "/", )
    # Heuristic: find the actual package by looking for internal/ or pkg/ prefix
    for prefix in ["internal/", "pkg/", "plugins/"]:
        idx = pkg.find(prefix.replace("/", "_"))
        if idx != -1:
            pkg = pkg[idx:].replace("_", "/")
            # Re-introduce underscores where package names have them
            break

    with open(json_file) as f:
        try:
            data = json.load(f)
        except json.JSONDecodeError:
            continue

    killed = 0
    lived = 0
    timed_out = 0
    not_covered = 0

    for file_entry in data.get("files", []):
        for m in file_entry.get("mutations", []):
            status = m.get("status", "")
            if status == "KILLED":
                killed += 1
            elif status == "LIVED":
                lived += 1
            elif status == "TIMED OUT":
                timed_out += 1
            elif status == "NOT COVERED":
                not_covered += 1

    total_tested = killed + lived
    score = (killed / total_tested * 100) if total_tested > 0 else None

    results.append({
        "package": pkg,
        "score": score,
        "killed": killed,
        "lived": lived,
        "timed_out": timed_out,
        "not_covered": not_covered,
        "total": total_tested,
    })

# Sort by score ascending (worst first), None scores at top
results.sort(key=lambda r: (r["score"] is not None, r["score"] or 0))

# Load untested packages
untested_file = report_dir / "untested-packages.txt"
untested = []
if untested_file.exists():
    untested = [l.strip() for l in untested_file.read_text().splitlines() if l.strip()]

# Load workflow-only diff
diff_file = report_dir / "workflow-only-coverage.txt"
diff_content = ""
if diff_file.exists():
    diff_content = diff_file.read_text().strip()

# Write summary
lines = []
lines.append("# Mutation Testing Report\n")
lines.append(f"Generated: {os.popen('date').read().strip()}\n\n")

if untested:
    lines.append("## Packages with no tests\n\n")
    for p in sorted(untested):
        lines.append(f"- `{p}`\n")
    lines.append("\n")

lines.append("## Mutation scores (worst first)\n\n")
lines.append("| Package | Score | Killed | Lived | Timed Out | Status |\n")
lines.append("|---------|-------|--------|-------|-----------|--------|\n")

for r in results:
    score_str = f"{r['score']:.1f}%" if r["score"] is not None else "n/a"
    if r["score"] is None:
        status = "⚠️ no mutants"
    elif r["score"] >= 80:
        status = "✅"
    elif r["score"] >= 60:
        status = "⚠️"
    else:
        status = "❌"
    timeout_str = f" (+{r['timed_out']} timeout)" if r["timed_out"] else ""
    lines.append(
        f"| `{r['package']}` | {score_str} | {r['killed']} | {r['lived']} | "
        f"{r['timed_out']} | {status} |\n"
    )

lines.append("\n")
lines.append("## Workflow-test-only coverage\n\n")
if diff_content:
    lines.append("Lines only reachable via workflow tests (not covered by package unit tests):\n\n")
    lines.append("```\n")
    lines.append(diff_content + "\n")
    lines.append("```\n")
else:
    lines.append("_Coverage diff not available. Run `./scripts/coverage-diff.sh` first._\n")

summary_path = report_dir / "summary.md"
summary_path.write_text("".join(lines))
print(f"Report written to {summary_path}")
PYEOF
```

Make it executable:
```bash
chmod +x scripts/generate-mutation-report.sh
```

**Step 2: Test on existing results from Task 2**

```bash
./scripts/generate-mutation-report.sh
cat docs/plans/mutation-report/summary.md
```

Expected: a markdown table with at least the `patch` package results.

**Step 3: Commit**

```bash
git add scripts/generate-mutation-report.sh
git commit -m "chore: add generate-mutation-report.sh report generator"
```

---

## Task 6: Add make mutation-test target

**Files:**
- Modify: `Makefile`

**Step 1: Add the target**

Add after `test-property:` target:

```makefile
## mutation-test: Run mutation testing across all unit-tested packages and generate report
mutation-test: build
	@echo "Running mutation testing (this will take a while)..."
	./scripts/mutation-test.sh
	./scripts/coverage-diff.sh
	./scripts/generate-mutation-report.sh
	@echo "Report: docs/plans/mutation-report/summary.md"
```

Add `mutation-test` to the `.PHONY` list.

**Step 2: Verify the target is listed**

```bash
make help 2>/dev/null | grep mutation || grep mutation-test Makefile
```

**Step 3: Commit**

```bash
git add Makefile
git commit -m "chore: add make mutation-test target"
```

---

## Task 7: Run the full diagnostic

**Step 1: Run the full mutation test suite**

This will take a while (expect 20-60 minutes depending on the number of packages).

```bash
make mutation-test
```

Monitor progress — results accumulate in `docs/plans/mutation-report/` as each package completes. You can inspect partial results at any time.

**Step 2: Check for timed-out mutants in the results**

```bash
grep -l "TIMED OUT" docs/plans/mutation-report/*.json | head -10
```

If any packages have a high number of timed-out mutants, re-run just that package with a higher coefficient:

```bash
./scripts/mutation-test.sh <package-path>
# If still timing out, edit the script to use --timeout-coefficient 20 for that package
```

**Step 3: Generate the final report**

```bash
./scripts/generate-mutation-report.sh
```

**Step 4: Review the summary**

```bash
cat docs/plans/mutation-report/summary.md
```

---

## Task 8: Commit the report and discuss next steps

**Step 1: Add report files to git**

```bash
git add docs/plans/mutation-report/summary.md
git add docs/plans/mutation-report/untested-packages.txt
git add docs/plans/mutation-report/workflow-only-coverage.txt
# Do NOT commit the .json files — they are large and regeneratable
echo "docs/plans/mutation-report/*.json" >> .gitignore
echo "docs/plans/mutation-report/coverage-*.out" >> .gitignore
git add .gitignore
git commit -m "chore: add mutation testing report and update .gitignore"
```

**Step 2: Review the report together**

With the report in hand, categorize packages into:

1. **Good (80%+)** — no action needed
2. **Low score, unit-tested** — write targeted tests to kill surviving mutants
3. **Low score or workflow-test-only** — decide case by case whether to add unit tests or accept workflow coverage

Remediation work is a separate plan.
