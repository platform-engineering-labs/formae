#!/usr/bin/env bash

# Returns the arch for binaries from people that think it is ok to use values not reported by uname
OS=""
ARCH=""


if uname | grep -i darwin > /dev/null; then
  OS="macos"
elif uname | grep -i linux > /dev/null; then
  OS="linux"
fi

if uname -m | grep -i arm > /dev/null; then
  ARCH="aarch64"
elif uname -m | grep -i aarch > /dev/null; then
  ARCH="aarch64"
elif uname -m | grep -i x86 > /dev/null; then
  ARCH="amd64"
fi

echo "${OS}-${ARCH}"