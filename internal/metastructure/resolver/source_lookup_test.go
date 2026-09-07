// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A reference names its source property with a URI fragment, a pre-flattened
// string that is read as a gjson path. A source key that carries a dot — a
// Kubernetes secret's "tls.crt" entry, say — is therefore read as a nested path,
// which misses when only the literal key is present and, on the mixed shape the
// historical corruption leaves behind, silently returns the NESTED value where
// the literal was meant. The fragment names what the author saw, so the literal
// wins.

const (
	literalOnly = `{"data":{"tls.crt":"literal-value"}}`
	nestedOnly  = `{"data":{"tls":{"crt":"nested-value"}}}`
	mixedShape  = `{"data":{"tls.crt":"literal-value","tls":{"crt":"nested-value"}}}`
)

func TestLookupSourceProperty(t *testing.T) {
	cases := []struct {
		name, doc, fragment, want string
	}{
		// The map-shaped secret entry: a plain parent field holding a literal
		// dotted key, which is what secretValue.at("tls.crt") produces.
		{"literal only", literalOnly, "data.tls.crt", "literal-value"},
		{"nested only", nestedOnly, "data.tls.crt", "nested-value"},
		{"mixed: the literal wins", mixedShape, "data.tls.crt", "literal-value"},

		// The whole fragment as one literal key, at the document root.
		{"top-level literal only", `{"tls.crt":"literal-value"}`, "tls.crt", "literal-value"},
		{"top-level nested only", `{"tls":{"crt":"nested-value"}}`, "tls.crt", "nested-value"},
		{
			"top-level mixed: the literal wins",
			`{"tls.crt":"literal-value","tls":{"crt":"nested-value"}}`,
			"tls.crt", "literal-value",
		},

		// The longest literal tail wins: both readings exist, and the more
		// specific literal key is the better candidate for what was named.
		{
			"longest literal tail wins",
			`{"a.b.c":"whole","a":{"b.c":"tail","b":{"c":"nested"}}}`,
			"a.b.c", "whole",
		},
		{
			"then the next longest",
			`{"a":{"b.c":"tail","b":{"c":"nested"}}}`,
			"a.b.c", "tail",
		},

		{"plain key", `{"Arn":"arn:test"}`, "Arn", "arn:test"},
		{"plain nested key", `{"a":{"b":"v"}}`, "a.b", "v"},
		{"plain deep key", `{"a":{"b":{"c":"v"}}}`, "a.b.c", "v"},
		{"array index in the path", `{"a":[{"b":"v"}]}`, "a.0.b", "v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LookupSourceProperty([]byte(tc.doc), tc.fragment)
			require.True(t, got.Exists(), "lookup of %q in %s found nothing", tc.fragment, tc.doc)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestLookupSourceProperty_AbsentAndEmpty(t *testing.T) {
	assert.False(t, LookupSourceProperty([]byte(`{"a":1}`), "missing").Exists())
	assert.False(t, LookupSourceProperty([]byte(`{"a":1}`), "").Exists(),
		"an empty fragment must not resolve to the document root")
	assert.False(t, LookupSourceProperty(nil, "a").Exists())
}

// Opacity classification reads the source through the same lookup, over both
// Properties and ReadOnlyProperties and over every ancestor candidate.
func TestIsSourcePropertyOpaque_DottedSource(t *testing.T) {
	opaqueLeaf := `{"$visibility":"Opaque","$value":"s"}`

	t.Run("literal dotted key is classified", func(t *testing.T) {
		src := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"data":{"tls.crt":` + opaqueLeaf + `}}`),
		}
		assert.True(t, isSourcePropertyOpaque(src, "data.tls.crt"))
	})

	t.Run("literal dotted key in ReadOnlyProperties is classified", func(t *testing.T) {
		src := &pkgmodel.Resource{
			ReadOnlyProperties: json.RawMessage(`{"data":{"tls.crt":` + opaqueLeaf + `}}`),
		}
		assert.True(t, isSourcePropertyOpaque(src, "data.tls.crt"))
	})

	t.Run("a dotted ancestor makes its leaf opaque", func(t *testing.T) {
		src := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"config":{"my.secret":` + opaqueLeaf + `}}`),
		}
		assert.True(t, isSourcePropertyOpaque(src, "config.my.secret.value"),
			"the opaque parent under a dotted key must still be found from a deeper fragment")
	})

	t.Run("mixed shape is decided by the literal", func(t *testing.T) {
		// The literal key is plain; only the nested duplicate is opaque. The
		// literal is what the fragment names, so the answer is "not opaque".
		src := &pkgmodel.Resource{
			Properties: json.RawMessage(`{"data":{"tls.crt":"cleartext","tls":{"crt":` + opaqueLeaf + `}}}`),
		}
		assert.False(t, isSourcePropertyOpaque(src, "data.tls.crt"),
			"the literal key decides the classification")
	})

	t.Run("plain sources are unaffected", func(t *testing.T) {
		src := &pkgmodel.Resource{Properties: json.RawMessage(`{"secret":` + opaqueLeaf + `}`)}
		assert.True(t, isSourcePropertyOpaque(src, "secret"))
		assert.False(t, isSourcePropertyOpaque(src, "Arn"))
	})
}

// Resolved-value extraction reads the named property out of the source document
// the reference resolved to.
func TestExtractResolvedValue_DottedSource(t *testing.T) {
	cases := []struct {
		name, resolved, want string
	}{
		{"literal only", literalOnly, "literal-value"},
		{"nested only", nestedOnly, "nested-value"},
		{"mixed: the literal wins", mixedShape, "literal-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := &propertyResolver{}
			got := pr.extractResolvedValue(pkgmodel.Ref{
				SourcePropertyName: "data.tls.crt",
				ResolvedValue:      pkgmodel.Value{Value: tc.resolved},
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

// End to end: a reference whose SOURCE is a literal dotted key resolves and
// delivers that key's value into the consumer's properties.
func TestReferenceWithDottedSourceDeliversTheLiteralValue(t *testing.T) {
	consumer := json.RawMessage(`{"env":{"$ref":"formae://SECRET#/data.tls.crt","$value":"stale"}}`)

	resolved, err := ResolvePropertyReferences("formae://SECRET#/data.tls.crt", consumer, mixedShape)
	require.NoError(t, err)

	assert.Equal(t, "literal-value", gjson.GetBytes(resolved, "env.$value").String(),
		"the literal key's value must be delivered, got %s", resolved)
}
