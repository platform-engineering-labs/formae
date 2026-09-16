// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package patch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestReadEquivalent(t *testing.T) {
	plain := pkgmodel.Schema{}
	ordered := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Items": {UpdateMethod: pkgmodel.FieldUpdateMethodArray}}}
	keyed := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Items": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"}}}
	atomic := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Doc": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic}}}

	cases := []struct {
		name         string
		stored, read string
		schema       pkgmodel.Schema
		want         bool
	}{
		{"identical", `{"a":1}`, `{"a":1}`, plain, true},
		{"unkeyed list reordered", `{"S":["a","b"]}`, `{"S":["b","a"]}`, plain, true},
		{"unkeyed list member changed", `{"S":["a","b"]}`, `{"S":["a","c"]}`, plain, false},
		{"ordered array reordered", `{"Items":["a","b"]}`, `{"Items":["b","a"]}`, ordered, false},
		{"entity set reordered", `{"Items":[{"Key":"k1","V":1},{"Key":"k2","V":2}]}`, `{"Items":[{"Key":"k2","V":2},{"Key":"k1","V":1}]}`, keyed, true},
		{"entity set member attribute changed", `{"Items":[{"Key":"k1","V":1}]}`, `{"Items":[{"Key":"k1","V":2}]}`, keyed, false},
		{"entity set member attribute removed", `{"Items":[{"Key":"k1","V":1}]}`, `{"Items":[{"Key":"k1"}]}`, keyed, false},
		{"key removed", `{"Endpoint":"old"}`, `{}`, plain, false},
		{"key added", `{}`, `{"Endpoint":"new"}`, plain, false},
		{"nested key removed", `{"O":{"a":1,"b":2}}`, `{"O":{"a":1}}`, plain, false},
		{"empty and null are empty objects", ``, `null`, plain, true},
		{"atomic object with null unchanged", `{"Doc":{"Value":null}}`, `{"Doc":{"Value":null}}`, atomic, true},
		{"atomic array with null unchanged", `{"Doc":[null,1]}`, `{"Doc":[null,1]}`, atomic, true},
		{"atomic null unchanged", `{"Doc":null}`, `{"Doc":null}`, atomic, true},
		{"atomic object with null changed", `{"Doc":{"Value":null}}`, `{"Doc":{"Value":1}}`, atomic, false},
		{"null member unchanged in unkeyed list", `{"S":[null,"a"]}`, `{"S":[null,"a"]}`, plain, true},
		{"empty versus populated", ``, `{"a":1}`, plain, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadEquivalent(json.RawMessage(tc.stored), json.RawMessage(tc.read), tc.schema)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestReadEquivalent_NestedHintsApplyInsideCollections(t *testing.T) {
	keyedWithSteps := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
		"Items":       {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		"Items.Steps": {UpdateMethod: pkgmodel.FieldUpdateMethodArray},
	}}
	unkeyedWithSteps := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
		"Rules.Steps": {UpdateMethod: pkgmodel.FieldUpdateMethodArray},
	}}
	cases := []struct {
		name         string
		stored, read string
		schema       pkgmodel.Schema
		want         bool
	}{
		{"ordered list inside entity-set element reordered", `{"Items":[{"Key":"k","Steps":["a","b"]}]}`, `{"Items":[{"Key":"k","Steps":["b","a"]}]}`, keyedWithSteps, false},
		{"ordered list inside entity-set element unchanged, elements reordered", `{"Items":[{"Key":"k1","Steps":["a","b"]},{"Key":"k2","Steps":["c"]}]}`, `{"Items":[{"Key":"k2","Steps":["c"]},{"Key":"k1","Steps":["a","b"]}]}`, keyedWithSteps, true},
		{"ordered list inside unkeyed element reordered", `{"Rules":[{"Steps":["a","b"]}]}`, `{"Rules":[{"Steps":["b","a"]}]}`, unkeyedWithSteps, false},
		{"unkeyed elements reordered with ordered inner lists unchanged", `{"Rules":[{"Steps":["a"]},{"Steps":["b"]}]}`, `{"Rules":[{"Steps":["b"]},{"Steps":["a"]}]}`, unkeyedWithSteps, true},
		{"unhinted nested list reordered is a set", `{"Rules":[{"Tags":["a","b"]}]}`, `{"Rules":[{"Tags":["b","a"]}]}`, pkgmodel.Schema{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadEquivalent(json.RawMessage(tc.stored), json.RawMessage(tc.read), tc.schema)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestReadEquivalent_UnexpectedShapesAreChangesNotErrors(t *testing.T) {
	ordered := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Items": {UpdateMethod: pkgmodel.FieldUpdateMethodArray}}}
	keyed := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Items": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"}}}

	for name, tc := range map[string]struct {
		stored, read string
		schema       pkgmodel.Schema
	}{
		"null member replaces object in ordered array": {`{"Items":[{"x":1}]}`, `{"Items":[null]}`, ordered},
		"null member replaces object in entity set":    {`{"Items":[{"Key":"k"}]}`, `{"Items":[null]}`, keyed},
		"object replaces list":                         {`{"Items":[1]}`, `{"Items":{"a":1}}`, ordered},
	} {
		t.Run(name, func(t *testing.T) {
			equal, err := ReadEquivalent(json.RawMessage(tc.stored), json.RawMessage(tc.read), tc.schema)
			require.NoError(t, err)
			assert.False(t, equal)
		})
	}

	_, err := ReadEquivalent(json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":`), pkgmodel.Schema{})
	require.Error(t, err, "invalid JSON is an error")
}
