// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecode_RoundTripsRequest(t *testing.T) {
	in := &OidcIdentityTokenRequest{Audience: "sts.amazonaws.com", RequestID: "r-1"}
	b, err := Encode(in)
	require.NoError(t, err)
	var out OidcIdentityTokenRequest
	require.NoError(t, Decode(b, &out))
	require.Equal(t, in, &out)
}

func TestDecode_IgnoresUnknownKeysAndZeroFillsMissing(t *testing.T) {
	type wideRequest struct {
		Audience string `json:"audience"`
		Extra    string `json:"extra"`
	}
	b, err := Encode(&wideRequest{Audience: "sts.amazonaws.com", Extra: "x"})
	require.NoError(t, err)
	var out OidcIdentityTokenRequest
	require.NoError(t, Decode(b, &out))
	require.Equal(t, "sts.amazonaws.com", out.Audience)
	require.Empty(t, out.RequestID)
}

// credentialGoldenFixtureHex is the hex-encoded, msgpack+zstd-encoded form
// of the OidcIdentityTokenRequest built in
// TestEncode_MatchesPluginCodecGoldenFixture below. It is mirrored verbatim
// in pkg/plugin/msgpack_test.go's TestEncodeMsgpack_MatchesCredentialGoldenFixture,
// which encodes the same field values through pkg/plugin's own (unexported)
// encodeMsgpack. The two codecs must be byte-identical for this to pass on
// both sides: it proves the agent (pkg/plugin) and an oidc-credential broker
// (pkg/credential) speak the same wire format despite living in separate Go
// modules with independently-vendored dependencies.
const credentialGoldenFixtureHex = "28b52ffd0400b9010082a861756469656e6365b17374" +
	"732e616d617a6f6e6177732e636f6da9726571756573744964b0676f6c64656e2d6669787475" +
	"72652d31c410421f"

func TestEncode_MatchesPluginCodecGoldenFixture(t *testing.T) {
	req := &OidcIdentityTokenRequest{Audience: "sts.amazonaws.com", RequestID: "golden-fixture-1"}
	b, err := Encode(req)
	require.NoError(t, err)

	got := hex.EncodeToString(b)
	t.Logf("golden hex: %s", got)

	require.Equal(t, credentialGoldenFixtureHex, got)
}
