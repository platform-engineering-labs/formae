//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hostedProfilePkl = `amends "formae:/Config.pkl"

cli {
    connection = new Hosted {
        endpoint = "https://cloud.formae.ai"
        installation = "3f2b8c14-0000-4000-8000-000000000000"
        auth = new Dynamic { type = "oidc" }
    }
}
`

// seedProfileBodies points the store at a temp config dir holding profiles
// with the given contents, and makes active the active pointer.
func seedProfileBodies(t *testing.T, active string, bodies map[string]string) {
	t.Helper()
	cfgDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cfgDir, "profiles"), 0o755))
	for name, body := range bodies {
		require.NoError(t, os.WriteFile(
			filepath.Join(cfgDir, "profiles", name+".pkl"), []byte(body), 0o600))
	}
	if active != "" {
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "active"), []byte(active+"\n"), 0o600))
	}
	t.Setenv("FORMAE_CONFIG_DIR", cfgDir)
}

func runShow(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newShowCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())
	return out.String()
}

func TestShowMachineJSONEmitsTheTaggedConnection(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "prod", "--output-consumer", "machine", "--output-schema", "json")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.EqualValues(t, 1, got["schemaVersion"])
	conn := got["cli"].(map[string]any)["connection"].(map[string]any)
	assert.Equal(t, "hosted", conn["mode"])
	assert.Equal(t, "3f2b8c14-0000-4000-8000-000000000000", conn["installation"])
	assert.NotContains(t, out, "\x1b[", "machine output carries no ANSI")
}

func TestShowMachineYAMLIsAccepted(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "prod", "--output-consumer", "machine", "--output-schema", "yaml")

	assert.Contains(t, out, "schemaVersion: 1")
	assert.Contains(t, out, "mode: hosted")
}

func TestShowWithNoArgumentUsesTheActiveProfile(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "--output-consumer", "machine", "--output-schema", "json")

	assert.Contains(t, out, `"profile":"prod"`)
}

func TestShowHumanRendersSections(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	out := runShow(t, "prod")

	assert.Contains(t, out, "Connection")
	assert.Contains(t, out, "cloud.formae.ai")
	assert.NotContains(t, out, "schemaVersion", "the schema version is machine bookkeeping")
	assert.NotContains(t, out, "{", "human output is not a serialisation format")
}

// A secret Pkl resolves from the environment never existed in the profile
// file, so redaction has to act on the resolved value rather than the text.
func TestShowRedactsASecretResolvedFromTheEnvironment(t *testing.T) {
	const profile = `amends "formae:/Config.pkl"

agent {
    datastore {
        datastoreType = "postgres"
        postgres {
            host = "db.internal"
            password = read("env:FORMAE_TEST_DB_PASSWORD")
        }
    }
}
`
	t.Setenv("FORMAE_TEST_DB_PASSWORD", "hunter2")
	seedProfileBodies(t, "prod", map[string]string{"prod": profile})

	out := runShow(t, "prod", "--output-consumer", "machine", "--output-schema", "json")

	assert.NotContains(t, out, "hunter2")
	assert.Contains(t, out, "db.internal")
}

func TestShowRejectsUnknownProfile(t *testing.T) {
	seedProfileBodies(t, "prod", map[string]string{"prod": hostedProfilePkl})

	cmd := newShowCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"nope"})
	err := cmd.Execute()

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "nope"), "the error names the profile asked for")
}
