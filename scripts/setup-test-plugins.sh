#!/usr/bin/env bash
# © 2026 Platform Engineering Labs Inc.
# SPDX-License-Identifier: FSL-1.1-ALv2
#
# Builds plugin binaries from source and stages them into a test-scoped
# plugin tree. Used by e2e tests until plugins are published to the
# `community` orbital repo.
#
# Usage:
#   scripts/setup-test-plugins.sh <output-dir>
#
# Environment:
#   PLUGIN_REFS  Optional comma-separated name=ref pairs to override the
#                default branch for one or more plugins
#                (e.g., aws=feat/foo,azure=feat/bar).

set -euo pipefail

OUT="${1:?usage: $0 <output-dir>}"
PLUGIN_REFS="${PLUGIN_REFS:-}"

# Plugins required by the e2e suite.
# - aws/azure: mandatory for cloud provider tests.
# - compose/grafana: optional, used by target-resolvable tests; setup_pkl.sh
#   gracefully skips related fixtures if missing.
# - auth-basic: required by the agent at startup (formae refuses to boot
#   without an auth plugin).
# - sftp: required by TestPluginConfig (imports plugins:/Sftp.pkl).
PLUGINS=(aws azure compose grafana auth-basic sftp)

# Resolve any ref overrides into an associative array.
declare -A REF
if [[ -n "$PLUGIN_REFS" ]]; then
    IFS=',' read -ra PAIRS <<< "$PLUGIN_REFS"
    for pair in "${PAIRS[@]}"; do
        REF["${pair%%=*}"]="${pair##*=}"
    done
fi

mkdir -p "$OUT"

for p in "${PLUGINS[@]}"; do
    REPO_URL="https://github.com/platform-engineering-labs/formae-plugin-${p}.git"
    REF_FOR_PLUGIN="${REF[$p]:-main}"
    WORK="$(mktemp -d)"
    trap 'rm -rf "$WORK"' RETURN

    echo "==> Cloning $REPO_URL @ $REF_FOR_PLUGIN"
    if ! git clone --depth 1 --branch "$REF_FOR_PLUGIN" "$REPO_URL" "$WORK" 2>&1; then
        # If the branch doesn't exist (e.g., for an optional plugin), skip.
        echo "    skipped: could not clone $REPO_URL @ $REF_FOR_PLUGIN"
        continue
    fi

    pushd "$WORK" >/dev/null
    echo "==> Building $p"
    make build

    NAME=$(pkl eval -x 'name' formae-plugin.pkl)
    VERSION=$(pkl eval -x 'version' formae-plugin.pkl)
    TYPE=$(pkl eval -x 'if (this.hasProperty("type")) type else "resource"' formae-plugin.pkl)

    # Auth plugins are staged by name; resource plugins by lowercase namespace.
    if [[ "$TYPE" == "auth" ]]; then
        STAGE_DIR="$NAME"
    else
        NAMESPACE=$(pkl eval -x 'namespace' formae-plugin.pkl)
        STAGE_DIR=$(echo "$NAMESPACE" | tr '[:upper:]' '[:lower:]')
    fi

    STAGE="$OUT/$STAGE_DIR/v$VERSION"
    mkdir -p "$STAGE/schema"

    cp "bin/$NAME" "$STAGE/$STAGE_DIR"
    cp formae-plugin.pkl "$STAGE/"
    if [[ -d schema/pkl ]]; then cp -r schema/pkl "$STAGE/schema/"; fi
    if [[ -f schema/Config.pkl ]]; then cp schema/Config.pkl "$STAGE/schema/"; fi

    echo "==> Staged $NAME v$VERSION at $STAGE"
    popd >/dev/null
done

echo "==> All test plugins staged in $OUT"
