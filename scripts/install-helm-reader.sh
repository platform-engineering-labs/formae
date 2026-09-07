#!/usr/bin/env bash
# © 2026 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2
#
# install-helm-reader.sh — download a pinned pkl-reader-helm binary
# into the formae repo root so `make build` ships it next to the
# formae binary. The reader is needed at runtime to evaluate any
# forma that imports `@formae-helm/v<X.Y>/HelmChart.pkl`; formae's
# auto-register code (internal/schema/pkl/helm_reader.go) discovers
# it via exec.LookPath, so bundling it next to the formae binary
# means `pkg-bin` ships a self-contained dist/pel/bin/ tree.
#
# Pin policy: HELM_READER_VERSION env var (default tracks whatever
# the k8s plugin's `helm/PklProject` declares for `pkl-readers/helm`).
# Override for one-off testing.
#
# Output: ./pkl-reader-helm (chmod 755). Re-running is a no-op when
# the binary's `version` subcommand already reports the requested pin.
#
# Usage:
#   scripts/install-helm-reader.sh                  # use the pinned version
#   HELM_READER_VERSION=0.1.3 scripts/install-helm-reader.sh

set -euo pipefail

# Resolve repo root from this script's location so the target works
# regardless of the caller's CWD.
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)

DEFAULT_VERSION="0.1.2"
VERSION=${HELM_READER_VERSION:-$DEFAULT_VERSION}

TARGET="$REPO_ROOT/pkl-reader-helm"

# Map the running platform to apple/pkl-readers release asset names.
# macOS ships a universal binary; Linux is split by arch. Other
# platforms are unsupported — the helm reader isn't built for them.
case "$(uname -s)-$(uname -m)" in
  Darwin-*)                   ASSET="pkl-reader-helm-macos.bin" ;;
  Linux-x86_64)               ASSET="pkl-reader-helm-linux-amd64.bin" ;;
  Linux-aarch64|Linux-arm64)  ASSET="pkl-reader-helm-linux-aarch64.bin" ;;
  *)
    echo "install-helm-reader: unsupported platform $(uname -s)-$(uname -m)" >&2
    exit 1
    ;;
esac

URL="https://github.com/apple/pkl-readers/releases/download/helm@${VERSION}/${ASSET}"

# Skip the download when ./pkl-reader-helm already reports the
# requested version. `pkl-reader-helm version` (subcommand, not flag)
# prints `pkl-reader-helm <ver>` on its first line — match the second
# token loosely so future format tweaks don't churn the install.
if [[ -x "$TARGET" ]]; then
  if existing_version=$("$TARGET" version 2>/dev/null | awk 'NR==1 {print $2}'); then
    if [[ "$existing_version" == "$VERSION" ]]; then
      echo "install-helm-reader: pkl-reader-helm @ $VERSION already present"
      exit 0
    fi
    if [[ -n "$existing_version" ]]; then
      echo "install-helm-reader: replacing $existing_version -> $VERSION"
    fi
  fi
fi

# Download to a sibling temp file and rename atomically. Avoids leaving
# a half-written `pkl-reader-helm` behind if the user Ctrl-Cs mid-curl.
TMP=$(mktemp "$REPO_ROOT/.pkl-reader-helm.XXXXXX")
trap 'rm -f "$TMP"' EXIT

echo "install-helm-reader: downloading $URL"
if ! curl -fsSL "$URL" -o "$TMP"; then
  echo "install-helm-reader: download failed" >&2
  exit 1
fi

chmod 755 "$TMP"
mv "$TMP" "$TARGET"
trap - EXIT

echo "install-helm-reader: installed $TARGET (version $VERSION)"
"$TARGET" version 2>/dev/null | head -1 || true
