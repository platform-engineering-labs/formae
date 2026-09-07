// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

// OidcCredentialPluginInfo holds discovery information for an external
// oidc-credential plugin binary.
type OidcCredentialPluginInfo struct {
	Name       string
	Version    string
	BinaryPath string
	Namespaces []string
}
