# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

set shell := ["bash", "-cu"]

export VERSION := `git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*" | cut -d'-' -f1`

export CHANNEL := ```
    version=$(git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*")
    channel=$(echo $version | cut -d'-' -f2-)
    if [[ "$channel" == "$version" ]]; then
        echo "stable"
    else
        echo $channel
    fi
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