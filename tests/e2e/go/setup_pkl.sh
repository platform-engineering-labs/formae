#!/bin/bash

# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

# Sets up PKL dependencies for Go e2e tests by generating PklProject.
# Plugin schemas are resolved from the hub. The formae core schema uses
# the local build (since E2E tests run against the built-from-source agent).
# Versions are read from installed plugin manifests.

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

# hub_uri reads the baseUri and version from an installed plugin's schema
# PklProject and emits a PklProject dependency line using the hub URI.
#
# Args: <namespace_dir> <pkl_alias> <required: true|false>
hub_uri() {
    local ns_dir="$1"
    local alias="$2"
    local required="${3:-true}"

    local plugin_dir
    plugin_dir=$(find "$PLUGINS_DIR/$ns_dir" -mindepth 1 -maxdepth 1 -type d -name "v*" 2>/dev/null | sort -V | tail -n 1 || true)

    if [[ -z "$plugin_dir" ]] || [[ ! -f "$plugin_dir/schema/pkl/PklProject" ]]; then
        if [[ "$required" == "true" ]]; then
            echo "ERROR: $alias plugin not found at $PLUGINS_DIR/$ns_dir/"
            echo "Run 'make install-external-plugins' first."
            exit 1
        else
            echo "WARN: $alias plugin not found at $PLUGINS_DIR/$ns_dir/ — related E2E tests will be skipped" >&2
            return
        fi
    fi

    local base_uri version
    base_uri=$(pkl eval -x 'package.baseUri' "$plugin_dir/schema/pkl/PklProject")
    version=$(pkl eval -x 'version' "$plugin_dir/formae-plugin.pkl")
    echo "Using $alias plugin v$version from hub ($base_uri)" >&2
    echo "  [\"$alias\"] { uri = \"$base_uri@$version\" }"
}

# Resolve plugin schemas from the hub. AWS and Azure are required;
# compose and grafana are optional (needed for target resolvable tests).
AWS_DEP=$(hub_uri "aws" "aws" true)
AZURE_DEP=$(hub_uri "azure" "azure" true)
COMPOSE_DEP=$(hub_uri "docker" "compose" false)
GRAFANA_DEP=$(hub_uri "grafana" "grafana" false)

# Generate PklProject. Formae core uses local schema (testing from source);
# all plugins use published hub URIs.
cat > "$PKLPROJECT_PATH" << EOF
amends "pkl:Project"

dependencies {
  ["formae"] = import("../../../../internal/schema/pkl/schema/PklProject")
$AWS_DEP
$AZURE_DEP
${COMPOSE_DEP:+$COMPOSE_DEP
}${GRAFANA_DEP:+$GRAFANA_DEP
}}
EOF

echo "Generated $PKLPROJECT_PATH"

# Remove stale deps.json so pkl project resolve regenerates it
rm -f "$FIXTURES_DIR/PklProject.deps.json"

# Resolve dependencies
pkl project resolve "$FIXTURES_DIR"
echo "PKL dependencies resolved successfully"
