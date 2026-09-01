// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package drift

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func kmsSchemaForDrift() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}
}

func driftWitnesses(modsByStack map[string][]datastore.ResourceModification) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, mods := range modsByStack {
		for _, mod := range mods {
			if mod.Ksuid != "" && len(mod.OldProperties) > 0 {
				out[mod.Ksuid] = mod.OldProperties
			}
		}
	}
	return out
}

func TestWitnessedMovedModifications_WitnessedMovement_IsDrift(t *testing.T) {
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update", Ksuid: "k1",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForDrift(),
	}}}

	moved := WitnessedMovedModifications(mods["prod"], driftWitnesses(mods), forma, &forma_command.FormaCommand{})
	require.Len(t, moved, 1)
	assert.Equal(t, "signing-key", moved[0].Label)
}

func TestWitnessedMovedModifications_NoWitness_Tolerated(t *testing.T) {
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update", Ksuid: "k1",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForDrift(),
	}}}

	moved := WitnessedMovedModifications(mods["prod"], map[string]json.RawMessage{}, forma, &forma_command.FormaCommand{})
	assert.Empty(t, moved, "no write witness means the movement is the infrastructure's business")
}

func TestWitnessedMovedModifications_AlreadyUnabsorbed_NotDoubled(t *testing.T) {
	// A modification the legacy filter already rejects is not also returned
	// as witnessed movement; the caller unions the two sets.
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "orphan", Operation: "update", Ksuid: "k1",
			OldProperties: json.RawMessage(`{"EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Schema: kmsSchemaForDrift(),
	}}}

	moved := WitnessedMovedModifications(mods["prod"], driftWitnesses(mods), forma, &forma_command.FormaCommand{})
	assert.Empty(t, moved)
}

func TestWitnessedMovedModifications_PendingUpdate_LeftToLegacyFilter(t *testing.T) {
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update", Ksuid: "k1",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k2", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k2"}`),
		Schema:     kmsSchemaForDrift(),
	}}}
	fa := &forma_command.FormaCommand{ResourceUpdates: []resource_update.ResourceUpdate{{
		StackLabel:   "prod",
		Operation:    resource_update.OperationUpdate,
		DesiredState: pkgmodel.Resource{Type: "AWS::KMS::Key", Label: "signing-key"},
	}}}

	moved := WitnessedMovedModifications(mods["prod"], driftWitnesses(mods), forma, fa)
	assert.Empty(t, moved, "a resource with a pending update is already unabsorbed; the legacy filter owns it")
}

func TestAssertWitnessesIntoForma_AssertsOnlyMatchedResources_AndCopies(t *testing.T) {
	original := &pkgmodel.Forma{Resources: []pkgmodel.Resource{
		{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
			Properties: json.RawMessage(`{"Name": "k"}`),
			Schema:     kmsSchemaForDrift(),
		},
		{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "untouched",
			Properties: json.RawMessage(`{"Name": "u"}`),
			Schema:     kmsSchemaForDrift(),
		},
	}}
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update", Ksuid: "k1",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}

	asserted := AssertWitnessesIntoForma(original, mods, driftWitnesses(mods))

	require.NotSame(t, original, asserted)
	assert.JSONEq(t, `{"Name": "k", "EnableKeyRotation": false}`, string(asserted.Resources[0].Properties),
		"the witnessed value is asserted so a forced plan reverts to it")
	assert.JSONEq(t, `{"Name": "u"}`, string(asserted.Resources[1].Properties))
	assert.JSONEq(t, `{"Name": "k"}`, string(original.Resources[0].Properties), "the caller's forma is not mutated")
}
