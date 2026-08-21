// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import "io"

// OidcCredentialPluginAnnouncement is sent by the broker to the agent's
// PluginCoordinator on startup, mirroring pkg/plugin.PluginAnnouncement's
// role for resource plugins. SpawnToken carries the value of
// FORMAE_SPAWN_TOKEN verbatim; Namespaces carries the manifest's
// NormalizedNamespaces().
type OidcCredentialPluginAnnouncement struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Namespaces []string `json:"namespaces"`
	SpawnToken string   `json:"spawnToken"`
}

// MarshalEDF/UnmarshalEDF let the announcement cross the Ergo network
// boundary. MarshalEDF on VALUE receiver, UnmarshalEDF on POINTER receiver
// (required by Ergo's RegisterTypeOf). Same encodeMsgpack/Decode codec as
// the rest of this package's wire types.
func (a OidcCredentialPluginAnnouncement) MarshalEDF(w io.Writer) error {
	return encodeMsgpack(w, &a)
}

func (a *OidcCredentialPluginAnnouncement) UnmarshalEDF(data []byte) error {
	return Decode(data, a)
}
