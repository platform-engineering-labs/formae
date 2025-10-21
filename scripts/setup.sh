#!/usr/bin/env bash

export INSTALLPREFIX="/opt/pel"
export OS=$(uname | tr '[:upper:]' '[:lower:]')
export ARCH=$(uname -m |  tr -d '_')

skip_prompt='false'
version='latest'

help() {
  echo "setup.sh installs formae from the PEL repository"
  echo " -h show help"
  echo " -v [version] select version"
  echo " -y skip confirmations"
  exit 0
}

while getopts 'hv:y' flag; do
  case "${flag}" in
    h) help ;;
    v) version="$OPTARG" ;;
    y) skip_prompt='true' ;;
  esac
done

if [[ "$version" == "latest" ]]; then
  if which ruby > /dev/null; then
    version=$(ruby -e "
      require 'net/http'
      require 'uri'
      require 'json'

      resp = Net::HTTP.get(URI('https://hub.platform.engineering/binaries/repo.json'))
      repo_json = JSON.parse(resp)

      repo_json['Packages'].each do |pkg|
        if pkg['OsArch']['OS'] == ENV['OS'] and pkg['OsArch']['Arch'] == ENV['ARCH']
          print pkg['Version']
          exit
        end
      end
    ")
  elif which jq > /dev/null; then
    version=$(curl -s https://hub.platform.engineering/binaries/repo.json | jq -r '[.Packages[] | select(.OsArch.OS == env.OS and .OsArch.Arch == env.ARCH)][0].Version')
  else
    echo "Could not find a ruby interpreter or jq, required by the installation, please install either package to continue!"
    exit 1
  fi
fi

if ! [ $(id -u) = 0 ]; then
  echo "This script requires escalated privileges to install Formae to: ${INSTALLPREFIX}/formae"
  echo "Your password will be required, to utilize sudo"
else
  echo "This script will install Formae to: ${INSTALLPREFIX}/formae"
fi

if ! "$skip_prompt"; then
  read -p "Type 'Y' to continue: " input
  if [[ "$input" == "Y" ]]; then
    echo "Continuing..."
  else
    echo "Exiting."
    exit 1
  fi
fi

pkgname="formae@${version}_${OS}-${ARCH}.tgz"

echo "Downloading: ${pkgname}"
if which curl > /dev/null; then
  if ! curl "https://hub.platform.engineering/binaries/pkgs/${pkgname}" 2>/dev/null > ${pkgname}; then
    echo "Failed to download: ${pkgname}"
    exit 1
  fi
elif which wget > /dev/null; then
   if ! wget -qc -O  ${pkgname} "https://hub.platform.engineering/binaries/pkgs/${pkgname}" 2>/dev/null; then
      echo "Failed to download: ${pkgname}"
      exit 1
    fi
else
  echo "Could not find: wget or curl, please install one so we can fetch the install package"
fi

echo "Installing..."

if ! [ $(id -u) = 0 ]; then
  sudo mkdir -m 755 -p "${INSTALLPREFIX}"
  sudo tar -zxf ${pkgname} -C "${INSTALLPREFIX}"
else
  mkdir -m 755 -p "${INSTALLPREFIX}"
  tar -zxf ${pkgname} -C "${INSTALLPREFIX}"
fi

echo "Done."
echo ""
echo "IMPORTANT: ensure you add /opt/pel/formae/bin to your PATH, and reload your shell configuration"