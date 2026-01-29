#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

# Sets up PKL dependencies for e2e tests by generating PklProject
# with the correct path to the installed AWS plugin schema.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PKL_DIR="$SCRIPT_DIR/pkl"
PLUGINS_DIR="$HOME/.pel/formae/plugins"
PKLPROJECT_PATH="$PKL_DIR/PklProject"

# Find installed AWS plugin version directory
AWS_PLUGIN_DIR=$(find "$PLUGINS_DIR/aws" -mindepth 1 -maxdepth 1 -type d -name "v*" 2>/dev/null | head -n 1)

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

# Extract version for logging
VERSION=$(pkl eval -x 'version' "$AWS_PLUGIN_DIR/formae-plugin.pkl")
echo "Using AWS plugin v$VERSION from $AWS_SCHEMA_DIR"

# Generate PklProject with absolute path
cat > "$PKLPROJECT_PATH" << EOF
amends "pkl:Project"

dependencies {
  ["formae"] = import("../../../plugins/pkl/schema/PklProject")
  ["aws"] = import("$AWS_SCHEMA_DIR/PklProject")
}
EOF

echo "Generated $PKLPROJECT_PATH"

# Remove stale deps.json so pkl project resolve regenerates it
rm -f "$PKL_DIR/PklProject.deps.json"
