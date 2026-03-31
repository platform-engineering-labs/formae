# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

export VERSION := `git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*"`
GITHUB := env("GITHUB_ACTIONS", "false")
CHANNEL := env("OPS_CHANNEL", "dev")

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