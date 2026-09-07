// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package credential

import "io"

// MarshalEDF/UnmarshalEDF implementations for all cross-node message types.
// These use encodeMsgpack/decodeMsgpack for MessagePack + zstd compression.
//
// MarshalEDF on VALUE receiver, UnmarshalEDF on POINTER receiver
// (required by Ergo's RegisterTypeOf).

func (m OidcCredentialPluginAnnouncement) MarshalEDF(w io.Writer) error {
	return encodeMsgpack(w, &m)
}
func (m *OidcCredentialPluginAnnouncement) UnmarshalEDF(data []byte) error {
	return decodeMsgpack(data, m)
}

func (m OidcIdentityTokenRequest) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, &m) }
func (m *OidcIdentityTokenRequest) UnmarshalEDF(data []byte) error {
	return decodeMsgpack(data, m)
}

func (m IdentityTokenResponse) MarshalEDF(w io.Writer) error { return encodeMsgpack(w, &m) }
func (m *IdentityTokenResponse) UnmarshalEDF(data []byte) error {
	return decodeMsgpack(data, m)
}
