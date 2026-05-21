// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldHint_EdgeKind_Unmarshal_Default(t *testing.T) {
	var fh FieldHint
	err := json.Unmarshal([]byte(`{}`), &fh)
	require.NoError(t, err)
	require.Equal(t, EdgeKindDefault, fh.EdgeKind)
}

func TestFieldHint_EdgeKind_Unmarshal_Explicit(t *testing.T) {
	var fh FieldHint
	err := json.Unmarshal([]byte(`{"EdgeKind":"runtimeDependency"}`), &fh)
	require.NoError(t, err)
	require.Equal(t, EdgeKindRuntimeDependency, fh.EdgeKind)
}

func TestFieldHint_EdgeKind_FromAttachesTo_TransitionalAlias(t *testing.T) {
	// Old-shape JSON carrying AttachesTo without EdgeKind — derive EdgeKindAttachesTo.
	var fh FieldHint
	err := json.Unmarshal([]byte(`{"AttachesTo":true}`), &fh)
	require.NoError(t, err)
	require.Equal(t, EdgeKindAttachesTo, fh.EdgeKind)
}

func TestFieldHintAttachesToJSONRoundTrip(t *testing.T) {
	original := FieldHint{AttachesTo: true}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded FieldHint
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.True(t, decoded.AttachesTo, "AttachesTo should round-trip through JSON (raw=%s)", string(data))
}

func TestFieldHintAttachesToDefaultsFalse(t *testing.T) {
	var decoded FieldHint
	require.NoError(t, json.Unmarshal([]byte(`{"CreateOnly":true}`), &decoded))

	assert.False(t, decoded.AttachesTo, "AttachesTo should default false for pre-existing stored schemas")
}
