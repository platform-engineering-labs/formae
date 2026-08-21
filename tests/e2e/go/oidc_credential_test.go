// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// The audience the echo plugin asks for in Create, and the claims the
	// stub broker signs into the token it mints for it. These name standing
	// AWS resources: the static OIDC issuer registered as an IAM identity
	// provider, the key in its published JWKS, and the subject and role its
	// trust policy conditions on.
	oidcEchoAudience = "sts.amazonaws.com"
	oidcEchoIssuer   = "https://e2e-oidc-issuer-942849037363.s3.us-west-2.amazonaws.com"
	oidcEchoKeyID    = "e2e-oidc-key-1"
	oidcEchoSubject  = "e2e-oidc-subject"
	oidcEchoRoleName = "e2e-oidc-assume-role"

	oidcEchoStackQuery = "stack:e2e-oidc-echo"
	oidcEchoLabel      = "e2e-oidc-token"

	// The namespace the stub broker announces and the echo plugin serves,
	// as the coordinator keys it (uppercased on ingest).
	oidcEchoNamespace = "OIDCECHO"

	// Plugin trees staged by `make test-e2e`: one with the stub broker beside
	// the echo plugin, one with the echo plugin alone.
	oidcPluginDirEnv         = "E2E_OIDC_PLUGIN_DIR"
	oidcPluginDirNoBrokerEnv = "E2E_OIDC_PLUGIN_DIR_NO_BROKER"
)

// stagedOidcPluginDir returns the staged plugin tree named by the given
// environment variable. `make test-e2e` builds both fixture binaries and
// stages them; running the suite by hand without it leaves nothing to
// discover, so say so rather than failing later on a missing plugin.
func stagedOidcPluginDir(t *testing.T, envVar string) string {
	t.Helper()

	dir := os.Getenv(envVar)
	if dir == "" {
		t.Fatalf("%s is not set — run these tests via `make test-e2e`, which builds and stages the oidc fixtures", envVar)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s=%q is not readable: %v", envVar, dir, err)
	}
	return dir
}

// applyOidcEchoFixture applies the echo fixture against the given agent and
// returns the created resource as inventory reports it.
func applyOidcEchoFixture(t *testing.T, bin string, agent *Agent) Resource {
	t.Helper()

	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())
	fixture := filepath.Join(fixturesDir(t), "oidc_echo.pkl")

	cmdID := cli.Apply(t, "reconcile", fixture)
	RequireCommandSuccess(t, cli.WaitForCommand(t, cmdID, 2*time.Minute))

	resources := cli.Inventory(t, "--query", oidcEchoStackQuery)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource in %s, got %d", oidcEchoStackQuery, len(resources))
	}
	return RequireResource(t, resources, oidcEchoLabel)
}

// echoOutput reads one of the echo resource's read-only outputs. token and
// tokenError are not schema fields, so the agent files them under
// ReadOnlyProperties.
func echoOutput(t *testing.T, r Resource, key string) string {
	t.Helper()

	value, ok := r.ReadOnlyProperties[key]
	if !ok {
		t.Fatalf("resource %s has no read-only property %q (read-only properties: %v)", r.Label, key, r.ReadOnlyProperties)
	}
	str, ok := value.(string)
	if !ok {
		t.Fatalf("resource %s read-only property %q: expected string, got %T: %v", r.Label, key, value, value)
	}
	return str
}

// decodeJWTSegment base64url-decodes one segment of a compact JWS and parses
// it as a JSON object.
func decodeJWTSegment(t *testing.T, name, segment string) map[string]any {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decoding token %s %q: %v", name, segment, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("parsing token %s %q: %v", name, raw, err)
	}
	return decoded
}

// decodeJWT splits a compact JWS into its three segments and returns the
// decoded header and claims. The signature is not verified here: STS
// verifying it against the issuer's JWKS is what the test asserts on.
func decodeJWT(t *testing.T, token string) (header, claims map[string]any) {
	t.Helper()

	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("token %q: expected 3 dot-separated segments, got %d", token, len(segments))
	}
	return decodeJWTSegment(t, "header", segments[0]), decodeJWTSegment(t, "claims", segments[1])
}

// jwtString reads a string-valued entry out of a decoded token document.
func jwtString(t *testing.T, doc map[string]any, key string) string {
	t.Helper()

	value, ok := doc[key]
	if !ok {
		t.Fatalf("token document has no %q (document: %v)", key, doc)
	}
	str, ok := value.(string)
	if !ok {
		t.Fatalf("token document %q: expected string, got %T: %v", key, value, value)
	}
	return str
}

// TestOidcCredential_TokenExchangesForRealCredentials proves the whole chain:
// the agent discovers and spawns the stub broker, pairs it with the echo
// plugin's namespace, the signed token the broker mints for the audience the
// plugin asks for arrives inside the plugin's Create, and AWS STS accepts
// that token and exchanges it for credentials on the standing role.
func TestOidcCredential_TokenExchangesForRealCredentials(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithPluginDir(stagedOidcPluginDir(t, oidcPluginDirEnv)))
	agent.WaitForOidcBroker(t, oidcEchoNamespace, 60*time.Second)

	echo := applyOidcEchoFixture(t, bin, agent)

	if got := echoOutput(t, echo, "tokenError"); got != "" {
		t.Fatalf("tokenError: got %q, want empty", got)
	}

	header, claims := decodeJWT(t, echoOutput(t, echo, "token"))
	for _, check := range []struct {
		doc      map[string]any
		key      string
		expected string
	}{
		{header, "alg", "RS256"},
		{header, "kid", oidcEchoKeyID},
		{claims, "iss", oidcEchoIssuer},
		{claims, "sub", oidcEchoSubject},
		{claims, "aud", oidcEchoAudience},
	} {
		if got := jwtString(t, check.doc, check.key); got != check.expected {
			t.Errorf("token %s: got %q, want %q", check.key, got, check.expected)
		}
	}

	if got := echoOutput(t, echo, "stsError"); got != "" {
		t.Fatalf("stsError: got %q, want empty", got)
	}
	if got := echoOutput(t, echo, "stsAssumedRoleArn"); !strings.Contains(got, oidcEchoRoleName) {
		t.Errorf("stsAssumedRoleArn %q does not name role %q", got, oidcEchoRoleName)
	}

	expiration := echoOutput(t, echo, "stsExpiration")
	expiresAt, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		t.Fatalf("stsExpiration %q is not RFC3339: %v", expiration, err)
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("stsExpiration %q is not in the future", expiration)
	}
}

// TestOidcCredential_NoBrokerFailsClosed proves a plugin whose namespace has
// no broker paired gets an error instead of a token: the echo plugin is
// staged without the stub broker beside it, and the failure it records names
// both the missing pairing and its own namespace.
func TestOidcCredential_NoBrokerFailsClosed(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithPluginDir(stagedOidcPluginDir(t, oidcPluginDirNoBrokerEnv)))

	echo := applyOidcEchoFixture(t, bin, agent)

	if got := echoOutput(t, echo, "token"); got != "" {
		t.Errorf("token: got %q, want empty with no broker paired", got)
	}
	tokenError := echoOutput(t, echo, "tokenError")
	for _, want := range []string{"no oidc-credential broker paired", "OidcEcho"} {
		if !strings.Contains(tokenError, want) {
			t.Errorf("tokenError %q does not contain %q", tokenError, want)
		}
	}
}
