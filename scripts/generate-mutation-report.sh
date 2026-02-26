#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
REPORT_DIR="$REPO_ROOT/.plans/mutation-report"

echo "Generating mutation test report..."

python3 - "$REPORT_DIR" <<'PYEOF'
import json
from datetime import datetime
import sys
from pathlib import Path

report_dir = Path(sys.argv[1])
results = []

for json_file in sorted(report_dir.glob("*.json")):
    # Read package name from sidecar .pkg file
    pkg_file = json_file.with_suffix(".pkg")
    if pkg_file.exists():
        pkg = pkg_file.read_text().strip()
    else:
        # Fallback: use filename stem (may be inaccurate for packages with underscores)
        pkg = json_file.stem

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
lines.append(f"Generated: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n")

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
