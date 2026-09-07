// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// secretAndConsumer builds a source resource whose schema declares both a scalar
// and a map-shaped secret field, and a consumer referencing property on it with
// no author-written $visibility. Opacity has to be inherited from the source's
// schema, which is the only place a secret is declared.
func secretAndConsumer(property string) []*pkgmodel.Resource {
	return []*pkgmodel.Resource{
		{
			Label: "the-secret",
			Type:  "Test::Secret",
			Stack: "default",
			Schema: pkgmodel.Schema{
				Fields: []string{"SecretString", "data"},
				Hints: map[string]pkgmodel.FieldHint{
					"SecretString": {Opaque: true},
					"data":         {Opaque: true},
				},
			},
			Properties: json.RawMessage(`{}`),
		},
		{
			Label: "consumer",
			Type:  "Test::Consumer",
			Stack: "default",
			Properties: json.RawMessage(`{"Password":{"$res":true,"$label":"the-secret",` +
				`"$type":"Test::Secret","$stack":"default","$property":"` + property + `"}}`),
		},
	}
}

// TestMarkInheritedOpaque_ReferenceToSecret covers every shape a secret
// reference takes. Each must come out marked Opaque, because that marking is
// what causes the resolved value to be hashed at rest; a reference that misses
// it persists its secret in cleartext.
func TestMarkInheritedOpaque_ReferenceToSecret(t *testing.T) {
	cases := []struct {
		name     string
		property string
	}{
		// secret.res.secretValue
		{"scalar secret", "SecretString"},
		// secret.res.secretValue.json("password") — $json rides the envelope and
		// leaves the property path alone.
		{"scalar secret with a $json path", "SecretString"},
		// secret.res.secretValue.at("token") — the key is folded into the
		// property path, so the leaf is not itself a declared opaque field.
		{"map secret selecting one key", "data.token"},
		// secret.res.secretValue.at("a.b") — a key containing the separator.
		{"map secret with an escaped key", `data.a\.b`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resources := secretAndConsumer(tc.property)
			markInheritedOpaqueResolvables(resources)

			assert.Equal(t, pkgmodel.VisibilityOpaque,
				gjson.GetBytes(resources[1].Properties, "Password.$visibility").String(),
				"a reference to %q must inherit Opaque, or its resolved value is stored in cleartext; got %s",
				tc.property, resources[1].Properties)
		})
	}
}

// TestMarkInheritedOpaque_LeavesNonSecretReferenceAlone asserts the rule stays
// narrow: a reference to a property that is not opaque, and one whose top-level
// field is not opaque either, are both left untouched.
func TestMarkInheritedOpaque_LeavesNonSecretReferenceAlone(t *testing.T) {
	for _, property := range []string{"Arn", "config.endpoint"} {
		resources := secretAndConsumer(property)
		markInheritedOpaqueResolvables(resources)

		assert.Empty(t, gjson.GetBytes(resources[1].Properties, "Password.$visibility").String(),
			"a reference to non-secret property %q must not be marked Opaque", property)
	}
}

// TestMarkInheritedOpaque_KeepsAuthoredVisibility asserts an explicit visibility
// on the envelope wins, so inheritance never overwrites what the author wrote.
func TestMarkInheritedOpaque_KeepsAuthoredVisibility(t *testing.T) {
	resources := secretAndConsumer("SecretString")
	resources[1].Properties = json.RawMessage(`{"Password":{"$res":true,"$label":"the-secret",` +
		`"$type":"Test::Secret","$stack":"default","$property":"SecretString","$visibility":"Clear"}}`)

	markInheritedOpaqueResolvables(resources)

	assert.Equal(t, pkgmodel.VisibilityClear,
		gjson.GetBytes(resources[1].Properties, "Password.$visibility").String(),
		"an explicitly authored visibility must be preserved")
}
