// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

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

func kmsSchemaForNotes() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"Name", "EnableKeyRotation"},
		Hints:  map[string]pkgmodel.FieldHint{"EnableKeyRotation": {HasProviderDefault: true}},
	}
}

func TestComputeSuppressedDriftNotes_AbsorbedSuppressedMovement_Noted(t *testing.T) {
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForNotes(),
	}}}
	fa := &forma_command.FormaCommand{}

	notes := computeSuppressedDriftNotes(mods, forma, fa)

	require.Len(t, notes, 1)
	assert.Equal(t, "prod", notes[0].Stack)
	assert.Equal(t, "AWS::KMS::Key", notes[0].Type)
	assert.Equal(t, "signing-key", notes[0].Label)
	assert.Equal(t, "EnableKeyRotation", notes[0].Path)
	assert.JSONEq(t, `false`, string(notes[0].From))
	assert.JSONEq(t, `true`, string(notes[0].To))
}

func TestComputeSuppressedDriftNotes_UnabsorbedModification_NoNote(t *testing.T) {
	// A modification with no matching forma declaration is unabsorbed: it is
	// rejection territory (displayed in full there), never a note.
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "orphan", Operation: "update",
			OldProperties: json.RawMessage(`{"EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Schema: kmsSchemaForNotes(),
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, &forma_command.FormaCommand{})
	assert.Empty(t, notes)
}

func TestComputeSuppressedDriftNotes_ModificationWithPendingUpdate_NoNote(t *testing.T) {
	// A resource with a generated update keeps its modification unabsorbed
	// (the rejection displays the full drift); the classifier must not
	// produce a note for it.
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k2", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k2"}`),
		Schema:     kmsSchemaForNotes(),
	}}}
	fa := &forma_command.FormaCommand{ResourceUpdates: []resource_update.ResourceUpdate{{
		StackLabel:   "prod",
		Operation:    resource_update.OperationUpdate,
		DesiredState: pkgmodel.Resource{Type: "AWS::KMS::Key", Label: "signing-key"},
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, fa)
	assert.Empty(t, notes)
}

func TestComputeSuppressedDriftNotes_NonUpdateOperations_NeverNoted(t *testing.T) {
	// Create/delete modifications carry no property blobs and are rejection
	// territory; they must fail closed.
	mods := map[string][]datastore.ResourceModification{
		"prod": {
			{Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "create"},
			{Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "delete"},
		},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForNotes(),
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, &forma_command.FormaCommand{})
	assert.Empty(t, notes)
}

func TestComputeSuppressedDriftNotes_MissingPropertyBlobs_NoNote(t *testing.T) {
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update",
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForNotes(),
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, &forma_command.FormaCommand{})
	assert.Empty(t, notes)
}

func TestComputeSuppressedDriftNotes_AliasRename_ResolvesDeclaration(t *testing.T) {
	// The modification is recorded under the old label; the forma renames
	// the resource with alias. When no update was generated for it, the
	// suppressed movement is still classified against the aliased
	// declaration.
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "old-key", Operation: "update",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "new-key", Alias: "old-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForNotes(),
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, &forma_command.FormaCommand{})
	require.Len(t, notes, 1)
	assert.Equal(t, "old-key", notes[0].Label, "the note names the modification's identity")
	assert.Equal(t, "EnableKeyRotation", notes[0].Path)
}

func TestComputeSuppressedDriftNotes_DriftAbsorbedByDeclaration_NoNote(t *testing.T) {
	// The user extracted the drifted values into the forma: the declared
	// field is plan territory (and matches, so no update was generated).
	// Nothing is suppressed.
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		Schema:     kmsSchemaForNotes(),
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, &forma_command.FormaCommand{})
	assert.Empty(t, notes)
}

func TestComputeSuppressedDriftNotes_ConvergenceOnlyUpdate_CoexistingSuppressedMovement_Noted(t *testing.T) {
	// A convergence-only update does not block absorption (the existing
	// filter's rule); suppressed movement on the same resource is still
	// noted.
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key", Operation: "update",
			OldProperties: json.RawMessage(`{"Name": "k", "EnableKeyRotation": false}`),
			Properties:    json.RawMessage(`{"Name": "k", "EnableKeyRotation": true}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "AWS::KMS::Key", Label: "signing-key",
		Properties: json.RawMessage(`{"Name": "k"}`),
		Schema:     kmsSchemaForNotes(),
	}}}
	fa := &forma_command.FormaCommand{ResourceUpdates: []resource_update.ResourceUpdate{{
		StackLabel: "prod",
		Operation:  resource_update.OperationUpdate,
		DesiredState: pkgmodel.Resource{
			Type: "AWS::KMS::Key", Label: "signing-key",
			PatchDocument: json.RawMessage(`[{"op": "replace", "path": "/Secret", "value": "x"}]`),
		},
		ProvenanceRecords: []resource_update.OccurrenceRecord{{
			DestinationPath:  "Secret",
			Class:            resource_update.OccurrenceDeferredUpdate,
			HasStoredWritten: true,
			DesiredIdentity:  resource_update.OccurrenceIdentity{Ksuid: "id"},
			StoredIdentity:   resource_update.OccurrenceIdentity{Ksuid: "id"},
		}},
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, fa)
	require.Len(t, notes, 1)
	assert.Equal(t, "EnableKeyRotation", notes[0].Path)
}

func TestComputeSuppressedDriftNotes_OpaquePath_NoValues(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "MasterSecret"},
		Hints:  map[string]pkgmodel.FieldHint{"MasterSecret": {HasProviderDefault: true, Opaque: true}},
	}
	mods := map[string][]datastore.ResourceModification{
		"prod": {{
			Stack: "prod", Type: "X::Y::Z", Label: "r", Operation: "update",
			OldProperties: json.RawMessage(`{"Name": "n", "MasterSecret": "h1"}`),
			Properties:    json.RawMessage(`{"Name": "n", "MasterSecret": "h2"}`),
		}},
	}
	forma := &pkgmodel.Forma{Resources: []pkgmodel.Resource{{
		Stack: "prod", Type: "X::Y::Z", Label: "r",
		Properties: json.RawMessage(`{"Name": "n"}`),
		Schema:     schema,
	}}}

	notes := computeSuppressedDriftNotes(mods, forma, &forma_command.FormaCommand{})
	require.Len(t, notes, 1)
	assert.True(t, notes[0].Opaque)
	assert.Nil(t, notes[0].From)
	assert.Nil(t, notes[0].To)
	raw, err := json.Marshal(notes[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "h1")
	assert.NotContains(t, string(raw), "h2")
}
