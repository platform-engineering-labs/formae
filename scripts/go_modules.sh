#!/usr/bin/env bash
set -euo pipefail

# Prints every Go module directory tracked in the repository, one per line,
# as a path relative to the repository root. The root module is rendered as
# ".". Output is sorted under the C locale and duplicate-free.
#
# Modules are enumerated from Git's index rather than a filesystem walk, so a
# go.mod that is untracked or excluded by .gitignore is not reported: only
# what Git would check out counts as a module.

REPO_ROOT=$(git rev-parse --show-toplevel)

# Holds the NUL-delimited listing so it can be read with a plain redirect: a
# process substitution would let a failure of the command feeding it pass
# unnoticed under `pipefail`.
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

git -C "$REPO_ROOT" ls-files -z -- ':(glob)go.mod' ':(glob)**/go.mod' > "$tmp"

while IFS= read -r -d '' go_mod; do
  dir="${go_mod%/go.mod}"
  if [[ "$dir" == "go.mod" ]]; then
    dir="."
  fi

  # Prune vendor and testdata as exact path components, wherever they occur,
  # without pruning a directory that merely starts with one of those names
  # (e.g. vendor-tools, testdata2).
  case "/$dir/" in
    */vendor/*|*/testdata/*) continue ;;
  esac

  printf '%s\n' "$dir"
done < "$tmp" \
  | LC_ALL=C sort -u
