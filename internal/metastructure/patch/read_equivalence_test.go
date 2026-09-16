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

func TestReadEquivalent_ShapeTheDiffCannotHandleIsAnErrorNotEqual(t *testing.T) {
	ordered := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Items": {UpdateMethod: pkgmodel.FieldUpdateMethodArray}}}
	keyed := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"Items": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"}}}

	for name, tc := range map[string]struct {
		stored, read string
		schema       pkgmodel.Schema
	}{
		"null member in ordered array": {`{"Items":[{"x":1}]}`, `{"Items":[null]}`, ordered},
		"null member in entity set":    {`{"Items":[{"Key":"k"}]}`, `{"Items":[null]}`, keyed},
		"invalid json":                 {`{"a":1}`, `{"a":`, pkgmodel.Schema{}},
	} {
		t.Run(name, func(t *testing.T) {
			equal, err := ReadEquivalent(json.RawMessage(tc.stored), json.RawMessage(tc.read), tc.schema)
			require.Error(t, err)
			assert.False(t, equal)
		})
	}
}
