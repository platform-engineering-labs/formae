// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connection

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These exercise the command as a consumer meets it: real flag parsing, real
// selection, real rendering. The unit tests above pin the decision; these pin
// the bytes, which is the part another repo actually depends on. Slice 1's argv
// defect got in because the consumer's stub asserted our belief about the CLI
// rather than the CLI itself.
//
// Every shape here is reachable without an auth plugin. A hosted success needs
// one, so it belongs with the integration evidence gathered against a real
// installation rather than being faked here.

const classicProfile = `amends "formae:/Config.pkl"

cli {
    connection = new Classic {
        url = "http://localhost"
        port = 49684
    }
}
`

func hostedProfile(issuer string) string {
	return `amends "formae:/Config.pkl"

cli {
    connection = new Hosted {
        endpoint = "https://cloud.formae.ai"
        installation = "3HzFPXfPDGhwLJJVtaHbmFs6vLa"
        auth = new Dynamic {
            type = "oidc"
            role = "cli"
            issuer = "` + issuer + `"
        }
    }
}
`
}

// seed points the store at a temp dir holding the given profiles, and makes
// active the active pointer.
func seed(t *testing.T, active string, profiles map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if len(profiles) > 0 {
		if err := os.MkdirAll(filepath.Join(root, "profiles"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Isolating the config directory alone is not enough. Plugin discovery reads
	// pluginDir, which defaults to a path under the caller's home, so a test whose
	// premise is "no auth plugin is installed" is answered by whatever the
	// developer running it happens to have installed: it passes in CI and fails on
	// any machine carrying the oidc plugin, for a reason unrelated to the
	// behaviour under test.
	empty := filepath.Join(root, "empty-plugins")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range profiles {
		body += "\npluginDir = " + strconv.Quote(empty) + "\n"
		if err := os.WriteFile(filepath.Join(root, "profiles", name+".pkl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if active != "" {
		if err := os.WriteFile(filepath.Join(root, "active"), []byte(active+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FORMAE_CONFIG_DIR", root)
	return root
}

// run executes the command exactly as the CLI would and returns stdout.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := ConnectionCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}

func machine(args ...string) []string {
	return append(args, "--output-consumer", "machine", "--output-schema", "json")
}

func decode(t *testing.T, out string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not json: %v\n%s", err, out)
	}
	return got
}

func TestContractClassic(t *testing.T) {
	seed(t, "dev", map[string]string{"dev": classicProfile})

	out, err := run(t, machine("resolve")...)
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}

	got := decode(t, out)
	if got["schemaVersion"] != float64(1) || got["profile"] != "dev" {
		t.Fatalf("envelope: %s", out)
	}
	conn := got["connection"].(map[string]any)
	if conn["mode"] != "classic" || conn["url"] != "http://localhost" || conn["port"] != float64(49684) {
		t.Fatalf("connection: %s", out)
	}
	if _, present := got["credential"]; present {
		t.Fatalf("classic must carry no credential key at all: %s", out)
	}
}

func TestContractUntrustedIssuer(t *testing.T) {
	seed(t, "prod", map[string]string{"prod": hostedProfile("https://auth.evil.example")})

	out, _ := run(t, machine("resolve")...)

	got := decode(t, out)
	if got["code"] != "untrusted_issuer" {
		t.Fatalf("code: %s", out)
	}
	if strings.Contains(out, "evil.example") {
		t.Logf("note: the refusal names the rejected issuer: %s", out)
	}
}

func TestContractAmbiguousProfile(t *testing.T) {
	seed(t, "prod", map[string]string{
		"prod":    hostedProfile("https://auth.formae.ai"),
		"staging": classicProfile,
	})

	out, _ := run(t, machine("resolve")...)

	got := decode(t, out)
	if got["code"] != "ambiguous_profile" {
		t.Fatalf("code: %s", out)
	}
	details, ok := got["details"].(map[string]any)
	if !ok {
		t.Fatalf("ambiguity must carry the candidates so a consumer can offer them: %s", out)
	}
	candidates, ok := details["candidates"].([]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("candidates: %s", out)
	}
	if details["active"] != "prod" {
		t.Fatalf("the active profile must be marked: %s", out)
	}
}

// Naming the profile settles the choice, so the same store resolves.
func TestContractExplicitProfileIsNeverAmbiguous(t *testing.T) {
	seed(t, "prod", map[string]string{
		"prod":    classicProfile,
		"staging": classicProfile,
	})

	out, err := run(t, machine("resolve", "--profile", "staging")...)
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if got := decode(t, out); got["profile"] != "staging" {
		t.Fatalf("the named profile is the effective one: %s", out)
	}
}

// A hosted profile this build trusts, with no auth plugin installed, cannot be
// authenticated — and a hosted connection that cannot be authenticated is not a
// usable connection, so resolution fails rather than reporting a clean endpoint.
func TestContractAuthFailure(t *testing.T) {
	seed(t, "prod", map[string]string{"prod": hostedProfile("https://auth.formae.ai")})

	out, err := run(t, machine("resolve", "--profile", "prod")...)
	if err == nil {
		t.Fatalf("expected a failure without an auth plugin: %s", redactCredentials(out))
	}
	if got := decode(t, out); got["code"] != "auth_failed" {
		t.Fatalf("code: %s", redactCredentials(out))
	}
	if strings.Contains(out, "Bearer") {
		t.Fatal("a failure must never carry a credential")
	}
}

func TestContractUnknownProfile(t *testing.T) {
	seed(t, "dev", map[string]string{"dev": classicProfile})

	out, err := run(t, machine("resolve", "--profile", "nope")...)
	if err == nil {
		t.Fatalf("expected a failure: %s", out)
	}
	got := decode(t, out)
	if got["code"] != "internal" {
		t.Fatalf("an undeclared failure is reported as internal: %s", out)
	}
	if got["schemaVersion"] != float64(1) {
		t.Fatalf("every envelope carries the schema version: %s", out)
	}
}

// An explicit config file is a selection in its own right: it is not a profile,
// so it reports no name and can never be ambiguous.
func TestContractExplicitConfigFile(t *testing.T) {
	root := seed(t, "prod", map[string]string{
		"prod":    classicProfile,
		"staging": classicProfile,
	})

	out, err := run(t, machine("resolve", "--config", filepath.Join(root, "profiles", "staging.pkl"))...)
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if got := decode(t, out); got["profile"] != "" {
		t.Fatalf("a config file must not borrow the active profile's name: %s", out)
	}
}

func TestContractYAML(t *testing.T) {
	seed(t, "dev", map[string]string{"dev": classicProfile})

	out, err := run(t, "resolve", "--output-consumer", "machine", "--output-schema", "yaml")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if !strings.Contains(out, "mode: classic") || !strings.Contains(out, "schemaVersion: 1") {
		t.Fatalf("yaml: %s", out)
	}
}

// Human output answers "am I hosted, which installation, do I have a session"
// without being a way to print a token into a scrollback.
func TestContractHumanOutputCarriesNoJSON(t *testing.T) {
	seed(t, "dev", map[string]string{"dev": classicProfile})

	out, err := run(t, "resolve")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if strings.Contains(out, "schemaVersion") || strings.Contains(out, "{") {
		t.Fatalf("human output is not the machine document: %s", out)
	}
	if !strings.Contains(out, "profile: dev") {
		t.Fatalf("human output: %s", out)
	}
}

// Argv the command cannot parse fails before the flags that say how to render a
// failure are established, so it exits non-zero without an envelope. Pinned so
// the limit is a decision rather than a surprise: a consumer that cannot parse
// what it got must report the exit status rather than guess.
func TestContractArgvErrorsAreNotEnvelopes(t *testing.T) {
	seed(t, "dev", map[string]string{"dev": classicProfile})

	for _, args := range [][]string{
		machine("resolve", "unexpected-arg"),
		{"resolve", "--output-consumer", "sideways"},
		machine("resolve", "--no-such-flag"),
	} {
		out, err := run(t, args...)
		if err == nil {
			t.Fatalf("%v should fail: %s", args, out)
		}
		if strings.Contains(out, `"schemaVersion"`) {
			t.Fatalf("%v produced an envelope, which the docs say it cannot: %s", args, out)
		}
	}
}
