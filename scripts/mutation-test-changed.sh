#!/usr/bin/env bash
set -euo pipefail

# Runs gremlins mutation testing on Go packages changed in the current PR.
# Writes a markdown summary table to $GITHUB_STEP_SUMMARY (or stdout when local).

REPO_ROOT=$(git rev-parse --show-toplevel)
BASE_REF="${GITHUB_BASE_REF:-main}"

# ── 1. Find changed Go source files (exclude tests) ────────────────────────
changed_files=$(git diff --name-only "origin/${BASE_REF}...HEAD" -- '*.go' \
  | grep -v '_test\.go$' || true)

if [[ -z "$changed_files" ]]; then
  echo "No Go source files changed — nothing to mutate."
  exit 0
fi

# ── 2. Map to unique package directories ────────────────────────────────────
changed_packages=$(echo "$changed_files" | xargs -I{} dirname {} | sort -u)

# ── 3. Filter to packages that have //go:build unit test files ──────────────
testable_packages=()
while IFS= read -r pkg; do
  pkg_abs="$REPO_ROOT/$pkg"
  if [[ -d "$pkg_abs" ]] && grep -qlr '//go:build unit' --include='*_test.go' "$pkg_abs" 2>/dev/null; then
    testable_packages+=("$pkg")
  fi
done <<< "$changed_packages"

if [[ ${#testable_packages[@]} -eq 0 ]]; then
  echo "Changed packages have no unit-tagged tests — nothing to mutate."
  exit 0
fi

echo "Packages to test: ${testable_packages[*]}"

# ── 4. Helper: find nearest go.mod by walking up from a directory ───────────
find_module_root() {
  local dir="$REPO_ROOT/$1"
  while [[ "$dir" != "$REPO_ROOT" && "$dir" != "/" ]]; do
    if [[ -f "$dir/go.mod" ]]; then
      echo "$dir"
      return
    fi
    dir=$(dirname "$dir")
  done
  # Fall back to repo root
  echo "$REPO_ROOT"
}

# ── 5. Run gremlins per package, collect results ────────────────────────────
declare -a results=()  # "pkg|score|killed|lived|timed_out"

for pkg in "${testable_packages[@]}"; do
  module_root=$(find_module_root "$pkg")
  # Compute relative package path from the module root
  rel_pkg="${REPO_ROOT}/${pkg#/}"
  rel_pkg="${rel_pkg#"$module_root"/}"

  echo ""
  echo "=== $pkg (module: ${module_root#"$REPO_ROOT"/}) ==="

  json_file=$(mktemp)
  # gremlins may exit non-zero when mutants survive — that's expected
  if (cd "$module_root" && gremlins unleash \
      --tags unit \
      --timeout-coefficient 10 \
      --workers 4 \
      -o "$json_file" \
      "./$rel_pkg" 2>&1); then
    true
  fi

  # Parse JSON output
  row=$(python3 -c "
import json, sys
try:
    data = json.load(open('$json_file'))
except Exception:
    print('$pkg|n/a|0|0|0')
    sys.exit(0)
killed = lived = timed_out = 0
for f in data.get('files', []):
    for m in f.get('mutations', []):
        s = m.get('status', '')
        if s == 'KILLED': killed += 1
        elif s == 'LIVED': lived += 1
        elif s == 'TIMED OUT': timed_out += 1
total = killed + lived
score = f'{killed/total*100:.1f}%' if total > 0 else 'n/a'
print(f'$pkg|{score}|{killed}|{lived}|{timed_out}')
")

  results+=("$row")
  rm -f "$json_file"
done

# ── 6. Write markdown summary ───────────────────────────────────────────────
summary=""
summary+="## Mutation Testing (changed packages)\n\n"
summary+="| Package | Score | Killed | Lived | Timed Out |\n"
summary+="|---------|-------|--------|-------|-----------|\n"

for row in "${results[@]}"; do
  IFS='|' read -r pkg score killed lived timed_out <<< "$row"
  summary+="| \`$pkg\` | $score | $killed | $lived | $timed_out |\n"
done

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo -e "$summary" >> "$GITHUB_STEP_SUMMARY"
  echo "Summary written to job summary."
else
  echo ""
  echo -e "$summary"
fi
