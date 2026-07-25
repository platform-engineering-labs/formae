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

func TestFieldHint_RequiredOnUpdate_JSONRoundTrip(t *testing.T) {
	original := FieldHint{WriteOnly: true, RequiredOnUpdate: true}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded FieldHint
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.RequiredOnUpdate, "RequiredOnUpdate should round-trip through JSON (raw=%s)", string(data))
}

func TestFieldHint_RequiredOnUpdate_DefaultsFalseForLegacyJSON(t *testing.T) {
	// Schemas published before RequiredOnUpdate existed carry only WriteOnly;
	// they must default RequiredOnUpdate to false so writeOnly fields stop
	// force-resending rather than silently inheriting the old behavior.
	var decoded FieldHint
	require.NoError(t, json.Unmarshal([]byte(`{"WriteOnly":true}`), &decoded))
	assert.False(t, decoded.RequiredOnUpdate, "RequiredOnUpdate should default false for pre-existing stored schemas")
}

func TestSchema_RequiredOnUpdate_CollectsTaggedFields(t *testing.T) {
	s := Schema{
		Hints: map[string]FieldHint{
			"LoginProfile.Password": {WriteOnly: true, RequiredOnUpdate: true},
			"Code.S3Bucket":         {WriteOnly: true},
		},
	}
	assert.ElementsMatch(t, []string{"LoginProfile.Password"}, s.RequiredOnUpdate())
}

func TestSchema_ParentMappings_Unmarshal(t *testing.T) {
	var s Schema
	raw := `{
		"Identifier": "MountTargetId",
		"Parent": "AWS::EFS::FileSystem",
		"ParentMappings": [
			{"parentProperty": "FileSystemId", "childProperty": "FileSystemId"}
		]
	}`
	err := json.Unmarshal([]byte(raw), &s)
	require.NoError(t, err)
	require.Equal(t, "AWS::EFS::FileSystem", s.Parent)
	require.Len(t, s.ParentMappings, 1)
	require.Equal(t, "FileSystemId", s.ParentMappings[0].ParentProperty)
	require.Equal(t, "FileSystemId", s.ParentMappings[0].ChildProperty)
}

func TestSchema_ParentMappings_Composite(t *testing.T) {
	var s Schema
	raw := `{
		"Identifier": "TaskSetId",
		"Parent": "AWS::ECS::Service",
		"ParentMappings": [
			{"parentProperty": "ServiceName", "childProperty": "Service"},
			{"parentProperty": "Cluster", "childProperty": "Cluster"}
		]
	}`
	err := json.Unmarshal([]byte(raw), &s)
	require.NoError(t, err)
	require.Len(t, s.ParentMappings, 2)
	require.Equal(t, "ServiceName", s.ParentMappings[0].ParentProperty)
	require.Equal(t, "Service", s.ParentMappings[0].ChildProperty)
	require.Equal(t, "Cluster", s.ParentMappings[1].ParentProperty)
	require.Equal(t, "Cluster", s.ParentMappings[1].ChildProperty)
}

func TestSchema_ParentFields_DefaultsForLegacyJSON(t *testing.T) {
	var s Schema
	require.NoError(t, json.Unmarshal([]byte(`{"Identifier":"X"}`), &s))
	require.Equal(t, "", s.Parent)
	require.Nil(t, s.ParentMappings)
}

func TestFieldHintFormatRoundTripsThroughJSON(t *testing.T) {
	raw := `{"Identifier":"X","Fields":["configJson"],"Hints":{"configJson":{"Format":"json"}}}`
	var s Schema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Hints["configJson"].Format; got != "json" {
		t.Fatalf("Format = %q, want json", got)
	}
}

func TestSchemaFormatHints(t *testing.T) {
	s := Schema{Hints: map[string]FieldHint{
		"configJson": {Format: "json"},
		"name":       {CreateOnly: true},
	}}
	fh := s.FormatHints()
	if len(fh) != 1 || fh["configJson"] != "json" {
		t.Fatalf("FormatHints = %v, want {configJson: json}", fh)
	}
}

func TestSchema_OpaqueSelector(t *testing.T) {
	s := Schema{Hints: map[string]FieldHint{
		"SecretString": {Opaque: true},
		"Name":         {Opaque: false},
	}}
	assert.Equal(t, []string{"SecretString"}, s.Opaque())
}
