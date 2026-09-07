// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

// OidcCredentialPluginAnnouncement is sent by the broker to the agent's
// PluginCoordinator on startup, mirroring pkg/plugin.PluginAnnouncement's
// role for resource plugins. SpawnToken carries the value of
// FORMAE_SPAWN_TOKEN verbatim; Namespaces carries the manifest's
// NormalizedNamespaces().
//
// Its MarshalEDF/UnmarshalEDF hooks live in msgpack_edf.go with the rest of
// this package's cross-node message types.
type OidcCredentialPluginAnnouncement struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Namespaces []string `json:"namespaces"`
	SpawnToken string   `json:"spawnToken"`
}
