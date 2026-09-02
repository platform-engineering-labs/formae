// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package pkl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/schema"
	model "github.com/platform-engineering-labs/formae/pkg/model"
)

// A keypair generator extracted to PKL re-evaluates to the same spec: type,
// bits, and cadence all survive, so an extract can be re-applied without the
// generator silently changing shape.
func TestGenerateSourceCode_KeyPairGenerator_RoundTrips(t *testing.T) {
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
				"Type": "keypair",
				"Label": "id-key",
				"Stack": "default",
				"Rotation": {"EverySeconds": 86400},
				"Bits": 3072
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

	assert.Contains(t, generated, "new formae.KeyPairGenerator {",
		"the emitted generator must keep its kind, not fall into the unknown-type skip")
	assert.Contains(t, generated, "bits = 3072")

	evaluated, err := PKL{}.Evaluate(targetPath, model.CommandApply, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err, "emitted PKL must itself evaluate")

	require.Len(t, evaluated.Generators, 1)
	var round struct {
		Type     string                      `json:"Type"`
		Bits     int                         `json:"Bits"`
		Rotation *struct{ EverySeconds int } `json:"Rotation"`
	}
	require.NoError(t, json.Unmarshal(evaluated.Generators[0], &round))
	assert.Equal(t, "keypair", round.Type)
	assert.Equal(t, 3072, round.Bits)
	require.NotNil(t, round.Rotation, "cadence must survive the round trip")
	assert.Equal(t, 86400, round.Rotation.EverySeconds)
}
