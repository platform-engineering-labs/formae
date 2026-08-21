// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// The audience the echo plugin asks for in Create, and the token the stub
	// broker mints for it.
	oidcEchoAudience  = "sts.amazonaws.com"
	oidcEchoStubToken = "e2e-stub-jwt." + oidcEchoAudience

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

// TestOidcCredential_TokenReachesPlugin proves the whole chain: the agent
// discovers and spawns the stub broker, pairs it with the echo plugin's
// namespace, and the token the broker mints for the audience the plugin asks
// for arrives inside the plugin's Create.
func TestOidcCredential_TokenReachesPlugin(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin, WithPluginDir(stagedOidcPluginDir(t, oidcPluginDirEnv)))
	agent.WaitForOidcBroker(t, oidcEchoNamespace, 60*time.Second)

	echo := applyOidcEchoFixture(t, bin, agent)

	if got := echoOutput(t, echo, "token"); got != oidcEchoStubToken {
		t.Errorf("token: got %q, want %q", got, oidcEchoStubToken)
	}
	if got := echoOutput(t, echo, "tokenError"); got != "" {
		t.Errorf("tokenError: got %q, want empty", got)
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
