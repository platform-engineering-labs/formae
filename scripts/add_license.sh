#!/bin/bash
# Add license headers to source files (idempotent)

if ! command -v reuse >/dev/null 2>&1; then
    echo "Error: REUSE tool not installed."
    echo "Install with: pipx install reuse"
    exit 1
fi

echo "Adding license headers to source files (this is idempotent)..."

echo "Processing Go files..."
find . -name "*.go" -not -path "*/.git/*" | \
    xargs reuse annotate --copyright="Platform Engineering Labs Inc." --year=2025 \
    --copyright-prefix=symbol --license=FSL-1.1-ALv2

echo "Processing Pkl files..."
find . -name "*.pkl" -not -path "*/.git/*" | \
    xargs reuse annotate --copyright="Platform Engineering Labs Inc." --year=2025 \
    --copyright-prefix=symbol --license=FSL-1.1-ALv2 --style c

echo "Processing Python files..."
find . -name "*.py" -not -path "*/.git/*" | \
    xargs reuse annotate --copyright="Platform Engineering Labs Inc." --year=2025 \
    --copyright-prefix=symbol --license=FSL-1.1-ALv2 || true

echo "Processing Shell scripts..."
find . -name "*.sh" -not -path "*/.git/*" -not -path "*/scripts/*" | \
    xargs reuse annotate --copyright="Platform Engineering Labs Inc." --year=2025 \
    --copyright-prefix=symbol --license=FSL-1.1-ALv2 || true

echo "Done! Run 'make lint-reuse' to verify"

