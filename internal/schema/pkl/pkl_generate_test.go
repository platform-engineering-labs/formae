// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package pkl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/schema"
	model "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestGenerateSourceCode_HashedSecretCount_ConsistencyCheck verifies that the
// HashedSecretCount returned by GenerateSourceCode equals both the known number
// of hashed opaque fields in the forma (2) and the number of sentinel comment
// occurrences actually present in the written file. This three-way consistency
// check ensures the count cannot diverge from what gen.pkl emits.
func TestGenerateSourceCode_HashedSecretCount_ConsistencyCheck(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)
	hex := "a3f5b2c8d9e1f04712345678abcdef0123456789abcdef0123456789abcdef01"

	// Build a forma with 2 hashed opaque fields (two separate resources) plus
	// one non-hashed resource, so the expected count is exactly 2.
	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{
			{
				Label:      "hashed-secret-1",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      "default",
				Target:     "aws",
				Properties: hashedOpaqueProps(t, hex),
			},
			{
				Label:      "hashed-secret-2",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      "default",
				Target:     "aws",
				Properties: hashedOpaqueProps(t, hex),
			},
		},
	}

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "out.pkl")

	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	res, err := PKL{}.GenerateSourceCode(forma, targetPath, nil, options)
	require.NoError(t, err)

	const expectedCount = 2

	// Assertion 1: the result field equals the known count.
	assert.Equal(t, expectedCount, res.HashedSecretCount,
		"HashedSecretCount must equal the number of hashed opaque fields in the forma")

	// Assertion 2: the written file contains exactly that many sentinel comments.
	written, err := os.ReadFile(res.TargetPath)
	require.NoError(t, err)
	fileCount := strings.Count(string(written), hashedSecretSentinel)
	assert.Equal(t, expectedCount, fileCount,
		"the written file must contain exactly %d sentinel comment(s)", expectedCount)

	// Assertion 3: the result field equals the file count — they must agree.
	assert.Equal(t, res.HashedSecretCount, fileCount,
		"HashedSecretCount must equal the sentinel comment count in the written file")
}

// TestGenerateSourceCode_HashedSecretCount_ZeroForNonHashed verifies that a
// forma with no hashed opaque fields produces HashedSecretCount == 0.
func TestGenerateSourceCode_HashedSecretCount_ZeroForNonHashed(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "plain-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: []byte(`{"SecretString":{"$value":"plaintext","$visibility":"Opaque","$strategy":"Update"}}`),
		}},
	}

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "out.pkl")

	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	res, err := PKL{}.GenerateSourceCode(forma, targetPath, nil, options)
	require.NoError(t, err)

	assert.Equal(t, 0, res.HashedSecretCount,
		"HashedSecretCount must be 0 when no hashed opaque fields are present")
}

// TestGenerateSourceCode_Generator_RoundTrips verifies that a forma carrying
// a standalone generator (as it would when re-serialized from stored state,
// the same way standalone policies already are) round-trips through
// GenerateSourceCode into a .pkl file that declares an equivalent
// formae.PasswordGenerator referencing its stack, and that the emitted file
// itself evaluates.
func TestGenerateSourceCode_Generator_RoundTrips(t *testing.T) {
	deps, pluginDir := fakeawsDeps(t)

	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "default"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "plain-secret",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "default",
			Target:     "aws",
			Properties: []byte(`{"SecretString":{"$value":"plaintext","$visibility":"Opaque","$strategy":"Update"}}`),
		}},
		Generators: []json.RawMessage{
			[]byte(`{
				"Type": "password",
				"Label": "db-password",
				"Stack": "default",
				"Length": 24,
				"Uppercase": true,
				"Lowercase": true,
				"Digits": true,
				"Symbols": false,
				"ExcludeCharacters": "oO0",
				"RequireEachIncludedType": true
			}`),
		},
	}

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "out.pkl")

	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	}

	_, err := PKL{}.GenerateSourceCode(forma, targetPath, nil, options)
	require.NoError(t, err)

	written, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	generated := string(written)

	assert.Contains(t, generated, "new formae.PasswordGenerator {")
	assert.Contains(t, generated, `label = "db-password"`)
	assert.Contains(t, generated, "stack = default.res")
	assert.Contains(t, generated, "length = 24")
	assert.Contains(t, generated, `excludeCharacters = "oO0"`)

	_, err = PKL{}.Evaluate(targetPath, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err, "emitted PKL must itself evaluate")
}
