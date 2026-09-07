// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package pkl

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestPkl_FieldHint_CoOwned_RendersSystemPatterns verifies that a field
// annotated with @formae.FieldHint's coOwned round-trips into Schema.Hints as
// a CoOwned.SystemPatterns list, while a plain field alongside it renders no
// CoOwned key at all — never an empty object, which Go would decode as a
// non-nil, misleadingly "co-owned with no patterns" value instead of "not
// co-owned".
func TestPkl_FieldHint_CoOwned_RendersSystemPatterns(t *testing.T) {
	p := PKL{}
	forma, err := p.Evaluate("./testdata/forma/co_owned_field_test.pkl", model.CommandEval, model.FormaApplyModeReconcile, nil)
	require.NoError(t, err)

	jsonString := forma.ToJSON()

	membersHint := gjson.Get(jsonString, "Resources.0.Properties.value.Hints.members")
	assert.Equal(t, "Set", membersHint.Get("UpdateMethod").String())
	patterns := membersHint.Get("CoOwned.SystemPatterns").Array()
	require.Len(t, patterns, 1)
	assert.Equal(t, "aws:*", patterns[0].String())

	plainHint := gjson.Get(jsonString, "Resources.0.Properties.value.Hints.plain")
	assert.False(t, plainHint.Get("CoOwned").Exists(),
		"an unannotated field must render no CoOwned key at all, not an empty object")
}

// TestPkl_FieldHint_CoOwned_ArrayUpdateMethodFailsEval verifies that pairing
// coOwned with updateMethod "Array" fails at PKL eval: a field replaced or
// diffed wholesale has no per-member merge to reconcile co-ownership against.
func TestPkl_FieldHint_CoOwned_ArrayUpdateMethodFailsEval(t *testing.T) {
	p := PKL{}
	_, err := p.Evaluate("./testdata/forma/co_owned_field_array_update_method_test.pkl", model.CommandEval, model.FormaApplyModeReconcile, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "coOwned requires updateMethod")
}

// TestPkl_FieldHint_CoOwned_OpaqueFailsEval verifies that pairing coOwned
// with opaque fails at PKL eval: an opaque field's member identities are its
// secret values, not names, so recording them in an ownership record would
// persist the secret unhashed.
func TestPkl_FieldHint_CoOwned_OpaqueFailsEval(t *testing.T) {
	p := PKL{}
	_, err := p.Evaluate("./testdata/forma/co_owned_field_opaque_test.pkl", model.CommandEval, model.FormaApplyModeReconcile, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "coOwned cannot be combined with opaque")
}
