// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIdentityRule(t *testing.T) {
	tests := []struct {
		name string
		hint FieldHint
		want string
	}{
		{
			name: "EntitySet with IndexField",
			hint: FieldHint{UpdateMethod: FieldUpdateMethodEntitySet, IndexField: "Key", CoOwned: &CoOwnership{}},
			want: "EntitySet/Key",
		},
		{
			name: "Set",
			hint: FieldHint{UpdateMethod: FieldUpdateMethodSet, CoOwned: &CoOwnership{}},
			want: "Set",
		},
		{
			name: "CoOwned with no UpdateMethod is a Mapping",
			hint: FieldHint{CoOwned: &CoOwnership{}},
			want: "Mapping",
		},
		{
			name: "Array is not co-ownable",
			hint: FieldHint{UpdateMethod: FieldUpdateMethodArray},
			want: "",
		},
		{
			name: "Atomic is not co-ownable",
			hint: FieldHint{UpdateMethod: FieldUpdateMethodAtomic},
			want: "",
		},
		{
			name: "zero-value hint is not co-ownable",
			hint: FieldHint{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IdentityRule(tt.hint))
		})
	}
}

func TestMemberIdentities(t *testing.T) {
	tests := []struct {
		name  string
		value string
		hint  FieldHint
		want  []string
	}{
		{
			name:  "object uses keys",
			value: `{"a":1,"b":2}`,
			hint:  FieldHint{CoOwned: &CoOwnership{}},
			want:  []string{"a", "b"},
		},
		{
			name:  "EntitySet array uses json-marshaled IndexField values",
			value: `[{"Key":"k1"},{"Key":"k2"}]`,
			hint:  FieldHint{UpdateMethod: FieldUpdateMethodEntitySet, IndexField: "Key", CoOwned: &CoOwnership{}},
			want:  []string{`"k1"`, `"k2"`},
		},
		{
			name:  "scalar set canonicalizes and dedups each element",
			value: `["x","y","x"]`,
			hint:  FieldHint{UpdateMethod: FieldUpdateMethodSet, CoOwned: &CoOwnership{}},
			want:  []string{`"x"`, `"y"`},
		},
		{
			name:  "resolved ref flattens to its $value",
			value: `[{"$ref":"formae://k/id","$value":"sg-1"}]`,
			hint:  FieldHint{UpdateMethod: FieldUpdateMethodSet, CoOwned: &CoOwnership{}},
			want:  []string{`"sg-1"`},
		},
		{
			name:  "unresolved ref lacking $value contributes nothing",
			value: `[{"$ref":"formae://k/id"}]`,
			hint:  FieldHint{UpdateMethod: FieldUpdateMethodSet, CoOwned: &CoOwnership{}},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MemberIdentities(gjson.Parse(tt.value), tt.hint)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPartitionOwnership(t *testing.T) {
	partition := PartitionOwnership(
		[]string{"a", "b"},
		[]string{"a", "c"},
		[]string{"a", "c", "d"},
	)

	assert.Equal(t, map[string]struct{}{"a": {}, "b": {}}, partition.Declared)
	assert.Equal(t, map[string]struct{}{"c": {}}, partition.FormerlyOwned)
	assert.Equal(t, map[string]struct{}{"d": {}}, partition.NeverOwned)
}

func TestPartitionOwnership_NilPrior(t *testing.T) {
	partition := PartitionOwnership(
		[]string{"a"},
		nil,
		[]string{"a", "d"},
	)

	assert.Empty(t, partition.FormerlyOwned)
	assert.Equal(t, map[string]struct{}{"d": {}}, partition.NeverOwned)
}

func TestNormalizeOwnedMembers_DropsEmptyMembers(t *testing.T) {
	om := OwnedMembers{
		"Rules": {Rule: "Set", Members: []string{}},
	}
	hints := map[string]FieldHint{
		"Rules": {UpdateMethod: FieldUpdateMethodSet, CoOwned: &CoOwnership{}},
	}

	got := NormalizeOwnedMembers(om, hints)
	assert.Nil(t, got)
}

func TestNormalizeOwnedMembers_DropsPathThatLostCoOwned(t *testing.T) {
	om := OwnedMembers{
		"Rules": {Rule: "Set", Members: []string{"m1"}},
	}
	hints := map[string]FieldHint{
		"Rules": {UpdateMethod: FieldUpdateMethodSet}, // CoOwned no longer set
	}

	got := NormalizeOwnedMembers(om, hints)
	assert.Nil(t, got)
}

func TestNormalizeOwnedMembers_SortsAndDedupsMembers(t *testing.T) {
	om := OwnedMembers{
		"Rules": {Rule: "Set", Members: []string{"m2", "m1", "m2"}},
	}
	hints := map[string]FieldHint{
		"Rules": {UpdateMethod: FieldUpdateMethodSet, CoOwned: &CoOwnership{}},
	}

	got := NormalizeOwnedMembers(om, hints)
	require.Contains(t, got, "Rules")
	assert.Equal(t, []string{"m1", "m2"}, got["Rules"].Members)
}

func TestOwnedMembersEqual_NilAndEmptyAreEqual(t *testing.T) {
	assert.True(t, OwnedMembersEqual(nil, OwnedMembers{}))
}

func TestOwnedMembersEqual_NormalizesBeforeComparing(t *testing.T) {
	a := OwnedMembers{"Rules": {Rule: "Set", Members: []string{"m1", "m2"}}}
	b := OwnedMembers{"Rules": {Rule: "Set", Members: []string{"m2", "m1", "m2"}}}
	assert.True(t, OwnedMembersEqual(a, b))
}

func TestOwnedMembersEqual_DifferentMembersAreNotEqual(t *testing.T) {
	a := OwnedMembers{"Rules": {Rule: "Set", Members: []string{"m1"}}}
	b := OwnedMembers{"Rules": {Rule: "Set", Members: []string{"m2"}}}
	assert.False(t, OwnedMembersEqual(a, b))
}

func TestResource_OwnedMembers_JSONRoundTrip(t *testing.T) {
	original := Resource{
		Label: "sg-1",
		OwnedMembers: OwnedMembers{
			"Rules": {Rule: "Set", Members: []string{"m1", "m2"}},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Resource
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original.OwnedMembers, decoded.OwnedMembers)
}

func TestResource_OwnedMembers_AbsentUnmarshalsToNil(t *testing.T) {
	var decoded Resource
	require.NoError(t, json.Unmarshal([]byte(`{"Label":"sg-1"}`), &decoded))
	assert.Nil(t, decoded.OwnedMembers)
}

func TestMemberIdentitiesIncomplete(t *testing.T) {
	setHint := FieldHint{UpdateMethod: FieldUpdateMethodSet}
	esHint := FieldHint{UpdateMethod: FieldUpdateMethodEntitySet, IndexField: "Key"}
	mapHint := FieldHint{}

	cases := []struct {
		name string
		json string
		hint FieldHint
		want bool
	}{
		{"literal set members", `["a","b"]`, setHint, false},
		{"resolved envelope member", `[{"$ref":"formae://resource/x#/Id","$value":"sg-1"}]`, setHint, false},
		{"unresolved ref member", `[{"$ref":"formae://resource/x#/Id"}]`, setHint, true},
		{"unresolved resolvable member", `[{"$res":true,"$type":"String"}]`, setHint, true},
		{"entityset member with key", `[{"Key":"a","Value":"1"}]`, esHint, false},
		{"entityset member missing key", `[{"Value":"1"}]`, esHint, true},
		{"mapping keys always identify", `{"mine":{"$res":true,"$type":"String"}}`, mapHint, false},
		{"whole-field unresolved envelope", `{"$res":true,"$type":"Mapping"}`, mapHint, true},
		{"empty collection", `[]`, setHint, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MemberIdentitiesIncomplete(gjson.Parse(tc.json), tc.hint)
			assert.Equal(t, tc.want, got)
		})
	}
}
