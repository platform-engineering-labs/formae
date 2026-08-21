// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package credential

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEDFHooks_RoundTripRequest(t *testing.T) {
	in := OidcIdentityTokenRequest{Audience: "sts.amazonaws.com", RequestID: "r-1"}

	var buf bytes.Buffer
	require.NoError(t, in.MarshalEDF(&buf))

	var out OidcIdentityTokenRequest
	require.NoError(t, out.UnmarshalEDF(buf.Bytes()))
	require.Equal(t, in, out)
}

func TestEDFHooks_RoundTripResponse(t *testing.T) {
	in := IdentityTokenResponse{ErrorCode: ErrCodeMintFailed}

	var buf bytes.Buffer
	require.NoError(t, in.MarshalEDF(&buf))

	var out IdentityTokenResponse
	require.NoError(t, out.UnmarshalEDF(buf.Bytes()))
	require.Equal(t, in, out)
}

func TestUnmarshalEDF_IgnoresUnknownKeysAndZeroFillsMissing(t *testing.T) {
	type wideRequest struct {
		Audience string `json:"audience"`
		Extra    string `json:"extra"`
	}
	var buf bytes.Buffer
	require.NoError(t, encodeMsgpack(&buf, &wideRequest{Audience: "sts.amazonaws.com", Extra: "x"}))

	var out OidcIdentityTokenRequest
	require.NoError(t, out.UnmarshalEDF(buf.Bytes()))
	require.Equal(t, "sts.amazonaws.com", out.Audience)
	require.Empty(t, out.RequestID)
}

func TestUnmarshalEDF_RejectsAnUndecodablePayload(t *testing.T) {
	var out OidcIdentityTokenRequest
	require.Error(t, out.UnmarshalEDF([]byte("not a valid encoded request")))
}

// credentialGoldenFixtureHex is the hex-encoded, msgpack+zstd-encoded form
// of the OidcIdentityTokenRequest built in
// TestMarshalEDF_MatchesPluginCodecGoldenFixture below. It is mirrored
// verbatim in pkg/plugin/msgpack_test.go's
// TestEncodeMsgpack_MatchesCredentialGoldenFixture, which encodes the same
// field values through pkg/plugin's own (unexported) encodeMsgpack. The two
// codecs must be byte-identical for this to pass on both sides: it proves
// the agent (pkg/plugin) and an oidc-credential broker (pkg/credential)
// speak the same wire format despite living in separate Go modules with
// independently-vendored dependencies.
const credentialGoldenFixtureHex = "28b52ffd0400b9010082a861756469656e6365b17374" +
	"732e616d617a6f6e6177732e636f6da9726571756573744964b0676f6c64656e2d6669787475" +
	"72652d31c410421f"

func TestMarshalEDF_MatchesPluginCodecGoldenFixture(t *testing.T) {
	req := OidcIdentityTokenRequest{Audience: "sts.amazonaws.com", RequestID: "golden-fixture-1"}

	var buf bytes.Buffer
	require.NoError(t, req.MarshalEDF(&buf))

	got := hex.EncodeToString(buf.Bytes())
	t.Logf("golden hex: %s", got)

	require.Equal(t, credentialGoldenFixtureHex, got)
}
