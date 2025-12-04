#!/usr/bin/env bash

export INSTALLPREFIX="/opt/pel"
export PLUGINDIR="~/.pel/formae/plugins"
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
        if pkg['OsArch']['OS'] == ENV['OS'] and pkg['OsArch']['Arch'] == ENV['ARCH'] and !pkg['Version'].include?('-')
          print pkg['Version']
          exit
        end
      end
    ")
  elif which jq > /dev/null; then
    version=$(curl -s https://hub.platform.engineering/binaries/repo.json | jq -r '[.Packages[] | select(.Version | index("-") | not) | select(.OsArch.OS == env.OS and .OsArch.Arch == env.ARCH)][0].Version')
  else
    echo "Could not find a ruby interpreter or jq, required by the installation, please install either package to continue!"
    exit 1
  fi
fi

if [[ $version == "" ]]; then
  echo "No version found for platform: ${OS}-${ARCH}"
  echo "most likely it is unsupported for now"
  exit 1
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

# Install plugin executables to user directory
for f in "${INSTALLPREFIX}/formae/plugins/"*; do
  if file "$f" | grep -q "executable"; then
    name=$(basename "$f")
    dest="$HOME/.pel/formae/plugins/${name}/v${version}"
    mkdir -p "$dest"
    cp "$f" "$dest/"
  fi
done

echo "Done."
echo ""
echo "IMPORTANT: ensure you add /opt/pel/formae/bin to your PATH, and reload your shell configuration"
echo ""

CURRENT_SHELL=$(basename "$SHELL" 2>/dev/null || echo "bash")

setup_bash_completions() {
  echo "Setting up bash completions..."
  
  if [[ "$OS" == "darwin" ]]; then
    # Check if bash-completion is installed on macOS
    if ! brew list bash-completion &>/dev/null && ! brew list bash-completion@2 &>/dev/null; then
      echo "Warning: bash-completion is not installed. Install it with:"
      echo "  brew install bash-completion"
      echo "Then add the following to your ~/.bash_profile or ~/.bashrc:"
      echo '  [[ -r "/usr/local/etc/profile.d/bash_completion.sh" ]] && . "/usr/local/etc/profile.d/bash_completion.sh"'
      echo ""
      return
    fi
  fi
  
  # Generate completion file
  ${INSTALLPREFIX}/formae/bin/formae completion bash > formae-cli-completion.bash 2>/dev/null || {
    echo "Warning: Could not generate bash completions. Make sure formae is in your PATH."
    return
  }
  
  if [[ "$OS" == "darwin" ]]; then
    # macOS with Homebrew bash-completion
    completion_dir="/usr/local/etc/bash_completion.d"
    if [[ -d "$completion_dir" ]]; then
      if [[ $(id -u) = 0 ]]; then
        cp formae-cli-completion.bash "$completion_dir/formae"
      else
        sudo cp formae-cli-completion.bash "$completion_dir/formae"
      fi
      echo "Bash completions installed to $completion_dir"
    else
      mkdir -p ~/.local/share/bash-completion/completions
      cp formae-cli-completion.bash ~/.local/share/bash-completion/completions/formae
      echo "Bash completions installed to ~/.local/share/bash-completion/completions"
    fi
  else
    # Linux
    mkdir -p ~/.local/share/bash-completion/completions
    cp formae-cli-completion.bash ~/.local/share/bash-completion/completions/formae
    echo "Bash completions installed to ~/.local/share/bash-completion/completions"
  fi
  
  rm -f formae-cli-completion.bash
  echo "Bash completions setup complete!"
}

setup_zsh_completions() {
  echo "Setting up zsh completions..."
  
  # Create zsh completion directory if it doesn't exist
  zsh_completion_dir="${HOME}/.zsh/completions"
  mkdir -p "$zsh_completion_dir"
  
  ${INSTALLPREFIX}/formae/bin/formae completion zsh > "${zsh_completion_dir}/_formae" 2>/dev/null || {
    echo "Warning: Could not generate zsh completions. Make sure formae is in your PATH."
    return
  }
  
  if ! echo "$FPATH" | grep -q "$zsh_completion_dir"; then
    echo "Add the following to your ~/.zshrc:"
    echo "  fpath=(${zsh_completion_dir} \$fpath)"
    echo "  autoload -U compinit && compinit"
  fi
  
  echo "Zsh completions installed to ${zsh_completion_dir}/_formae"
  echo "Zsh completions setup complete!"
}

echo "Shell completions:"
if [[ "$OS" == "darwin" ]]; then
  echo "For zsh (default on macOS):"
  echo "  Add completions with: formae completion zsh > ~/.zsh/completions/_formae"
  echo "  Then add to ~/.zshrc: fpath=(~/.zsh/completions \$fpath) && autoload -U compinit && compinit"
  echo ""
  echo "For bash (requires bash-completion via Homebrew):"
  echo "  First install: brew install bash-completion"
  echo "  Then add completions with: formae completion bash > /usr/local/etc/bash_completion.d/formae"
else
  echo "For bash:"
  echo "  formae completion bash > ~/.local/share/bash-completion/completions/formae"
  echo "  mkdir -p ~/.local/share/bash-completion/completions"
  echo ""
  echo "For zsh:"
  echo "  mkdir -p ~/.zsh/completions"
  echo "  formae completion zsh > ~/.zsh/completions/_formae"
  echo "  Add to ~/.zshrc: fpath=(~/.zsh/completions \$fpath) && autoload -U compinit && compinit"
fi

echo ""

if ! "$skip_prompt"; then
  echo "Would you like to install shell completions for $CURRENT_SHELL? (y/N)"
  read -r install_completions
  if [[ "$install_completions" =~ ^[Yy]$ ]]; then
    case "$CURRENT_SHELL" in
      bash)
        setup_bash_completions
        ;;
      zsh)
        setup_zsh_completions
        ;;
      *)
        echo "Automatic completion setup not supported for $CURRENT_SHELL"
        echo "Please follow the manual instructions above."
        ;;
    esac
  fi
fi
