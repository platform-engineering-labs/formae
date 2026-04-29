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
PLUGINS_DIR="${FORMAE_PLUGIN_DIR:-$HOME/.pel/formae/plugins}"
PKLPROJECT_PATH="$FIXTURES_DIR/PklProject"

# Ensure version.semver exists (needed by formae PklProject).
# Strip any pre-release suffix (e.g. 0.85.0-dev → 0.85.0) so the build
# identity is the canonical semver, matching what schema-prerelease publishes
# to the hub.
VERSION_FILE="$REPO_ROOT/version.semver"
if [[ ! -f "$VERSION_FILE" ]]; then
    RAW_VERSION=$(git -C "$REPO_ROOT" describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*" 2>/dev/null || echo "0.0.0")
    VERSION="${RAW_VERSION%%-*}"
    echo "$VERSION" > "$VERSION_FILE"
    echo "Generated $VERSION_FILE ($VERSION)"
fi

# Resolve formae from the hub — mirrors how a real user's PklProject references
# formae (by hub URI, not by local path) and ensures PKL nominal type identity
# between the fixture's formae and the formae pinned by hub-fetched plugin
# schemas. Requires the corresponding schema version to have been published
# via the schema-prerelease workflow.
FORMAE_VERSION=$(cat "$VERSION_FILE")
FORMAE_URI="package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@${FORMAE_VERSION}"

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
            echo "ERROR: $alias plugin not found at $PLUGINS_DIR/$ns_dir/" >&2
            echo "Install plugins via 'formae plugin install $alias' against an agent whose orbital tree shares this directory, or set FORMAE_PLUGIN_DIR to a tree that has it." >&2
            exit 1
        else
            echo "WARN: $alias plugin not found at $PLUGINS_DIR/$ns_dir/ — fixtures importing this plugin will fail to evaluate" >&2
            return
        fi
    fi

    local base_uri version
    base_uri=$(pkl eval -x 'package.baseUri' "$plugin_dir/schema/pkl/PklProject")
    version=$(pkl eval -x 'version' "$plugin_dir/formae-plugin.pkl")
    echo "Using $alias plugin v$version from hub ($base_uri)" >&2
    echo "  [\"$alias\"] { uri = \"$base_uri@$version\" }"
}

# Resolve plugin schemas from the hub. All plugins are optional at this
# level — the e2e workflow installs only the plugins each test needs (matrix
# .plugins), so a single setup_pkl.sh run is shared across tests with
# different plugin sets. Fixtures that import a plugin not installed will
# fail to evaluate with a clear PKL error, which is the right failure mode.
AWS_DEP=$(hub_uri "aws" "aws" false)
AZURE_DEP=$(hub_uri "azure" "azure" false)
COMPOSE_DEP=$(hub_uri "compose" "compose" false)
GRAFANA_DEP=$(hub_uri "grafana" "grafana" false)

# Generate PklProject. Both formae core and plugins are pinned via hub URIs
# — matches a real user's setup, and avoids the PKL type-identity split that
# would happen if the fixture's formae and a plugin's formae were declared
# at different URIs. Each plugin dep is included only if its hub_uri call
# returned non-empty (i.e. the plugin is installed in $PLUGINS_DIR).
cat > "$PKLPROJECT_PATH" << EOF
amends "pkl:Project"

dependencies {
  ["formae"] { uri = "$FORMAE_URI" }
${AWS_DEP:+$AWS_DEP
}${AZURE_DEP:+$AZURE_DEP
}${COMPOSE_DEP:+$COMPOSE_DEP
}${GRAFANA_DEP:+$GRAFANA_DEP
}}
EOF

echo "Generated $PKLPROJECT_PATH"

# Remove stale deps.json so pkl project resolve regenerates it
rm -f "$FIXTURES_DIR/PklProject.deps.json"

# Resolve dependencies
pkl project resolve "$FIXTURES_DIR"
echo "PKL dependencies resolved successfully"
