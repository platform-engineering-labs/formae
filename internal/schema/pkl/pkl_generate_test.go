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
	"github.com/tidwall/gjson"

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

// genBoundProps builds a properties blob whose secret-bearing field carries the
// authored $gen envelope extraction emits: the generator named by label and
// stack, one of its outputs, and nothing internal.
func genBoundProps(t *testing.T, generatorLabel, generatorStack string) json.RawMessage {
	t.Helper()
	props := map[string]any{
		"Name": "app/db-password",
		"SecretString": map[string]any{
			"$gen":        true,
			"$label":      generatorLabel,
			"$stack":      generatorStack,
			"$output":     "value",
			"$visibility": "Opaque",
		},
	}
	b, err := json.Marshal(props)
	require.NoError(t, err)
	return b
}

// passwordGenerator builds the stored shape of a PasswordGenerator declaration,
// as ExtractResources emits it alongside the resources bound to it.
func passwordGenerator(label, stack string) json.RawMessage {
	return json.RawMessage(`{
		"Type": "password",
		"Label": "` + label + `",
		"Stack": "` + stack + `",
		"Length": 24,
		"Uppercase": true,
		"Lowercase": true,
		"Digits": true,
		"Symbols": false,
		"ExcludeCharacters": "",
		"RequireEachIncludedType": true
	}`)
}

// generateAndEvaluate writes forma out as PKL source and evaluates that source
// back through the real eval path, returning the emitted text and the forma the
// emitted text evaluates to.
func generateAndEvaluate(t *testing.T, forma *model.Forma) (string, *model.Forma) {
	t.Helper()
	deps, pluginDir := fakeawsDeps(t)
	targetPath := filepath.Join(t.TempDir(), "out.pkl")

	_, err := PKL{}.GenerateSourceCode(forma, targetPath, nil, &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginDir,
		Dependencies:   deps,
	})
	require.NoError(t, err)

	written, err := os.ReadFile(targetPath)
	require.NoError(t, err)

	evaluated, err := PKL{}.Evaluate(targetPath, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err, "emitted PKL must itself evaluate:\n%s", string(written))

	return string(written), evaluated
}

// TestGenerateSourceCode_GeneratorBinding_RoundTripsThroughPkl verifies that a
// resource whose secret-bearing property is bound to a generator is written out
// as PKL source that references the generator's output accessor, and that
// evaluating that source back yields the same $gen envelope naming the same
// generator and output. A resource holding an ordinary recorded value sits in
// the same forma, so a binding invented for it would show.
func TestGenerateSourceCode_GeneratorBinding_RoundTripsThroughPkl(t *testing.T) {
	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "secrets"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{
			{
				Label:      "db",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      "secrets",
				Target:     "aws",
				Properties: genBoundProps(t, "db-password-gen", "secrets"),
			},
			{
				Label:      "api-key",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      "secrets",
				Target:     "aws",
				Properties: []byte(`{"Name":"app/api-key","SecretString":{"$value":"plaintext","$visibility":"Opaque","$strategy":"Update"}}`),
			},
		},
		Generators: []json.RawMessage{passwordGenerator("db-password-gen", "secrets")},
	}

	generated, evaluated := generateAndEvaluate(t, forma)

	// The emitted source declares the generator local it references.
	assert.Contains(t, generated, "local dbPasswordGen = new formae.PasswordGenerator {")
	assert.Contains(t, generated, "secretString = dbPasswordGen.gen.value")
	assert.Equal(t, 1, strings.Count(generated, ".gen."),
		"only the bound property may reference a generator output")

	jsonString := evaluated.ToJSON()
	bound := gjson.Get(jsonString, `Resources.#(Label=="db").Properties.SecretString`)
	assert.True(t, bound.Get("$gen").Bool(), "the re-evaluated property must be a $gen envelope")
	assert.Equal(t, "db-password-gen", bound.Get("$label").String())
	assert.Equal(t, "secrets", bound.Get("$stack").String())
	assert.Equal(t, "value", bound.Get("$output").String())

	// The recorded value beside it stays a recorded value.
	literal := gjson.Get(jsonString, `Resources.#(Label=="api-key").Properties.SecretString`)
	assert.Equal(t, "plaintext", literal.Get("$value").String())
	assert.False(t, literal.Get("$gen").Exists(), "a recorded value must not become a generator reference")
}

// TestGenerateSourceCode_CrossStackGeneratorBinding_RoundTripsThroughPkl
// verifies the same for a resource bound to a generator that lives on another
// stack: the binding names the generator's local, not its stack, so the
// reference survives the round trip with the generator's own stack intact.
func TestGenerateSourceCode_CrossStackGeneratorBinding_RoundTripsThroughPkl(t *testing.T) {
	forma := &model.Forma{
		Stacks:  []model.Stack{{Label: "app"}, {Label: "shared-secrets"}},
		Targets: []model.Target{fakeawsTarget()},
		Resources: []model.Resource{{
			Label:      "db",
			Type:       "FakeAWS::SecretsManager::Secret",
			Stack:      "app",
			Target:     "aws",
			Properties: genBoundProps(t, "db-password-gen", "shared-secrets"),
		}},
		Generators: []json.RawMessage{passwordGenerator("db-password-gen", "shared-secrets")},
	}

	generated, evaluated := generateAndEvaluate(t, forma)

	assert.Contains(t, generated, "local dbPasswordGen = new formae.PasswordGenerator {")
	assert.Contains(t, generated, "stack = sharedSecrets.res")
	assert.Contains(t, generated, "secretString = dbPasswordGen.gen.value")

	jsonString := evaluated.ToJSON()
	bound := gjson.Get(jsonString, `Resources.#(Label=="db").Properties.SecretString`)
	assert.True(t, bound.Get("$gen").Bool())
	assert.Equal(t, "db-password-gen", bound.Get("$label").String())
	assert.Equal(t, "shared-secrets", bound.Get("$stack").String(),
		"the envelope must name the generator's own stack, not the bound resource's")
	assert.Equal(t, "value", bound.Get("$output").String())
}
