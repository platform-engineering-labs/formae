// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriftPatch(t *testing.T) {
	old := json.RawMessage(`{"Value":"/api/v1","Description":"API endpoint"}`)
	cur := json.RawMessage(`{"Value":"/api/v2","Description":"API endpoint"}`)
	p, err := DriftPatch(old, cur)
	require.NoError(t, err)
	var ops []map[string]any
	require.NoError(t, json.Unmarshal(p, &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, "replace", ops[0]["op"])
	assert.Equal(t, "/Value", ops[0]["path"])
	assert.Equal(t, "/api/v2", ops[0]["value"])
}

// The applied baseline is formae's own bookkeeping inside a reference
// envelope, not cloud state. A drift report describes what changed in the
// cloud, so the baseline must not appear in it — including when absorbing the
// drift drops the baseline and would otherwise render as a removal.
func TestDriftPatch_OmitsTheAppliedBaseline(t *testing.T) {
	old := json.RawMessage(`{"TargetKeyId":{"$ref":"formae://abc#/Arn","$value":"key-old","$applied":"arn:aws:kms:::key/key-old"}}`)
	cur := json.RawMessage(`{"TargetKeyId":{"$ref":"formae://abc#/Arn","$value":"key-new"}}`)

	p, err := DriftPatch(old, cur)
	require.NoError(t, err)
	assert.NotContains(t, string(p), "$applied", "the applied baseline is not cloud state")

	var ops []map[string]any
	require.NoError(t, json.Unmarshal(p, &ops))
	require.Len(t, ops, 1, "only the observed value changed")
	assert.Equal(t, "replace", ops[0]["op"])
	assert.Equal(t, "/TargetKeyId/$value", ops[0]["path"])
	assert.Equal(t, "key-new", ops[0]["value"])
}

// A drift report on a resource whose reference did not change at all is empty,
// even though the stored side carries a baseline the read side never has.
func TestDriftPatch_UnchangedReferenceWithBaseline_IsEmpty(t *testing.T) {
	old := json.RawMessage(`{"TargetKeyId":{"$ref":"formae://abc#/Arn","$value":"key-1","$applied":"arn:aws:kms:::key/key-1"}}`)
	cur := json.RawMessage(`{"TargetKeyId":{"$ref":"formae://abc#/Arn","$value":"key-1"}}`)

	p, err := DriftPatch(old, cur)
	require.NoError(t, err)
	var ops []map[string]any
	require.NoError(t, json.Unmarshal(p, &ops))
	assert.Empty(t, ops)
}
