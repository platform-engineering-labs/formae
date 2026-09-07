// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const frozenRef = `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/SecretString","$value":"a-stored-digest","$hashed":true,"$visibility":"Opaque","$strategy":"SetOnce"}`
const bareFrozenRef = `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/SecretString"}`

func planFrozenRef(t *testing.T, stored, desired string, schema pkgmodel.Schema) ([]ResourceUpdate, error) {
	t.Helper()
	prior := pkgmodel.Resource{Label: "db", Type: "TEST::DB", Properties: json.RawMessage(stored), Schema: schema}
	next := prior
	next.Properties = json.RawMessage(desired)
	return NewResourceUpdateForExisting(resolver.NewResolvableProperties(), nil, prior, next,
		pkgmodel.Target{}, pkgmodel.Target{}, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
}

func TestOpaqueSetOnceRef_NoOp(t *testing.T) {
	updates, err := planFrozenRef(t, `{"Password":`+frozenRef+`}`, `{"Password":`+bareFrozenRef+`}`,
		pkgmodel.Schema{Fields: []string{"Password"}})
	require.NoError(t, err)
	require.Empty(t, updates, "a bare reference to the frozen destination must converge")
}

func TestOpaqueSetOnceRef_SiblingUpdate(t *testing.T) {
	updates, err := planFrozenRef(t, `{"Password":`+frozenRef+`,"Name":"old"}`, `{"Password":`+bareFrozenRef+`,"Name":"new"}`,
		pkgmodel.Schema{Fields: []string{"Password", "Name"}})
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.JSONEq(t, `[{"op":"replace","path":"/Name","value":"new"}]`, string(updates[0].DesiredState.PatchDocument))
	require.Empty(t, updates[0].RemainingResolvables)
}

func TestOpaqueSetOnceRef_ReplacementNeedsValue(t *testing.T) {
	updates, err := planFrozenRef(t, `{"Password":`+frozenRef+`,"Name":"old"}`, `{"Password":`+bareFrozenRef+`,"Name":"new"}`,
		pkgmodel.Schema{Fields: []string{"Password", "Name"}, Hints: map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}}})
	require.Error(t, err, "must refuse before scheduling the destructive half of replacement")
	require.Empty(t, updates)
}

func TestOpaqueSetOnceRef_Eligibility(t *testing.T) {
	for _, tc := range []struct {
		name, old, desired string
		frozen             bool
	}{
		{"live shape", frozenRef, bareFrozenRef, true},
		{"source witness changed", strings.Replace(frozenRef, `"$hashed":true`, `"$hashed":true,"$resolvedFrom":"old-source"`, 1), bareFrozenRef, true},
		{"strategy absent", strings.Replace(frozenRef, `,"$strategy":"SetOnce"`, "", 1), bareFrozenRef, false},
		{"rotating destination", strings.Replace(frozenRef, "SetOnce", "Update", 1), bareFrozenRef, false},
		{"explicit update", frozenRef, strings.Replace(bareFrozenRef, `}`, `,"$strategy":"Update"}`, 1), false},
		{"json repoint", frozenRef, strings.Replace(bareFrozenRef, `}`, `,"$json":"password"}`, 1), false},
		{"reference repoint", frozenRef, strings.Replace(bareFrozenRef, "SecretString", "Other", 1), false},
		{"new destination", `{}`, bareFrozenRef, false},
		{"desired plaintext", frozenRef, strings.Replace(bareFrozenRef, `}`, `,"$value":"new-secret"}`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := classifyFrozenSetOnceRefs(json.RawMessage(`{"Password":`+tc.old+`}`), json.RawMessage(`{"Password":`+tc.desired+`}`))
			require.Equal(t, tc.frozen, len(refs) == 1)
		})
	}
}

func TestOpaqueSetOnceRef_EnclosingWritesRefused(t *testing.T) {
	for _, tc := range []struct {
		name, old, desired string
		hints              map[string]pkgmodel.FieldHint
	}{
		{"atomic sibling", `{"Settings":{"Password":` + frozenRef + `,"Name":"old"}}`, `{"Settings":{"Password":` + bareFrozenRef + `,"Name":"new"}}`, map[string]pkgmodel.FieldHint{"Settings": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic}}},
		{"array sibling", `{"Entries":[{"Password":` + frozenRef + `,"Name":"old"}]}`, `{"Entries":[{"Password":` + bareFrozenRef + `,"Name":"new"}]}`, map[string]pkgmodel.FieldHint{"Entries": {UpdateMethod: pkgmodel.FieldUpdateMethodArray}}},
		{"array reorder duplicate refs", `{"Entries":[{"Password":` + frozenRef + `,"Name":"a"},{"Password":` + frozenRef + `,"Name":"b"}]}`, `{"Entries":[{"Password":` + bareFrozenRef + `,"Name":"b"},{"Password":` + bareFrozenRef + `,"Name":"a"}]}`, map[string]pkgmodel.FieldHint{"Entries": {UpdateMethod: pkgmodel.FieldUpdateMethodArray}}},
		{"required nested leaf", `{"Settings":{"Password":` + frozenRef + `},"Name":"old"}`, `{"Settings":{"Password":` + bareFrozenRef + `},"Name":"new"}`, map[string]pkgmodel.FieldHint{"Settings.Password": {RequiredOnUpdate: true}}},
		{"required leaf", `{"Password":` + frozenRef + `,"Name":"old"}`, `{"Password":` + bareFrozenRef + `,"Name":"new"}`, map[string]pkgmodel.FieldHint{"Password": {RequiredOnUpdate: true}}},
		{"required parent", `{"Settings":{"Password":` + frozenRef + `},"Name":"old"}`, `{"Settings":{"Password":` + bareFrozenRef + `},"Name":"new"}`, map[string]pkgmodel.FieldHint{"Settings": {RequiredOnUpdate: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updates, err := planFrozenRef(t, tc.old, tc.desired, pkgmodel.Schema{Fields: []string{"Settings", "Entries", "Password", "Name"}, Hints: tc.hints})
			require.Error(t, err)
			require.Empty(t, updates)
			require.NotContains(t, err.Error(), "a-stored-digest")
		})
	}
}

func TestOpaqueSetOnceRef_ArraysConverge(t *testing.T) {
	for _, wrap := range []func(string) string{
		func(ref string) string { return `{"Entries":[` + ref + `,` + ref + `]}` },
		func(ref string) string {
			return `{"Entries":[{"Password":` + ref + `,"Name":"a"},{"Password":` + ref + `,"Name":"b"}]}`
		},
	} {
		updates, err := planFrozenRef(t, wrap(frozenRef), wrap(bareFrozenRef), pkgmodel.Schema{Fields: []string{"Entries"}})
		require.NoError(t, err)
		require.Empty(t, updates)
	}
}

func TestOpaqueSetOnceRef_ExecutionAndRecovery(t *testing.T) {
	schema := pkgmodel.Schema{Fields: []string{"Password", "Name"}}
	updates, err := planFrozenRef(t, `{"Password":`+frozenRef+`,"Name":"old"}`, `{"Password":`+bareFrozenRef+`,"Name":"new"}`, schema)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	// Recovery must retain the decision, including when an enriching Read
	// replaced the prior digest with different live plaintext.
	wire, err := json.Marshal(updates[0])
	require.NoError(t, err)
	var ru ResourceUpdate
	require.NoError(t, json.Unmarshal(wire, &ru))
	require.NoError(t, ru.updateExistingResourceProperties(`{"Password":"different-live-secret","Name":"old"}`))
	patch, _, err := ru.regeneratePatchDocument(pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	require.JSONEq(t, `[{"op":"replace","path":"/Name","value":"new"}]`, string(patch))
	proc := newOperationCapturingProcess()
	ru.ResourceTarget = pkgmodel.Target{Config: json.RawMessage(`{}`)}
	_, _, _, err = update(StateUpdating, ResourceUpdateData{resourceUpdate: &ru}, proc)
	require.NoError(t, err)
	op := proc.capturedUpdate(t)
	require.JSONEq(t, `{"$opaque":"preserved"}`, gjson.GetBytes(op.DesiredProperties, "Password").Raw)
	require.NotContains(t, string(op.DesiredProperties), "a-stored-digest")
	require.NotContains(t, string(op.PatchDocument), "a-stored-digest")
	require.NoError(t, ru.updateResourceProperties(`{"Name":"new"}`, true))
	require.JSONEq(t, frozenRef, gjson.GetBytes(ru.DesiredState.Properties, "Password").Raw)
	next := ru.DesiredState
	next.Properties = json.RawMessage(`{"Password":` + bareFrozenRef + `,"Name":"new"}`)
	again, err := NewResourceUpdateForExisting(resolver.NewResolvableProperties(), nil, ru.DesiredState, next, pkgmodel.Target{}, pkgmodel.Target{}, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.NoError(t, err)
	require.Empty(t, again, "the update must preserve convergence on the next apply")
}

func TestOpaqueSetOnceRef_SharedSourceResolution(t *testing.T) {
	schema := pkgmodel.Schema{Fields: []string{"Password", "Other"}}
	updates, err := planFrozenRef(t, `{"Password":`+frozenRef+`,"Other":"old"}`, `{"Password":`+bareFrozenRef+`,"Other":`+bareFrozenRef+`}`, schema)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	ru := &updates[0]
	require.Len(t, ru.RemainingResolvables, 1, "the unfrozen consumer still needs this source")
	require.NoError(t, ru.ResolveValue("formae://2abcdefghijklmnopqrstuvwxyz#/SecretString", "rotated-source", pkgmodel.FormaApplyModeReconcile))
	require.JSONEq(t, bareFrozenRef, gjson.GetBytes(ru.DesiredState.Properties, "Password").Raw)
	require.Equal(t, "rotated-source", gjson.GetBytes(ru.DesiredState.Properties, "Other.$value").String())
	require.JSONEq(t, `[{"op":"replace","path":"/Other","value":"rotated-source"}]`, string(ru.DesiredState.PatchDocument))
}

func TestOpaqueSetOnceRef_ForceResentOnlyStillConverges(t *testing.T) {
	updates, err := planFrozenRef(t, `{"Entries":[`+frozenRef+`],"Name":"same"}`, `{"Entries":[`+bareFrozenRef+`],"Name":"same"}`, pkgmodel.Schema{Fields: []string{"Entries", "Name"}, Hints: map[string]pkgmodel.FieldHint{"Name": {RequiredOnUpdate: true}}})
	require.NoError(t, err)
	require.Empty(t, updates)
}

func TestOpaqueSetOnceRef_EmptyPatchDispatchRefused(t *testing.T) {
	for _, tc := range []struct {
		name, stored, desired string
		schema                pkgmodel.Schema
	}{
		{"array", `{"Entries":[{"Name":"a","Password":` + frozenRef + `}]}`, `{"Entries":[{"Name":"a","Password":` + bareFrozenRef + `}]}`, pkgmodel.Schema{Fields: []string{"Entries"}}},
		{"required", `{"Password":` + frozenRef + `}`, `{"Password":` + bareFrozenRef + `}`, pkgmodel.Schema{Fields: []string{"Password"}, Hints: map[string]pkgmodel.FieldHint{"Password": {RequiredOnUpdate: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := pkgmodel.Resource{Label: "db", Stack: "old-stack", Type: "TEST::DB", Properties: json.RawMessage(tc.stored), Schema: tc.schema}
			desired := prior
			desired.Stack = "new-stack"
			desired.Properties = json.RawMessage(tc.desired)
			updates, err := NewResourceUpdateForExisting(resolver.NewResolvableProperties(), nil, prior, desired, pkgmodel.Target{}, pkgmodel.Target{}, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
			require.Error(t, err, "refuse before sibling operations can execute")
			require.Empty(t, updates)
			// The dispatch guard also protects updates persisted by an older planner.
			records := buildProvenanceRecords(desired.Properties, prior.Properties, resolver.NewResolvableProperties(), tc.schema, false)
			require.NoError(t, recordFrozenSetOnceRefs(records, classifyFrozenSetOnceRefs(prior.Properties, desired.Properties)))
			ru := &ResourceUpdate{Operation: OperationUpdate, PriorState: prior, DesiredState: desired, PreviousProperties: prior.Properties, ProvenanceRecords: records}
			require.Empty(t, ru.DesiredState.PatchDocument)
			ru.ResourceTarget = pkgmodel.Target{Config: json.RawMessage(`{}`)}
			proc := newOperationCapturingProcess()
			state, _, _, err := update(StateUpdating, ResourceUpdateData{resourceUpdate: ru}, proc)
			require.NoError(t, err)
			require.Nil(t, proc.operation, "an empty patch must not bypass the frozen-value boundary")
			require.Equal(t, StateFinishedWithError, state)
			require.Contains(t, ru.FailureReason, "usable")
		})
	}
}

func TestOpaqueSetOnceRef_EntitySetConverges(t *testing.T) {
	updates, err := planFrozenRef(t, `{"Entries":[{"Name":"a","Password":`+frozenRef+`}]}`, `{"Entries":[{"Name":"a","Password":`+bareFrozenRef+`}]}`, pkgmodel.Schema{Fields: []string{"Entries"}, Hints: map[string]pkgmodel.FieldHint{"Entries": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Name"}}})
	require.NoError(t, err)
	require.Empty(t, updates)
}

func TestOpaqueSetOnceRef_MetadataArrayRefusedAtPlanning(t *testing.T) {
	prior := pkgmodel.Resource{Label: "db", Stack: "stack", Type: "TEST::DB", Properties: json.RawMessage(`{"Entries":[{"Name":"a","Password":` + frozenRef + `}]}`), Schema: pkgmodel.Schema{Fields: []string{"Entries"}}}
	desired := prior
	desired.Label = "renamed"
	desired.Alias = "db"
	desired.Properties = json.RawMessage(`{"Entries":[{"Name":"a","Password":` + bareFrozenRef + `}]}`)
	updates, err := NewResourceUpdateForExisting(resolver.NewResolvableProperties(), nil, prior, desired, pkgmodel.Target{}, pkgmodel.Target{}, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, false, false)
	require.Error(t, err, "even a synthetic rename cannot safely restore array entries by their planning-time indices")
	require.Empty(t, updates)
}
