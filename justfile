# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

set shell := ["bash", "-cu"]

export VERSION := `git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*" | cut -d'-' -f1`

export CHANNEL := ```
    # Channel — `stable` for X.Y.Z tags, `dev` for X.Y.Z-dev[.N] tags.
    # An earlier shape took everything after the first `-` (cut -d'-' -f2-)
    # and produced literal `dev.2` for `0.85.0-dev.2`, which orbital wrote
    # as a real channel name and broke downstream installs.
    version=$(git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*")
    if echo "$version" | grep -q -- '-'; then echo dev; else echo stable; fi
```

GITHUB := env("GITHUB_ACTIONS", "false")

default: clean build setup

clean:
	find . -name '*.opkg' -type f -delete

build:
    make pkg-bin
    rm -rf dist/pel/formae/bin

setup:
   {{ if GITHUB != "false" { "ops setup" } else {""} }}

pkg: clean build setup
    ops opkg build --secure --target-path dist/pel

publish: pkg
    ops publish --repo pel --channel {{ CHANNEL }} *.opkg