// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are regression tests for PLA-35: a surfaced opaque value must not churn
// (re-emit cleartext) or corrupt (re-emit its stored hash, for setOnce) on an
// unrelated sibling edit, while a genuinely changed opaque value must still
// produce a patch op. They drive the public factory entrypoint and assert on the
// resulting ResourceUpdate, so they fail on the unfixed tree and are independent
// of how the suppression is implemented.

var anySHA256 = regexp.MustCompile(`[0-9a-f]{64}`)

// opaqueLeaf renders a wrapped opaque property with the given strategy and value.
func opaqueLeaf(strategy, value string) string {
	return `{"$strategy":"` + strategy + `","$visibility":"Opaque","$value":"` + value + `"}`
}

func opaqueRes(label string, schema pkgmodel.Schema, props string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      label,
		Type:       "AWS::SecretsManager::Secret",
		Stack:      "default",
		Schema:     schema,
		Properties: json.RawMessage(props),
	}
}

// updatePatchFor runs the production factory and returns the single mutable
// update's patch document. Fails the test if the factory plans anything other
// than one in-place update.
func updatePatchFor(t *testing.T, existing, desired pkgmodel.Resource) json.RawMessage {
	t.Helper()
	updates := planUpdates(t, existing, desired)
	require.Len(t, updates, 1, "expected a single in-place update, got %d", len(updates))
	require.Equal(t, OperationUpdate, updates[0].Operation)
	return updates[0].DesiredState.PatchDocument
}

func planUpdates(t *testing.T, existing, desired pkgmodel.Resource) []ResourceUpdate {
	t.Helper()
	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}
	updates, err := NewResourceUpdateForExisting(
		resolver.ResolvableProperties{}, existing, desired,
		target, target, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser)
	require.NoError(t, err)
	return updates
}

func opsByPath(t *testing.T, patchDoc json.RawMessage) map[string]jsonpatch.JsonPatchOperation {
	t.Helper()
	byPath := map[string]jsonpatch.JsonPatchOperation{}
	if len(patchDoc) == 0 {
		return byPath
	}
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	for _, op := range ops {
		byPath[op.Path] = op
	}
	return byPath
}

func writeOnlyMutableSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"secret", "name"},
		Hints:  map[string]pkgmodel.FieldHint{"secret": {WriteOnly: true}},
	}
}

// Headline rotation use case: a surfaced writeOnly opaque value that is UNCHANGED
// must not re-emit when an unrelated sibling field changes (no churn, no new
// secret version). On the unfixed tree this produces an `add /secret` op.
func TestUpdate_WriteOnlyOpaqueUnchanged_SiblingEdit_NoValueOp(t *testing.T) {
	schema := writeOnlyMutableSchema()
	existing := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", pkgmodel.ComputeValueHash("s"))+`,"name":"old"}`)
	desired := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", "s")+`,"name":"new"}`)

	patchDoc := updatePatchFor(t, existing, desired)
	byPath := opsByPath(t, patchDoc)

	assert.NotContains(t, byPath, "/secret", "unchanged opaque value must not enter the patch")
	require.Contains(t, byPath, "/name", "sibling change must still produce an op")
	assert.NotRegexp(t, anySHA256, string(patchDoc), "no stored hash may leak into the patch")
}

// Rotation still works: a CHANGED writeOnly opaque value produces a patch op
// carrying the new cleartext.
func TestUpdate_WriteOnlyOpaqueChanged_CarriesCleartext(t *testing.T) {
	schema := writeOnlyMutableSchema()
	existing := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", pkgmodel.ComputeValueHash("old"))+`,"name":"x"}`)
	desired := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", "new-secret")+`,"name":"x"}`)

	patchDoc := updatePatchFor(t, existing, desired)
	byPath := opsByPath(t, patchDoc)

	require.Contains(t, byPath, "/secret", "changed opaque value must appear in the patch")
	assert.Equal(t, "new-secret", byPath["/secret"].Value)
	assert.NotRegexp(t, anySHA256, string(patchDoc))
}

// No corruption: a setOnce-frozen opaque value must not re-emit its own stored
// hash as the new value when a sibling changes. On the unfixed tree this produces
// `add /secret = <sha256>`.
func TestUpdate_SetOnceOpaqueFrozen_SiblingEdit_NoValueOpNoHash(t *testing.T) {
	schema := writeOnlyMutableSchema()
	existing := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("SetOnce", pkgmodel.ComputeValueHash("s"))+`,"name":"old"}`)
	desired := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("SetOnce", "attempted-rotation")+`,"name":"new"}`)

	patchDoc := updatePatchFor(t, existing, desired)
	byPath := opsByPath(t, patchDoc)

	assert.NotContains(t, byPath, "/secret", "frozen setOnce opaque value must not enter the patch")
	require.Contains(t, byPath, "/name")
	assert.NotRegexp(t, anySHA256, string(patchDoc), "stored hash must never be sent as the value")
}

// The case the writeOnly-keyed approach misses: a surfaced opaque NON-writeOnly
// value that is UNCHANGED must not churn (replace with cleartext) on a sibling
// edit.
func TestUpdate_NonWriteOnlyOpaqueUnchanged_SiblingEdit_NoValueOp(t *testing.T) {
	schema := pkgmodel.Schema{Fields: []string{"secret", "name"}} // opaque but NOT writeOnly
	existing := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", pkgmodel.ComputeValueHash("s"))+`,"name":"old"}`)
	desired := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", "s")+`,"name":"new"}`)

	patchDoc := updatePatchFor(t, existing, desired)
	byPath := opsByPath(t, patchDoc)

	assert.NotContains(t, byPath, "/secret", "unchanged non-writeOnly opaque value must not churn")
	require.Contains(t, byPath, "/name")
	assert.NotRegexp(t, anySHA256, string(patchDoc))
}

// PLA-35 C2: an UNCHANGED opaque createOnly value must not phantom-replace the
// resource when a sibling changes — the factory must plan one in-place update,
// not a destroy+create.
func TestUpdate_OpaqueCreateOnlyUnchanged_SiblingEdit_NoReplace(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"secret", "name"},
		Hints:  map[string]pkgmodel.FieldHint{"secret": {CreateOnly: true}},
	}
	existing := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", pkgmodel.ComputeValueHash("s"))+`,"name":"old"}`)
	desired := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", "s")+`,"name":"new"}`)

	updates := planUpdates(t, existing, desired)
	require.Len(t, updates, 1, "unchanged opaque createOnly must not trigger a destroy+create")
	assert.Equal(t, OperationUpdate, updates[0].Operation)
	assert.Contains(t, opsByPath(t, updates[0].DesiredState.PatchDocument), "/name")
}

// PLA-35 C1 preserved: a CHANGED opaque createOnly value must still drive a
// replacement (destroy+create), not be silently dropped.
func TestUpdate_OpaqueCreateOnlyChanged_StillReplaces(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"secret", "name"},
		Hints:  map[string]pkgmodel.FieldHint{"secret": {CreateOnly: true}},
	}
	existing := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", pkgmodel.ComputeValueHash("s1"))+`,"name":"x"}`)
	desired := opaqueRes("r", schema, `{"secret":`+opaqueLeaf("Update", "s2")+`,"name":"x"}`)

	updates := planUpdates(t, existing, desired)
	require.Len(t, updates, 2, "changed opaque createOnly must plan destroy+create")
	ops := map[OperationType]bool{}
	for _, u := range updates {
		ops[u.Operation] = true
	}
	assert.True(t, ops[OperationDelete], "replace must include a delete")
	assert.True(t, ops[OperationCreate], "replace must include a create")
}

// Direct unit coverage of the helper's per-leaf decisions (complements the
// behavior-level regression tests above).
func TestSuppressUnchangedOpaqueValues_Decisions(t *testing.T) {
	t.Run("unchanged opaque dropped from both sides", func(t *testing.T) {
		existing := json.RawMessage(`{"secret":` + opaqueLeaf("Update", pkgmodel.ComputeValueHash("s")) + `,"name":"old"}`)
		desired := json.RawMessage(`{"secret":` + opaqueLeaf("Update", "s") + `,"name":"new"}`)

		strippedExisting, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.NotContains(t, string(strippedDesired), "secret")
		assert.NotContains(t, string(strippedExisting), "secret")
		assert.Contains(t, string(strippedDesired), "name")
	})

	t.Run("changed opaque kept", func(t *testing.T) {
		existing := json.RawMessage(`{"secret":` + opaqueLeaf("Update", pkgmodel.ComputeValueHash("old")) + `}`)
		desired := json.RawMessage(`{"secret":` + opaqueLeaf("Update", "new") + `}`)

		_, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.Contains(t, string(strippedDesired), "secret")
	})

	t.Run("non-opaque field untouched", func(t *testing.T) {
		existing := json.RawMessage(`{"token":{"$visibility":"Clear","$value":"a"}}`)
		desired := json.RawMessage(`{"token":{"$visibility":"Clear","$value":"a"}}`)

		_, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.Contains(t, string(strippedDesired), "token")
	})

	t.Run("first set kept (no stored value)", func(t *testing.T) {
		existing := json.RawMessage(`{"name":"x"}`)
		desired := json.RawMessage(`{"secret":` + opaqueLeaf("Update", "first") + `,"name":"x"}`)

		_, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.Contains(t, string(strippedDesired), "secret")
	})

	t.Run("nested unchanged opaque dropped", func(t *testing.T) {
		existing := json.RawMessage(`{"login":{"password":` + opaqueLeaf("Update", pkgmodel.ComputeValueHash("p")) + `}}`)
		desired := json.RawMessage(`{"login":{"password":` + opaqueLeaf("Update", "p") + `}}`)

		_, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.NotContains(t, string(strippedDesired), "password")
	})

	t.Run("array-nested unchanged opaque dropped", func(t *testing.T) {
		existing := json.RawMessage(`{"users":[{"password":` + opaqueLeaf("Update", pkgmodel.ComputeValueHash("p")) + `}],"name":"old"}`)
		desired := json.RawMessage(`{"users":[{"password":` + opaqueLeaf("Update", "p") + `}],"name":"new"}`)

		strippedExisting, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.NotContains(t, string(strippedDesired), "$value", "unchanged array-nested opaque value must be dropped")
		assert.NotContains(t, string(strippedExisting), "$value")
		assert.Contains(t, string(strippedDesired), "name")
	})

	t.Run("array-nested changed opaque kept", func(t *testing.T) {
		existing := json.RawMessage(`{"users":[{"password":` + opaqueLeaf("Update", pkgmodel.ComputeValueHash("p1")) + `}]}`)
		desired := json.RawMessage(`{"users":[{"password":` + opaqueLeaf("Update", "p2") + `}]}`)

		_, strippedDesired, err := SuppressUnchangedOpaqueValues(existing, desired)
		require.NoError(t, err)
		assert.Contains(t, string(strippedDesired), "p2", "changed array-nested opaque value must be kept")
	})
}
