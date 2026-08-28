// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func writeOnlyCreateOnlySchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"ClusterName", "AccessConfig"},
		Hints: map[string]pkgmodel.FieldHint{
			"AccessConfig": {WriteOnly: true, CreateOnly: true},
		},
	}
}

// A writeOnly+createOnly field whose last-applied value is in the stored
// document, and whose desired value differs, is a genuine createOnly change:
// it must surface in the createOnly patch so the plan declares a replacement,
// not be silently dropped.
func TestGeneratePatch_WriteOnlyCreateOnlyChanged_StoredBaseline_PlansReplacement(t *testing.T) {
	document := []byte(`{
		"ClusterName": "my-cluster",
		"AccessConfig": {"AuthenticationMode": "API_AND_CONFIG_MAP"}
	}`)
	patch := []byte(`{
		"ClusterName": "my-cluster",
		"AccessConfig": {"AuthenticationMode": "API"}
	}`)
	props := resolver.NewResolvableProperties()

	patchDoc, createOnlyPatch, _, err := generatePatch(document, patch, nil, nil, props, writeOnlyCreateOnlySchema(), pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	if len(patchDoc) > 0 {
		assert.JSONEq(t, "[]", string(patchDoc), "a createOnly change belongs in the createOnly patch, not the update patch")
	}
	require.NotEmpty(t, createOnlyPatch, "a changed writeOnly+createOnly field with a stored baseline must plan a replacement")
	assert.Contains(t, string(createOnlyPatch), "API", "the replacement patch must carry the new value")
}

// The same field with a stored baseline and an unchanged desired value plans
// nothing: equality against the stored last-applied value converges without
// any op or replacement.
func TestGeneratePatch_WriteOnlyCreateOnlyUnchanged_StoredBaseline_NoOp(t *testing.T) {
	document := []byte(`{
		"ClusterName": "my-cluster",
		"AccessConfig": {"AuthenticationMode": "API_AND_CONFIG_MAP"}
	}`)
	patch := []byte(`{
		"ClusterName": "my-cluster",
		"AccessConfig": {"AuthenticationMode": "API_AND_CONFIG_MAP"}
	}`)
	props := resolver.NewResolvableProperties()

	patchDoc, createOnlyPatch, _, err := generatePatch(document, patch, nil, nil, props, writeOnlyCreateOnlySchema(), pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch, "an unchanged writeOnly+createOnly field must not plan a replacement")
	assert.Nil(t, patchDoc, "an unchanged writeOnly+createOnly field must not plan any op")
}
