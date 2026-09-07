// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

// localInstaller is the minimal interface the install human path requires.
// CLIPluginManager satisfies it; tests inject stubs.
type localInstaller interface {
	LocalInstall(names []string) error
}

// localUninstaller is the minimal interface the uninstall human path requires.
type localUninstaller interface {
	LocalUninstall(names []string) error
}

// localUpdater is the minimal interface the update human path requires.
type localUpdater interface {
	LocalUpdate(names []string) error
}
