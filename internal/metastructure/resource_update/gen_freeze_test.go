// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// genBoundSchema declares one generator-bound secret alongside plain metadata,
// the shape of a provider secret whose value formae mints.
func genBoundSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "Password"},
		Hints:      map[string]pkgmodel.FieldHint{"Password": {Opaque: true}},
	}
}

// boundGenLeaf is the destination as the forma declares it once command
// translation has resolved the generator: a reference to an output, carrying
// no value of its own.
const boundGenLeaf = `{"$gen":true,"$generator":"` + boundGeneratorKsuid +
	`","$output":"value","$visibility":"Opaque"}`

// storedGenLeaf is that same destination at rest: the drawn value hashed, and
// the generation it was drawn from recorded on the occurrence.
func storedGenLeaf(digest, resolvedFrom string) string {
	return `{"$gen":true,"$generator":"` + boundGeneratorKsuid +
		`","$output":"value","$visibility":"Opaque","$hashed":true,"$value":"` + digest +
		`","$resolvedFrom":"` + resolvedFrom + `"}`
}

func genBoundProperties(description, passwordLeaf string) json.RawMessage {
	return json.RawMessage(`{"Name":"n","Description":"` + description + `","Password":` + passwordLeaf + `}`)
}

// genBoundUpdate builds the ResourceUpdateData for an update to a resource
// whose Password is bound to a generator, with the given planning-time
// occurrence records.
func genBoundUpdate(records []OccurrenceRecord, digest, priorDescription, desiredDescription, patch string) ResourceUpdateData {
	schema := genBoundSchema()
	ru := &ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Generated::Consumer", Stack: "default",
			Schema:     schema,
			Properties: genBoundProperties(priorDescription, storedGenLeaf(digest, provenance.DigestOfString("generation-1"))),
		},
		DesiredState: pkgmodel.Resource{
			Label: "consumer", Type: "Test::Generated::Consumer", Stack: "default",
			Schema:        schema,
			Properties:    genBoundProperties(desiredDescription, boundGenLeaf),
			PatchDocument: json.RawMessage(patch),
		},
		ProvenanceRecords: records,
		ResourceTarget:    pkgmodel.Target{Label: "us-east-1", Namespace: "test", Config: json.RawMessage(`{}`)},
	}
	return ResourceUpdateData{resourceUpdate: ru, commandID: "cmd-1", originalResourceKsuidURI: ru.DesiredState.URI()}
}

// stableGenRecord is the planning-time record for a binding whose generation
// is provably the one the destination already carries.
func stableGenRecord() []OccurrenceRecord {
	return []OccurrenceRecord{{
		DestinationPath:  "Password",
		DesiredIdentity:  OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: boundGeneratorKsuid, PropertyPath: "value"},
		StoredIdentity:   OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: boundGeneratorKsuid, PropertyPath: "value"},
		HasStoredWritten: true,
		Class:            OccurrenceStable,
	}}
}

// A binding whose occurrence classified stable draws no value, so its
// destination still holds the bare envelope when the update is dispatched. The
// envelope is never sent to a provider, and it must not block the rest of the
// document either: an edit to an unrelated property on the same resource has to
// reach the plugin, with the bound destination present but unusable.
func TestUpdate_StableGeneratorBinding_SiblingEditReachesPlugin(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("the-drawn-password")
	data := genBoundUpdate(stableGenRecord(), digest, "one", "two",
		`[{"op":"replace","path":"/Description","value":"two"}]`)
	proc := newOperationCapturingProcess()

	_, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)

	op := proc.capturedUpdate(t)
	assert.JSONEq(t, `[{"op":"replace","path":"/Description","value":"two"}]`, op.PatchDocument,
		"the patch must carry the unrelated change and nothing else")

	desired := map[string]any{}
	require.NoError(t, json.Unmarshal(op.DesiredProperties, &desired))
	assert.Equal(t, "two", desired["Description"], "the unrelated change must reach the plugin")
	assert.Equal(t, map[string]any{"$opaque": "preserved"}, desired["Password"],
		"a stable generator binding must reach the plugin as a present-but-unusable sentinel")
	assert.Contains(t, desired, "Password",
		"the bound field must stay present: absence means no value, which can clear the secret")
	assert.NotContains(t, string(op.DesiredProperties), "$gen",
		"the generator reference itself must never leave the agent")
	assert.NotContains(t, string(op.DesiredProperties), digest,
		"the stored digest must never leave the agent")
	assert.NotContains(t, string(op.PriorProperties), digest,
		"the stored digest must never leave the agent")

	assert.Contains(t, string(data.resourceUpdate.DesiredState.Properties), "$gen",
		"the durable desired state must keep the binding")
}

// A forced apply over an unchanged generator-bound secret classifies the
// binding stable — force is not a rotation — so it dispatches like any other
// update: the plugin call is made, the destination is present but unusable,
// and no value is drawn or written.
func TestUpdate_ForcedApplyOverUnchangedGeneratorBinding_DoesNotRotate(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("the-drawn-password")
	desired := genBoundProperties("one", boundGenLeaf)
	stored := genBoundProperties("one", storedGenLeaf(digest, provenance.DigestOfString("generation-1")))

	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	consumer := pkgmodel.Resource{
		Label: "consumer", Type: "Test::Generated::Consumer", Stack: "default",
		Schema: genBoundSchema(), Properties: desired,
	}
	answers, err := resolver.LoadResolvablePropertiesFromStacks(consumer, nil, nil,
		func(string) (pkgmodel.GeneratorIdentity, pkgmodel.Generator) {
			return heldGeneration(t, "generation-1", spec), spec
		})
	require.NoError(t, err)

	records := buildProvenanceRecords(desired, stored, answers, genBoundSchema(), true)
	require.Len(t, records, 1)
	require.Equal(t, OccurrenceStable, records[0].Class,
		"a forced apply must not reclassify a generator binding as one to re-assert")

	data := genBoundUpdate(records, digest, "one", "one", `[]`)
	proc := newOperationCapturingProcess()

	_, _, _, err = update(StateUpdating, data, proc)
	require.NoError(t, err)

	op := proc.capturedUpdate(t)
	desiredProps := map[string]any{}
	require.NoError(t, json.Unmarshal(op.DesiredProperties, &desiredProps))
	assert.Equal(t, map[string]any{"$opaque": "preserved"}, desiredProps["Password"],
		"a forced apply must leave the provider's value in place, not clear it and not replace it")
	assert.NotContains(t, string(op.DesiredProperties), digest)

	assert.Contains(t, string(data.resourceUpdate.DesiredState.Properties), "$gen",
		"a forced apply must not rotate the binding")
}

// The freeze is keyed on the planning-time classification, never on the mere
// presence of an envelope. A destination that has no stable record still owes a
// drawn value, and dispatching it unresolved stays a refusal.
func TestUpdate_GeneratorBindingWithoutStableRecord_IsStillRefused(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("the-drawn-password")
	data := genBoundUpdate(nil, digest, "one", "two",
		`[{"op":"replace","path":"/Description","value":"two"}]`)
	proc := newOperationCapturingProcess()

	state, _, _, err := update(StateUpdating, data, proc)
	require.NoError(t, err)
	require.Equal(t, StateFinishedWithError, state, "an undrawn binding must not be dispatched")
	assert.Nil(t, proc.operation, "the plugin must never be called with an undrawn binding")
	assert.Contains(t, strings.Join(proc.log.all(), "\n"), "failed to convert resource properties for plugin")
	assert.Equal(t, failureReasonUndrawnGeneratorValueOnUpdate, data.resourceUpdate.MostRecentFailureMessage())
}

// A stable binding that a draw was delivered into carries the drawn value in
// its envelope, and the freeze must leave it alone. Stability decides whether
// the generator draws, not who a draw reaches, so a stable destination does
// receive a draw that is happening — and substituting the sentinel over it
// would send the provider a preserved marker instead of the credential,
// leaving that destination on the old generation while its siblings moved.
func TestFreezeStableGeneratorBindings_LeavesADeliveredValueAlone(t *testing.T) {
	delivered := `{"$gen":true,"$generator":"` + boundGeneratorKsuid +
		`","$output":"value","$visibility":"Opaque","$value":"the-freshly-drawn-password"}`

	out, err := FreezeStableGeneratorBindings(
		genBoundProperties("one", delivered), stableGenRecord())
	require.NoError(t, err)

	assert.JSONEq(t, delivered, gjson.GetBytes(out, "Password").Raw,
		"a delivered value must survive the freeze untouched")
}

// The same binding with no value delivered into it is still frozen: nothing
// drew for it, so its envelope is bare, and the guard that refuses to hand a
// provider a reference in a secret's place would otherwise take the whole
// document with it.
func TestFreezeStableGeneratorBindings_FreezesABareEnvelope(t *testing.T) {
	out, err := FreezeStableGeneratorBindings(
		genBoundProperties("one", boundGenLeaf), stableGenRecord())
	require.NoError(t, err)

	assert.JSONEq(t, `{"$opaque":"preserved"}`, gjson.GetBytes(out, "Password").Raw)
}
