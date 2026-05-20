#!/usr/bin/env bash
# © 2026 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2
#
# install-atlas.sh — download a pinned atlas CLI binary into the formae
# repo root so `make build` ships it next to the formae binary and
# `pkg-bin` includes it in dist/pel/bin/. The atlas binary is required
# at runtime by formae-plugin-atlas (and any future plugin that wraps
# atlas's CLI surface); the plugin discovers it via exec.LookPath, so
# bundling it next to the formae binary keeps the installed tree
# self-contained.
#
# Pin policy: ATLAS_VERSION env var. Default is "latest" because
# release.ariga.io currently only serves a `latest` URL — once Ariga
# exposes per-version URLs (e.g. .../atlas-linux-amd64-v0.39.1) switch
# this to a real semver pin.
#
# Output: ./atlas (chmod 755). Re-running with a real version pin is a
# no-op when the binary's `version` subcommand already reports that pin;
# with `latest` we always re-download.
#
# Usage:
#   scripts/install-atlas.sh                # use the default pin
#   ATLAS_VERSION=v0.39.1 scripts/install-atlas.sh

set -euo pipefail

# Resolve repo root from this script's location so the target works
# regardless of the caller's CWD.
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)

# release.ariga.io currently only resolves "latest"; specific versions
# are not served at well-known paths today. Once Ariga exposes
# per-version URLs, switch DEFAULT_VERSION to a real semver pin.
DEFAULT_VERSION="latest"
VERSION=${ATLAS_VERSION:-$DEFAULT_VERSION}

TARGET="$REPO_ROOT/atlas"

# Map the running platform to the atlas release asset name.
case "$(uname -s)-$(uname -m)" in
  Darwin-x86_64)              ASSET="atlas-darwin-amd64-${VERSION}" ;;
  Darwin-arm64)               ASSET="atlas-darwin-arm64-${VERSION}" ;;
  Linux-x86_64)               ASSET="atlas-linux-amd64-${VERSION}" ;;
  Linux-aarch64|Linux-arm64)  ASSET="atlas-linux-arm64-${VERSION}" ;;
  *)
    echo "install-atlas: unsupported platform $(uname -s)-$(uname -m)" >&2
    exit 1
    ;;
esac

URL="https://release.ariga.io/atlas/${ASSET}"

# Idempotency: for a real version pin, skip the download when the
# existing binary already reports that version. The atlas binary prints
# its version on the first line of `atlas version` (e.g. "atlas version v0.39.1").
# With VERSION=latest there's no useful idempotency check (the upstream
# binary can change at any time), so always re-download.
if [[ "$VERSION" != "latest" && -x "$TARGET" ]]; then
  if existing_version=$("$TARGET" version 2>/dev/null | head -1 | awk '{print $2}'); then
    if [[ "$existing_version" == "$VERSION" || "v$existing_version" == "$VERSION" ]]; then
      echo "install-atlas: atlas @ $VERSION already present"
      exit 0
    fi
    if [[ -n "$existing_version" ]]; then
      echo "install-atlas: replacing $existing_version -> $VERSION"
    fi
  fi
fi
if [[ "$VERSION" == "latest" && -x "$TARGET" ]]; then
  echo "install-atlas: atlas present (latest pin; not re-downloading — set ATLAS_VERSION to force)"
  exit 0
fi

# Download to a sibling temp file and rename atomically. Avoids leaving
# a half-written `atlas` behind if the user Ctrl-Cs mid-curl.
TMP=$(mktemp "$REPO_ROOT/.atlas.XXXXXX")
trap 'rm -f "$TMP"' EXIT

echo "install-atlas: downloading $URL"
if ! curl -fsSL "$URL" -o "$TMP"; then
  echo "install-atlas: download failed" >&2
  exit 1
fi

chmod 755 "$TMP"
mv "$TMP" "$TARGET"
trap - EXIT

echo "install-atlas: installed $TARGET"
"$TARGET" version 2>/dev/null | head -1 || true
