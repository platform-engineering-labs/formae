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
// is the only op the diff would otherwise produce — and it must not itself
// decide that an update is sent.
func TestGeneratePatch_UnchangedRequiredOnUpdateField_ProducesNilPatch(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"Name", "Token"},
		Hints: map[string]pkgmodel.FieldHint{
			"Token": {RequiredOnUpdate: true},
		},
	}

	document := []byte(`{"Name": "n1", "Token": "t1"}`)
	desired := []byte(`{"Name": "n1", "Token": "t1"}`)

	patchDoc, createOnlyPatch, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)

	assert.Empty(t, createOnlyPatch)
	assert.Empty(t, patchDoc, "a requiredOnUpdate field's own force-resent add must not conjure a patch when nothing changed")
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

	patchDoc, createOnlyPatch, err := generatePatch(document, desired, nil, nil,
		resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModePatch)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)

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
