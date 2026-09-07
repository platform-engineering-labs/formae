// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func genBoundCreate(generatorKsuid string) ResourceUpdate {
	return ResourceUpdate{
		Operation: OperationCreate,
		DesiredState: pkgmodel.Resource{
			Label: "db", Type: "Test::Secret::Entry", Stack: "s",
			Properties: json.RawMessage(`{
				"Name": "db",
				"SecretString": {"$gen": true, "$generator": "` + generatorKsuid + `", "$output": "value", "$visibility": "Opaque"}
			}`),
		},
	}
}

// The drawn value reaches the destination inside its envelope, with the
// opaque marker intact — the envelope is what the persist path reads to
// decide the value is hashed at rest.
func TestResolveGeneratorValue_WritesInsideTheEnvelope(t *testing.T) {
	ru := genBoundCreate("gen-a")

	require.NoError(t, ru.ResolveGeneratorValue("gen-a", map[string]string{"value": "drawn-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile))

	envelope := gjson.GetBytes(ru.DesiredState.Properties, "SecretString")
	require.True(t, envelope.IsObject(), "the envelope must not be replaced by a bare scalar")
	assert.Equal(t, "drawn-credential", envelope.Get("$value").String())
	assert.Equal(t, "Opaque", envelope.Get("$visibility").String())
	assert.True(t, envelope.Get("$gen").Bool())
}

// A destination bound to a different generator is left alone.
func TestResolveGeneratorValue_LeavesAnotherGeneratorsDestinationAlone(t *testing.T) {
	ru := genBoundCreate("gen-a")

	require.NoError(t, ru.ResolveGeneratorValue("gen-b", map[string]string{"value": "drawn-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile))

	assert.False(t, gjson.GetBytes(ru.DesiredState.Properties, "SecretString.$value").Exists(),
		"only destinations naming the generator that drew may receive the value")
}

// A draw that is happening reaches every destination naming its generator,
// including one whose occurrence classified stable. Stability decides whether
// the generator draws at all; narrowing delivery by it is what leaves two
// consumers of one credential holding different values, each apply repairing
// one and breaking the other.
func TestResolveGeneratorValue_ReachesEveryDestinationIncludingAStableOne(t *testing.T) {
	ru := ResourceUpdate{
		Operation: OperationUpdate,
		DesiredState: pkgmodel.Resource{
			Label: "db", Type: "Test::Secret::Entry", Stack: "s",
			Properties: json.RawMessage(`{
				"Stable": {"$gen": true, "$generator": "gen-a", "$output": "value", "$visibility": "Opaque"},
				"Fresh":  {"$gen": true, "$generator": "gen-a", "$output": "value", "$visibility": "Opaque"}
			}`),
		},
		ProvenanceRecords: []OccurrenceRecord{{
			DestinationPath: "Stable",
			DesiredIdentity: OccurrenceIdentity{
				Kind: OccurrenceKindGenerator, Ksuid: "gen-a", PropertyPath: "value",
			},
			Class: OccurrenceStable,
		}},
	}

	require.NoError(t, ru.ResolveGeneratorValue("gen-a", map[string]string{"value": "drawn-credential"}, "generation-1", pkgmodel.FormaApplyModeReconcile))

	assert.Equal(t, "drawn-credential", gjson.GetBytes(ru.DesiredState.Properties, "Stable.$value").String(),
		"a stable destination receives the draw its siblings receive")
	assert.Equal(t, "drawn-credential", gjson.GetBytes(ru.DesiredState.Properties, "Fresh.$value").String(),
		"a destination that still needs a value receives the draw")
}

// Delivering a drawn value mutates DesiredState.Properties, so the derived
// patch must be re-derived under the SAME apply-mode semantics the command
// was planned with. Under reconcile an EntitySet member dropped from the
// desired state produces a remove op; under patch mode absence means "leave
// unchanged" and no remove may appear.
func TestResolveGeneratorValue_RegeneratesUnderCommandMode(t *testing.T) {
	newUpdate := func() ResourceUpdate {
		schema := pkgmodel.Schema{
			Identifier: "Name",
			Fields:     []string{"Name", "SecretString", "Tags"},
			Hints: map[string]pkgmodel.FieldHint{
				"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
			},
		}
		return ResourceUpdate{
			Operation: OperationUpdate,
			PriorState: pkgmodel.Resource{
				Label: "db", Type: "Test::Secret::Entry", Schema: schema,
				Properties: json.RawMessage(`{"Name": "db", "SecretString": "old", "Tags": [{"Key": "env", "Value": "prod"}, {"Key": "legacy", "Value": "true"}]}`),
			},
			DesiredState: pkgmodel.Resource{
				Label: "db", Type: "Test::Secret::Entry", Schema: schema,
				Properties: json.RawMessage(`{"Name": "db", "SecretString": {"$gen": true, "$generator": "gen-a", "$output": "value"}, "Tags": [{"Key": "env", "Value": "prod"}]}`),
			},
		}
	}

	reconcile := newUpdate()
	require.NoError(t, reconcile.ResolveGeneratorValue("gen-a", map[string]string{"value": "drawn"}, "generation-1", pkgmodel.FormaApplyModeReconcile))
	assert.Contains(t, string(reconcile.DesiredState.PatchDocument), "remove",
		"reconcile regeneration must keep the remove op for the dropped Tags member")
	assert.Contains(t, string(reconcile.DesiredState.PatchDocument), "Tags")

	patchMode := newUpdate()
	require.NoError(t, patchMode.ResolveGeneratorValue("gen-a", map[string]string{"value": "drawn"}, "generation-1", pkgmodel.FormaApplyModePatch))
	assert.NotContains(t, string(patchMode.DesiredState.PatchDocument), "remove",
		"patch-mode regeneration must leave the undeclared Tags member unchanged")
}

// A resource holding no destination for this generator is a no-op: nothing is
// written and no patch is re-derived.
func TestResolveGeneratorValue_WithNoDestinationChangesNothing(t *testing.T) {
	ru := ResourceUpdate{
		Operation: OperationUpdate,
		DesiredState: pkgmodel.Resource{
			Label: "plain", Type: "Test::Secret::Entry", Stack: "s",
			Properties:    json.RawMessage(`{"Name": "plain"}`),
			PatchDocument: json.RawMessage(`[{"op":"replace","path":"/Name","value":"plain"}]`),
		},
	}

	require.NoError(t, ru.ResolveGeneratorValue("gen-a", map[string]string{"value": "drawn"}, "generation-1", pkgmodel.FormaApplyModeReconcile))

	assert.JSONEq(t, `{"Name": "plain"}`, string(ru.DesiredState.Properties))
	assert.JSONEq(t, `[{"op":"replace","path":"/Name","value":"plain"}]`, string(ru.DesiredState.PatchDocument),
		"a resource with nothing to deliver keeps its planned patch")
}
