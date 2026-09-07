// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Cascade generation reads the parent through the reference's source-property
// fragment twice: once for the concrete value it puts in the op, and once to ask
// whether that value is a secret. Both must find a source held under a literal
// dotted key, and on the mixed shape both must be decided by the literal.

func dottedSourceParent(dataDoc string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Label:      "tls",
		Type:       "K8s::Core::Secret",
		Ksuid:      "parentksuid",
		Properties: json.RawMessage(dataDoc),
	}
}

func TestCascadeSourceIsOpaque_DottedSource(t *testing.T) {
	opaque := `{"$visibility":"Opaque","$value":"s"}`
	consumer := pkgmodel.Resource{Properties: json.RawMessage(`{"Cert":{"$ref":"formae://parentksuid#/data.tls.crt"}}`)}
	ref := resolver.ResolvableRef{TargetPath: "Cert", SourcePropertyName: "data.tls.crt"}

	t.Run("literal dotted key is classified", func(t *testing.T) {
		parent := dottedSourceParent(`{"data":{"tls.crt":` + opaque + `}}`)
		assert.True(t, cascadeSourceIsOpaque(consumer, ref, parent))
	})

	t.Run("mixed shape is decided by the literal", func(t *testing.T) {
		parent := dottedSourceParent(`{"data":{"tls.crt":"cleartext","tls":{"crt":` + opaque + `}}}`)
		assert.False(t, cascadeSourceIsOpaque(consumer, ref, parent),
			"the literal key decides, and it is not opaque")
	})

	t.Run("nested only still classified", func(t *testing.T) {
		parent := dottedSourceParent(`{"data":{"tls":{"crt":` + opaque + `}}}`)
		assert.True(t, cascadeSourceIsOpaque(consumer, ref, parent))
	})

	t.Run("plain sources are unaffected", func(t *testing.T) {
		plainRef := resolver.ResolvableRef{TargetPath: "Cert", SourcePropertyName: "Password"}
		parent := &pkgmodel.Resource{Properties: json.RawMessage(`{"Password":` + opaque + `}`)}
		assert.True(t, cascadeSourceIsOpaque(consumer, plainRef, parent))
		assert.False(t, cascadeSourceIsOpaque(consumer,
			resolver.ResolvableRef{TargetPath: "Cert", SourcePropertyName: "Name"},
			&pkgmodel.Resource{Properties: json.RawMessage(`{"Name":"db"}`)}))
	})
}

// The concrete-value read reaches the same source through the same helper, so
// the value that lands in a cascade op is the literal key's.
func TestCascadeConcreteValueRead_DottedSource(t *testing.T) {
	cases := []struct {
		name, doc, want string
	}{
		{"literal only", `{"data":{"tls.crt":"literal-value"}}`, "literal-value"},
		{"nested only", `{"data":{"tls":{"crt":"nested-value"}}}`, "nested-value"},
		{
			"mixed: the literal wins",
			`{"data":{"tls.crt":"literal-value","tls":{"crt":"nested-value"}}}`,
			"literal-value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := dottedSourceParent(tc.doc)
			got := resolver.LookupSourceProperty(parent.Properties, "data.tls.crt")
			assert.Equal(t, tc.want, got.String())
		})
	}
}
