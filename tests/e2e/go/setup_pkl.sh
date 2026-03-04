#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

# Sets up PKL dependencies for Go e2e tests by generating PklProject
# with the correct paths to installed AWS and Azure plugin schemas.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
FIXTURES_DIR="$SCRIPT_DIR/fixtures"
PLUGINS_DIR="$HOME/.pel/formae/plugins"
PKLPROJECT_PATH="$FIXTURES_DIR/PklProject"

# Ensure version.semver exists (needed by formae PklProject)
VERSION_FILE="$REPO_ROOT/version.semver"
if [[ ! -f "$VERSION_FILE" ]]; then
    VERSION=$(git -C "$REPO_ROOT" describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*" 2>/dev/null || echo "0.0.0-dev")
    echo "$VERSION" > "$VERSION_FILE"
    echo "Generated $VERSION_FILE with version $VERSION"
fi

# Find installed AWS plugin version directory
AWS_PLUGIN_DIR=$(find "$PLUGINS_DIR/aws" -mindepth 1 -maxdepth 1 -type d -name "v*" 2>/dev/null | sort -V | tail -n 1)

if [[ -z "$AWS_PLUGIN_DIR" ]]; then
    echo "ERROR: AWS plugin not found at $PLUGINS_DIR/aws/"
    echo "Run 'make install-external-plugins' first."
    exit 1
fi

AWS_SCHEMA_DIR="$AWS_PLUGIN_DIR/schema/pkl"

if [[ ! -f "$AWS_SCHEMA_DIR/PklProject" ]]; then
    echo "ERROR: AWS schema PklProject not found at $AWS_SCHEMA_DIR/PklProject"
    exit 1
fi

AWS_VERSION=$(pkl eval -x 'version' "$AWS_PLUGIN_DIR/formae-plugin.pkl")
echo "Using AWS plugin v$AWS_VERSION from $AWS_SCHEMA_DIR"

# Find installed Azure plugin version directory
AZURE_PLUGIN_DIR=$(find "$PLUGINS_DIR/azure" -mindepth 1 -maxdepth 1 -type d -name "v*" 2>/dev/null | sort -V | tail -n 1)

if [[ -z "$AZURE_PLUGIN_DIR" ]]; then
    echo "ERROR: Azure plugin not found at $PLUGINS_DIR/azure/"
    echo "Run 'make install-external-plugins' first."
    exit 1
fi

AZURE_SCHEMA_DIR="$AZURE_PLUGIN_DIR/schema/pkl"

if [[ ! -f "$AZURE_SCHEMA_DIR/PklProject" ]]; then
    echo "ERROR: Azure schema PklProject not found at $AZURE_SCHEMA_DIR/PklProject"
    exit 1
fi

AZURE_VERSION=$(pkl eval -x 'version' "$AZURE_PLUGIN_DIR/formae-plugin.pkl")
echo "Using Azure plugin v$AZURE_VERSION from $AZURE_SCHEMA_DIR"

# Generate PklProject with correct paths
cat > "$PKLPROJECT_PATH" << EOF
amends "pkl:Project"

dependencies {
  ["formae"] = import("../../../../internal/schema/pkl/schema/PklProject")
  ["aws"] = import("$AWS_SCHEMA_DIR/PklProject")
  ["azure"] = import("$AZURE_SCHEMA_DIR/PklProject")
}
EOF

echo "Generated $PKLPROJECT_PATH"

# Remove stale deps.json so pkl project resolve regenerates it
rm -f "$FIXTURES_DIR/PklProject.deps.json"

# Resolve dependencies
pkl project resolve "$FIXTURES_DIR"
echo "PKL dependencies resolved successfully"
