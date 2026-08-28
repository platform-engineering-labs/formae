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
	// stub broker signs into the token it mints for it. The issuer and key
	// are standing AWS resources: a static OIDC issuer served from S3 and the
	// key in its published JWKS. The subject is what the stub control plane
	// hands connect, so it is also what the provisioned role's trust policy
	// conditions on.
	oidcEchoAudience = "sts.amazonaws.com"
	oidcEchoIssuer   = "https://e2e-oidc-issuer-942849037363.s3.us-west-2.amazonaws.com"
	oidcEchoKeyID    = "e2e-oidc-key-1"

	// The subject the control plane produces, in the grammar it really uses:
	// the `fai:` namespace, a tenant, and the installation being connected.
	// Connect takes it verbatim into the trust it provisions and the GCP path
	// parses it back apart, so a fixture subject of a made-up shape would
	// exercise a string production never emits.
	oidcEchoTenantID = "2eOidcConnectE2eTenant00001"
	oidcEchoSubject  = "fai:" + oidcEchoTenantID + "/" + connectInstallationID

	// The e2e account, pinned rather than derived: connect is told which
	// account to connect and never infers one from ambient credentials, and
	// the test asserts on the role ARN that produces.
	oidcEchoAccount = "942849037363"

	// The role connect provisions and the echo plugin then assumes. The name
	// carries the suite's own prefix so the pre-cleanup purge reclaims it if
	// a run dies before its teardown; a survivor is converged rather than
	// refused, since it carries the ownership tags connect wrote.
	oidcConnectRoleName = "formae-e2e-oidc-connect-role"

	// The AWS shared-config profile connect provisions with, written by the
	// e2e workflow's credentials step.
	oidcConnectAWSProfile = "e2e-test"

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

// requireCredentialsOutlive holds the exchange's reported expiry to being a
// real one: an RFC3339 instant still in the future. It is the difference
// between an exchange that returned credentials and one that returned a
// document shaped like credentials.
func requireCredentialsOutlive(t *testing.T, expiration string) {
	t.Helper()

	expiresAt, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		t.Fatalf("exchangeExpiration %q is not RFC3339: %v", expiration, err)
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("exchangeExpiration %q is not in the future", expiration)
	}
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

// connectProvisionsTrust drives `formae connect aws` against a stub control
// plane and returns the ARN of the role it provisioned.
//
// This is the half of the chain that establishes trust. Connect reads the
// subject, role name and issuer from the control plane and provisions against
// them verbatim: the OIDC provider for the standing issuer (which already
// exists, so it is validated and reused) and a role whose trust policy pins
// that provider, that subject, and the STS audience. Nothing here is
// hand-provisioned, and nothing is asserted from the CLI's prose — the
// machine document and the registration the stub received are the evidence.
func connectProvisionsTrust(t *testing.T, bin string) string {
	t.Helper()

	stub := StartConnectStub(t, connectSetup{
		CloudSubject:  oidcEchoSubject,
		CloudRoleName: oidcConnectRoleName,
		Issuer:        oidcEchoIssuer,
	})
	configPath := WriteHostedConnectConfig(t, t.TempDir(),
		stagedOidcPluginDir(t, oidcAuthPluginDirEnv), stub.URL)

	// Registered before the run, not after it: a run that provisions the role
	// and then fails to register still leaves the role standing.
	t.Cleanup(func() { DeleteIAMRole(t, oidcConnectRoleName) })

	doc := RunConnect(t, bin, ConnectEnv(stub.URL, oidcEchoIssuer),
		"aws",
		"--config", configPath,
		"--account", oidcEchoAccount,
		"--profile-aws", oidcConnectAWSProfile,
		"--no-input",
	)

	if doc.Phase != "registered" {
		t.Fatalf("connect phase: got %q, want %q", doc.Phase, "registered")
	}
	if doc.Cloud != "aws" {
		t.Errorf("connect cloud: got %q, want %q", doc.Cloud, "aws")
	}
	if doc.Account != oidcEchoAccount {
		t.Errorf("connect account: got %q, want %q", doc.Account, oidcEchoAccount)
	}
	if !strings.HasSuffix(doc.RoleArn, ":role/"+oidcConnectRoleName) {
		t.Fatalf("connect roleArn %q does not name role %q", doc.RoleArn, oidcConnectRoleName)
	}

	// What the control plane was told, rather than what the CLI printed: the
	// registration is the connection, and a run that provisioned without
	// declaring it has not connected the account.
	registrations := stub.Registrations()
	if len(registrations) != 1 {
		t.Fatalf("stub control plane received %d registrations, want 1: %+v", len(registrations), registrations)
	}
	if got := registrations[0]; got.Cloud != "aws" || got.Account != oidcEchoAccount || got.RoleArn != doc.RoleArn {
		t.Errorf("registration: got %+v, want cloud aws, account %s, roleArn %s",
			got, oidcEchoAccount, doc.RoleArn)
	}

	return doc.RoleArn
}

// TestOidcCredential_TokenExchangesForRealCredentials proves the whole chain,
// from establishing trust to spending it: `formae connect` provisions the
// identity provider and the role and registers them; then the agent discovers
// and spawns the stub broker, pairs it with the echo plugin's namespace, the
// signed token the broker mints for the audience the plugin asks for arrives
// inside the plugin's Create, and AWS STS accepts that token against the role
// connect just made.
func TestOidcCredential_TokenExchangesForRealCredentials(t *testing.T) {
	bin := FormaeBinary(t)

	roleArn := connectProvisionsTrust(t, bin)

	agent := StartAgent(t, bin,
		WithPluginDir(stagedOidcPluginDir(t, oidcPluginDirEnv)),
		WithEnv(
			"E2E_OIDC_SUBJECT="+oidcEchoSubject,
			"E2E_OIDC_AUDIENCE="+oidcEchoAudience,
			"E2E_OIDC_ASSUME_ROLE_ARN="+roleArn,
		),
	)
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

	if got := echoOutput(t, echo, "exchangeError"); got != "" {
		t.Fatalf("exchangeError: got %q, want empty", got)
	}
	if got := echoOutput(t, echo, "exchangeIdentity"); !strings.Contains(got, oidcConnectRoleName) {
		t.Errorf("exchangeIdentity %q does not name role %q", got, oidcConnectRoleName)
	}
	requireCredentialsOutlive(t, echoOutput(t, echo, "exchangeExpiration"))
}

// TestOidcCredential_NoBrokerFailsClosed proves a plugin whose namespace has
// no broker paired gets an error instead of a token: the echo plugin is
// staged without the stub broker beside it, and the failure it records names
// both the missing pairing and its own namespace.
func TestOidcCredential_NoBrokerFailsClosed(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin,
		WithPluginDir(stagedOidcPluginDir(t, oidcPluginDirNoBrokerEnv)),
		// An audience, but no exchange coordinates: the plugin gets far enough
		// to ask for a token, which is where this test's failure has to happen.
		WithEnv("E2E_OIDC_AUDIENCE="+oidcEchoAudience),
	)

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
