// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIssuer      = "https://auth.formae.ai"
	testOtherIssuer = "https://auth.customer.example"

	// testPassword is the secret a basic-auth block carries. No refusal may
	// repeat it back.
	testPassword = "hunter2-not-in-any-output"

	// testToken is the credential itself, without its scheme.
	testToken = "gate-token-value"
)

// rawAuth renders an auth block as raw JSON, so a test can express blocks the
// Go struct cannot hold — a numeric clientId, a list-valued scopes.
func rawAuth(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	return json.RawMessage(marshalJSON(t, fields))
}

// oidcAuth returns our oidc CLI block, with the given fields overridden. A nil
// value removes a field, so a test can express an absent role.
func oidcAuth(t *testing.T, overrides map[string]any) json.RawMessage {
	t.Helper()
	fields := map[string]any{
		"type":     "oidc",
		"role":     "cli",
		"issuer":   testIssuer,
		"clientId": "formae-cli",
		"scopes":   "openid profile email offline_access",
	}
	for k, v := range overrides {
		if v == nil {
			delete(fields, k)
			continue
		}
		fields[k] = v
	}
	return rawAuth(t, fields)
}

// hosted returns a hosted connection carrying the given auth block.
func hosted(auth json.RawMessage) *pkgmodel.HostedConnection {
	return &pkgmodel.HostedConnection{
		Endpoint:     testOrigin,
		Installation: testUUIDA,
		Auth:         auth,
	}
}

// bearerHeader returns a header carrying value under the canonical key, the
// way an auth plugin's response reaches the CLI when it is usable at all.
func bearerHeader(value string) http.Header {
	h := http.Header{}
	h.Set("Authorization", value)
	return h
}

// testPlatform is the platform every gate test runs against unless it is
// testing the platform itself.
func testPlatform() platform {
	return platform{Origin: testOrigin, Issuer: testIssuer}
}

// gateCase is one gate decision: the connection and the credential the auth
// plugin produced for it.
type gateCase struct {
	name string
	conn pkgmodel.Connection
	hdr  http.Header
	// wantReason is the field or condition name the refusal must mention,
	// following the decode test's wantField.
	wantReason string
}

// refusalCases enumerates every shape that must not reach the control plane.
// The list is shared by the decision test and the request-count test, so a
// case added to it is proved to both refuse and send nothing.
func refusalCases(t *testing.T) []gateCase {
	t.Helper()
	return []gateCase{
		{
			name:       "no connection at all",
			conn:       nil,
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "hosted connection",
		},
		{
			name:       "a typed nil hosted connection",
			conn:       (*pkgmodel.HostedConnection)(nil),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "hosted connection",
		},
		{
			name:       "a classic connection carrying our own oidc block",
			conn:       &pkgmodel.ClassicConnection{URL: "https://agent.example", Port: 443, Auth: oidcAuth(t, nil)},
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "hosted connection",
		},
		{
			name: "hosted with a basic-auth block",
			conn: hosted(rawAuth(t, map[string]any{
				"type":     "basic",
				"username": "admin",
				"password": testPassword,
			})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "oidc auth plugin in its type field",
		},
		{
			name:       "hosted with no auth block",
			conn:       hosted(nil),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "carries no auth block",
		},
		{
			name:       "hosted with an empty auth block",
			conn:       hosted(rawAuth(t, map[string]any{})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "oidc auth plugin in its type field",
		},
		{
			name:       "hosted with an auth block that is not an object",
			conn:       hosted(json.RawMessage(`["oidc"]`)),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "JSON object of string fields",
		},
		{
			name:       "hosted with an auth block that is not JSON",
			conn:       hosted(json.RawMessage(`{`)),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "JSON object of string fields",
		},
		{
			name:       "oidc against a foreign issuer",
			conn:       hosted(oidcAuth(t, map[string]any{"issuer": testOtherIssuer})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "issuer other than",
		},
		{
			name:       "oidc against an issuer that only shares our host's suffix",
			conn:       hosted(oidcAuth(t, map[string]any{"issuer": "https://evil-auth.formae.ai"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "issuer other than",
		},
		{
			name:       "oidc with no issuer",
			conn:       hosted(oidcAuth(t, map[string]any{"issuer": nil})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "usable issuer origin",
		},
		{
			name:       "oidc with an issuer that is not an origin",
			conn:       hosted(oidcAuth(t, map[string]any{"issuer": "auth.formae.ai"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "usable issuer origin",
		},
		{
			name:       "oidc with an issuer carrying a path",
			conn:       hosted(oidcAuth(t, map[string]any{"issuer": testIssuer + "/realms/formae"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "usable issuer origin",
		},
		{
			name:       "the agent role",
			conn:       hosted(oidcAuth(t, map[string]any{"role": "agent"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "role other than",
		},
		{
			name:       "no role at all",
			conn:       hosted(oidcAuth(t, map[string]any{"role": nil})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "sets no role",
		},
		{
			name:       "an empty role",
			conn:       hosted(oidcAuth(t, map[string]any{"role": ""})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "sets no role",
		},
		{
			name:       "a role in another case",
			conn:       hosted(oidcAuth(t, map[string]any{"role": "CLI"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "role other than",
		},
		{
			name:       "another plugin's type",
			conn:       hosted(oidcAuth(t, map[string]any{"type": "basic"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "oidc auth plugin in its type field",
		},
		{
			name:       "no type at all",
			conn:       hosted(oidcAuth(t, map[string]any{"type": nil})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "oidc auth plugin in its type field",
		},
		{
			name:       "a type in another case",
			conn:       hosted(oidcAuth(t, map[string]any{"type": "OIDC"})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "oidc auth plugin in its type field",
		},
		{
			name:       "clientId as a JSON number",
			conn:       hosted(oidcAuth(t, map[string]any{"clientId": 42})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "\"clientId\" field is not a string",
		},
		{
			name:       "scopes as a JSON array",
			conn:       hosted(oidcAuth(t, map[string]any{"scopes": []string{"openid", "profile"}})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "\"scopes\" field is not a string",
		},
		{
			name:       "type as a JSON number",
			conn:       hosted(oidcAuth(t, map[string]any{"type": 1})),
			hdr:        bearerHeader("Bearer " + testToken),
			wantReason: "\"type\" field is not a string",
		},
		{
			name:       "a non-Bearer credential",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        bearerHeader("Basic YWRtaW46" + testPassword),
			wantReason: "Bearer credential",
		},
		{
			name:       "the credential under a non-canonical key",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        http.Header{"authorization": []string{"Bearer " + testToken}},
			wantReason: "Bearer credential",
		},
		{
			name:       "the credential under another header entirely",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        bearerHeaderNamed("X-Formae-Token", "Bearer "+testToken),
			wantReason: "Bearer credential",
		},
		{
			name:       "no credential",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        http.Header{},
			wantReason: "Bearer credential",
		},
		{
			name:       "a nil header",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        nil,
			wantReason: "Bearer credential",
		},
		{
			name:       "an empty credential",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        bearerHeader(""),
			wantReason: "Bearer credential",
		},
		{
			name:       "a Bearer scheme with no token",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        bearerHeader("Bearer"),
			wantReason: "Bearer credential",
		},
		{
			name:       "a Bearer scheme with a blank token",
			conn:       hosted(oidcAuth(t, nil)),
			hdr:        bearerHeader("Bearer   "),
			wantReason: "Bearer credential",
		},
	}
}

// bearerHeaderNamed returns a header carrying value under an arbitrary name.
func bearerHeaderNamed(name, value string) http.Header {
	h := http.Header{}
	h.Set(name, value)
	return h
}

// TestGateSync_AllowsOurOwnHostedOidcProfile pins the one shape that syncs:
// a hosted connection whose oidc CLI block names the issuer of the platform
// we are about to talk to, with a Bearer under the canonical key.
func TestGateSync_AllowsOurOwnHostedOidcProfile(t *testing.T) {
	credential := "Bearer " + testToken

	got := gateSync(hosted(oidcAuth(t, nil)), testPlatform(), bearerHeader(credential))

	require.True(t, got.OK, "reason: %s", got.Reason)
	assert.Empty(t, got.Reason, "an allowed sync has nothing to explain")
	assert.Equal(t, credential, got.Bearer, "the credential is passed on exactly as the plugin produced it")
	assert.Equal(t, cliAuthBlock{
		Type:     "oidc",
		Role:     "cli",
		Issuer:   testIssuer,
		ClientID: "formae-cli",
		Scopes:   "openid profile email offline_access",
	}, got.Auth)
}

// TestGateSync_ComparesIssuersCanonically pins that the issuer check is an
// origin comparison and not a string one: two spellings of our issuer are our
// issuer.
func TestGateSync_ComparesIssuersCanonically(t *testing.T) {
	tests := []struct {
		name   string
		issuer string
	}{
		{name: "mixed case host", issuer: "https://Auth.Formae.AI"},
		{name: "redundant port", issuer: "https://auth.formae.ai:443"},
		{name: "both", issuer: "https://Auth.Formae.AI:443"},
		{name: "trailing slash", issuer: "https://auth.formae.ai/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gateSync(
				hosted(oidcAuth(t, map[string]any{"issuer": tc.issuer})),
				testPlatform(),
				bearerHeader("Bearer "+testToken),
			)

			assert.True(t, got.OK, "reason: %s", got.Reason)
			assert.Equal(t, tc.issuer, got.Auth.Issuer,
				"the gate reports the block it validated, spelling included; canonicalising is the comparison's business")
		})
	}
}

// TestGateSync_LeavesTheOptionalFieldsAsTheyCame pins that the gate reports
// the block it was given. clientId and scopes are the only fields that may be
// defaulted, and defaulting them is the renderer's job — synthesising them
// here would hide from the renderer that the profile never named them.
func TestGateSync_LeavesTheOptionalFieldsAsTheyCame(t *testing.T) {
	got := gateSync(
		hosted(oidcAuth(t, map[string]any{"clientId": nil, "scopes": nil})),
		testPlatform(),
		bearerHeader("Bearer "+testToken),
	)

	require.True(t, got.OK, "reason: %s", got.Reason)
	assert.Empty(t, got.Auth.ClientID, "the gate does not synthesise %q", defaultOidcClientID)
	assert.Empty(t, got.Auth.Scopes, "the gate does not synthesise %q", defaultOidcScopes)
}

// TestGateSync_Refuses covers every shape that must not sync. A refusal
// carries no credential and no block, so nothing downstream can act on one it
// was not handed.
func TestGateSync_Refuses(t *testing.T) {
	for _, tc := range refusalCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			got := gateSync(tc.conn, testPlatform(), tc.hdr)

			assert.False(t, got.OK)
			assert.NotEmpty(t, got.Reason, "a refusal explains itself to a user who has just signed in")
			assert.Contains(t, got.Reason, tc.wantReason, "a refusal names the field or condition that failed")
			assert.Empty(t, got.Bearer, "a refusal carries no credential")
			assert.Equal(t, cliAuthBlock{}, got.Auth, "a refusal carries no block to render from")
		})
	}
}

// TestGateSync_RefusalNeverRepeatsASecretFromTheAuthBlock pins that a refusal
// names the field or condition that failed and never a value: the block that
// failed the gate is exactly the one that may hold a credential for some other
// system. Formatting got with %+v now goes through gateResult.String, so this
// also exercises that the redaction it does for a successful result does not
// somehow make a refusal's Reason less careful about the same thing.
func TestGateSync_RefusalNeverRepeatsASecretFromTheAuthBlock(t *testing.T) {
	tests := []struct {
		name string
		auth json.RawMessage
	}{
		{
			name: "a basic-auth block",
			auth: rawAuth(t, map[string]any{
				"type":     "basic",
				"username": "admin",
				"password": testPassword,
			}),
		},
		{
			name: "a block that fails to decode while carrying a password",
			auth: rawAuth(t, map[string]any{
				"type":     "oidc",
				"role":     "cli",
				"issuer":   testIssuer,
				"clientId": 42,
				"password": testPassword,
			}),
		},
		{
			name: "a foreign issuer alongside a password",
			auth: rawAuth(t, map[string]any{
				"type":     "oidc",
				"role":     "cli",
				"issuer":   testOtherIssuer,
				"password": testPassword,
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gateSync(hosted(tc.auth), testPlatform(), bearerHeader("Bearer "+testToken))

			require.False(t, got.OK)
			rendered := fmt.Sprintf("%+v", got)
			assert.NotContains(t, rendered, testPassword, "no value from the auth block may reach the user")
			assert.NotContains(t, rendered, testOtherIssuer, "not even a non-secret value is repeated back")
			assert.NotContains(t, rendered, testToken, "the credential never appears either")
		})
	}
}

// TestGateResult_StringNeverPrintsTheBearer pins the hazard the other test
// cannot: every refusal already carries a zero Bearer, so it proves nothing
// about a result that passed. A caller that logs or prints a successful
// gateResult with %v or %+v must still never see the credential, even though
// the field itself carries it for the caller that sends the request; the
// formatted form must still say enough to be useful for diagnosing a decision.
func TestGateResult_StringNeverPrintsTheBearer(t *testing.T) {
	got := gateSync(hosted(oidcAuth(t, nil)), testPlatform(), bearerHeader("Bearer "+testToken))
	require.True(t, got.OK, "reason: %s", got.Reason)
	require.NotEmpty(t, got.Bearer, "the test is meaningless without a bearer to redact")

	for _, format := range []string{"%v", "%+v"} {
		rendered := fmt.Sprintf(format, got)

		assert.NotContains(t, rendered, testToken, "the bearer must never reach a formatted result (%s)", format)
		assert.Contains(t, rendered, "OK:true", "the formatted form still says whether the gate passed (%s)", format)
		assert.Contains(t, rendered, oidcAuthType, "the formatted form still identifies the auth block it validated (%s)", format)
	}
}

// TestGateSync_ARefusalMakesNoControlPlaneRequest drives the real control-plane
// client through the gate's decision, the way the sync command will: a refused
// gate must leave the endpoint untouched, because the whole point of the gate
// is that the credential never leaves.
func TestGateSync_ARefusalMakesNoControlPlaneRequest(t *testing.T) {
	for _, tc := range refusalCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srv, seen := serveInstallations(t, validInstallation(testUUIDA))

			result := gateSync(tc.conn, platform{Origin: srv.URL, Issuer: testIssuer}, tc.hdr)
			if result.OK {
				_, err := newCloudClient(srv.URL).ListInstallations(context.Background(), result.Bearer)
				require.NoError(t, err)
			}

			requests, _, _, _ := seen.snapshot()
			assert.Zero(t, requests, "a refused gate must send nothing to the control plane")
		})
	}
}

// TestGateSync_AnAllowedGateSendsTheCredentialItValidated is the counterpart:
// the same wiring, with a block that passes, reaches the control plane with
// exactly the credential the gate returned.
func TestGateSync_AnAllowedGateSendsTheCredentialItValidated(t *testing.T) {
	srv, seen := serveInstallations(t, validInstallation(testUUIDA))
	credential := "Bearer " + testToken

	result := gateSync(hosted(oidcAuth(t, nil)), platform{Origin: srv.URL, Issuer: testIssuer}, bearerHeader(credential))
	require.True(t, result.OK, "reason: %s", result.Reason)

	_, err := newCloudClient(srv.URL).ListInstallations(context.Background(), result.Bearer)
	require.NoError(t, err)

	requests, header, _, _ := seen.snapshot()
	assert.Equal(t, 1, requests)
	assert.Equal(t, credential, header.Get("Authorization"))
}

// TestDecodeCliAuthBlock_Decodes covers the blocks that are the oidc plugin's
// CLI configuration. The schema constrains nothing past type, so this is a
// decode: what it accepts is what the renderer may re-emit from typed fields.
func TestDecodeCliAuthBlock_Decodes(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want cliAuthBlock
	}{
		{
			name: "every field",
			raw:  oidcAuth(t, nil),
			want: cliAuthBlock{
				Type:     "oidc",
				Role:     "cli",
				Issuer:   testIssuer,
				ClientID: "formae-cli",
				Scopes:   "openid profile email offline_access",
			},
		},
		{
			name: "without the optional fields",
			raw:  oidcAuth(t, map[string]any{"clientId": nil, "scopes": nil}),
			want: cliAuthBlock{Type: "oidc", Role: "cli", Issuer: testIssuer},
		},
		{
			name: "another plugin's block decodes; it is the gate that refuses it",
			raw:  rawAuth(t, map[string]any{"type": "basic", "username": "admin", "password": testPassword}),
			want: cliAuthBlock{Type: "basic"},
		},
		{
			name: "an unrecognised field is ignored rather than refused",
			raw:  oidcAuth(t, map[string]any{"audience": "https://api.formae.ai"}),
			want: cliAuthBlock{
				Type:     "oidc",
				Role:     "cli",
				Issuer:   testIssuer,
				ClientID: "formae-cli",
				Scopes:   "openid profile email offline_access",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeCliAuthBlock(tc.raw)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDecodeCliAuthBlock_Refuses covers the blocks that satisfy the schema but
// are not this shape. A block that does not decode is one the renderer could
// only copy blind, so it fails here rather than being read past.
func TestDecodeCliAuthBlock_Refuses(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		// wantField is the field a refusal must name, where one is at fault.
		wantField string
	}{
		{name: "absent", raw: nil},
		{name: "empty bytes", raw: json.RawMessage("")},
		{name: "a JSON array", raw: json.RawMessage(`["oidc"]`)},
		{name: "a JSON string", raw: json.RawMessage(`"oidc"`)},
		{name: "a JSON null", raw: json.RawMessage(`null`)},
		{name: "truncated JSON", raw: json.RawMessage(`{"type":"oidc"`)},
		{name: "clientId as a number", raw: oidcAuth(t, map[string]any{"clientId": 42}), wantField: "clientId"},
		{
			name:      "scopes as an array",
			raw:       oidcAuth(t, map[string]any{"scopes": []string{"openid"}}),
			wantField: "scopes",
		},
		{name: "type as a number", raw: oidcAuth(t, map[string]any{"type": 1}), wantField: "type"},
		{name: "role as a bool", raw: oidcAuth(t, map[string]any{"role": true}), wantField: "role"},
		{name: "issuer as an object", raw: oidcAuth(t, map[string]any{"issuer": map[string]any{"url": testIssuer}}), wantField: "issuer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeCliAuthBlock(tc.raw)

			require.Error(t, err)
			assert.ErrorIs(t, err, errAuthBlockUndecodable)
			assert.Equal(t, cliAuthBlock{}, got, "a block that did not decode yields no fields")
			if tc.wantField != "" {
				assert.Contains(t, err.Error(), tc.wantField, "a refusal names the field at fault")
			}
		})
	}
}

// TestDecodeCliAuthBlock_RefusalNeverRepeatsAValue pins that the decode's own
// error text carries no part of the block: encoding/json quotes input back for
// some targets, and the block may hold a credential for another system.
func TestDecodeCliAuthBlock_RefusalNeverRepeatsAValue(t *testing.T) {
	raw := rawAuth(t, map[string]any{
		"type":     "oidc",
		"role":     "cli",
		"issuer":   testIssuer,
		"clientId": map[string]any{testPassword: testPassword},
	})

	_, err := decodeCliAuthBlock(raw)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), testPassword)
}

// TestBearerFrom covers the one header shape the CLI can actually transmit.
// internal/api attaches the credential by reading the canonical Authorization
// key, so anything stored under another name is a credential this CLI can
// never send — the same failure as none at all, and it fails closed the same
// way (see hasCredential in internal/cli/app).
func TestBearerFrom(t *testing.T) {
	tests := []struct {
		name   string
		hdr    http.Header
		want   string
		wantOK bool
	}{
		{
			name:   "a bearer under the canonical key",
			hdr:    bearerHeader("Bearer " + testToken),
			want:   "Bearer " + testToken,
			wantOK: true,
		},
		{
			name:   "the scheme is case-insensitive per RFC 7235",
			hdr:    bearerHeader("bearer " + testToken),
			want:   "bearer " + testToken,
			wantOK: true,
		},
		{
			name:   "an upper-case scheme",
			hdr:    bearerHeader("BEARER " + testToken),
			want:   "BEARER " + testToken,
			wantOK: true,
		},
		{
			name:   "extra space after the scheme",
			hdr:    bearerHeader("Bearer  " + testToken),
			want:   "Bearer  " + testToken,
			wantOK: true,
		},
		{name: "a nil header", hdr: nil},
		{name: "an empty header", hdr: http.Header{}},
		{name: "an empty value", hdr: bearerHeader("")},
		{name: "a basic credential", hdr: bearerHeader("Basic YWRtaW46cGFzcw==")},
		{name: "a bare token with no scheme", hdr: bearerHeader(testToken)},
		{name: "the scheme with no token", hdr: bearerHeader("Bearer")},
		{name: "the scheme with a trailing space only", hdr: bearerHeader("Bearer ")},
		{name: "the scheme with a blank token", hdr: bearerHeader("Bearer \t ")},
		{name: "a scheme that merely starts with bearer", hdr: bearerHeader("Bearerish " + testToken)},
		{name: "a tab between scheme and token", hdr: bearerHeader("Bearer\t" + testToken)},
		{name: "leading whitespace before the scheme", hdr: bearerHeader(" Bearer " + testToken)},
		{name: "a non-canonical key", hdr: http.Header{"authorization": []string{"Bearer " + testToken}}},
		{name: "another header entirely", hdr: bearerHeaderNamed("X-Formae-Token", "Bearer "+testToken)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bearerFrom(tc.hdr)

			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
