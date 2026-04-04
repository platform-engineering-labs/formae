// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

// ResourcePluginInfo contains metadata for external resource plugins
// that run as separate processes.
type ResourcePluginInfo struct {
	Name       string // Plugin name from directory (e.g., "cloudflare-dns")
	Namespace  string // Namespace from manifest (e.g., "CLOUDFLARE")
	Version    string
	BinaryPath string
}

// AuthPluginInfo holds discovery information for an external auth plugin binary.
type AuthPluginInfo struct {
	Name       string
	Version    string
	BinaryPath string
}
