// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package patch

import (
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A requiredOnUpdate field is force-resent by stripping it from the document
// side before the diff, so its own value always reappears as an "add" op. When
// the document and the desired state agree on every field, that reappearance
// is the only op the diff would otherwise produce. generatePatch reports this
// via onlyForceResent rather than nilling patchDoc out: the decision of
// whether to plan an update at all belongs to the caller (planning drops it;
// execution-time regeneration must not), so the op itself is never stripped
// from what this function returns.
func TestGeneratePatch_UnchangedRequiredOnUpdateField_ReportsOnlyForceResent(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Token"},
		Hints: map[string]pkgmodel.FieldHint{
			"Token": {RequiredOnUpdate: true},
		},
	}

	document := []byte(`{"Name": "n1", "Token": "t1"}`)
	desired := []byte(`{"Name": "n1", "Token": "t1"}`)

	patchDoc, createOnlyPatch, onlyForceResent, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)

	assert.Empty(t, createOnlyPatch)
	assert.True(t, onlyForceResent, "nothing real changed, so the decision must report force-resent-only")

	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1, "the force-resent Token op is still content this function returns")
	assert.Equal(t, "/Token", ops[0].Path)
	assert.Equal(t, "add", ops[0].Operation)
	assert.Equal(t, "t1", ops[0].Value)
}

// When a real change is present elsewhere in the same resource, the
// requiredOnUpdate field's force-resent op rides along unchanged: the
// provider still requires it in every update payload it actually receives.
func TestGeneratePatch_ChangedFieldAlongsideRequiredOnUpdateField_KeepsForceResentAdd(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Token"},
		Hints: map[string]pkgmodel.FieldHint{
			"Token": {RequiredOnUpdate: true},
		},
	}

	document := []byte(`{"Name": "n1", "Token": "t1"}`)
	desired := []byte(`{"Name": "n2", "Token": "t1"}`)

	patchDoc, createOnlyPatch, onlyForceResent, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	assert.False(t, onlyForceResent, "a real change is present, so the decision must not report force-resent-only")

	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 2, "expected one op for the real Name change and one force-resent op for Token")

	var nameOp, tokenOp *jsonpatch.JsonPatchOperation
	for i := range ops {
		switch ops[i].Path {
		case "/Name":
			nameOp = &ops[i]
		case "/Token":
			tokenOp = &ops[i]
		}
	}

	require.NotNil(t, nameOp, "the real Name change must be present")
	assert.Equal(t, "n2", nameOp.Value)

	require.NotNil(t, tokenOp, "the force-resent Token op must still ride along with the real change")
	assert.Equal(t, "add", tokenOp.Operation, "requiredOnUpdate fields are force-resent as an add")
	assert.Equal(t, "t1", tokenOp.Value)
}

// A genuine change to a requiredOnUpdate field plans an update like any other
// change.
func TestGeneratePatch_ChangedRequiredOnUpdateFieldItself_PlansUpdate(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Token"},
		Hints: map[string]pkgmodel.FieldHint{
			"Token": {RequiredOnUpdate: true},
		},
	}

	document := []byte(`{"Name": "n1", "Token": "t1"}`)
	desired := []byte(`{"Name": "n1", "Token": "t2"}`)

	patchDoc, createOnlyPatch, onlyForceResent, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	assert.False(t, onlyForceResent, "the Token change itself is real, so the decision must not report force-resent-only")

	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1, "expected exactly one op for the genuine Token change")

	tokenOp := ops[0]
	assert.Equal(t, "/Token", tokenOp.Path)
	assert.Equal(t, "add", tokenOp.Operation, "requiredOnUpdate fields are force-resent as an add")
	assert.Equal(t, "t2", tokenOp.Value, "the op must carry the new value, not the stored one")
}

// A requiredOnUpdate hint on a field nested inside an array ("Items.Token")
// is force-resent the same way a top-level field is: stripping happens inside
// every array element, so the resulting "add" op lands at an array-indexed
// path ("/Items/0/Token"). That path must still be recognized as force-resent
// when nothing about it actually changed.
func TestGeneratePatch_UnchangedArrayNestedRequiredOnUpdateField_ReportsOnlyForceResent(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Items"},
		Hints: map[string]pkgmodel.FieldHint{
			"Items":       {UpdateMethod: pkgmodel.FieldUpdateMethodArray},
			"Items.Token": {RequiredOnUpdate: true},
		},
	}

	document := []byte(`{"Name": "n1", "Items": [{"Token": "same"}]}`)
	desired := []byte(`{"Name": "n1", "Items": [{"Token": "same"}]}`)

	patchDoc, createOnlyPatch, onlyForceResent, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)

	assert.Empty(t, createOnlyPatch)
	assert.True(t, onlyForceResent, "the array-nested Token op is unchanged, so the decision must report force-resent-only")

	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1, "the force-resent Items.0.Token op is still content this function returns")
	assert.Equal(t, "/Items/0/Token", ops[0].Path)
	assert.Equal(t, "add", ops[0].Operation)
	assert.Equal(t, "same", ops[0].Value)
}

// The changed direction for an array-nested requiredOnUpdate field: a genuine
// change still plans an update like any other change.
func TestGeneratePatch_ChangedArrayNestedRequiredOnUpdateField_PlansUpdate(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Items"},
		Hints: map[string]pkgmodel.FieldHint{
			"Items":       {UpdateMethod: pkgmodel.FieldUpdateMethodArray},
			"Items.Token": {RequiredOnUpdate: true},
		},
	}

	document := []byte(`{"Name": "n1", "Items": [{"Token": "old"}]}`)
	desired := []byte(`{"Name": "n1", "Items": [{"Token": "new"}]}`)

	patchDoc, createOnlyPatch, onlyForceResent, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)

	assert.Empty(t, createOnlyPatch)
	assert.False(t, onlyForceResent, "the Items.0.Token change itself is real, so the decision must not report force-resent-only")

	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1, "expected exactly one op for the genuine array-nested Token change")
	assert.Equal(t, "/Items/0/Token", ops[0].Path)
	assert.Equal(t, "new", ops[0].Value)
}
