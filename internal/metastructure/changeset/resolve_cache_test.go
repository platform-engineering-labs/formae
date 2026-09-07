// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestPreserveRefMetadata_WrapsSchemaOpaqueBareValue(t *testing.T) {
	r := ResolveCache{}
	orig := pkgmodel.Resource{
		Schema:     pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}}},
		Properties: json.RawMessage(`{"SecretString":"x"}`),
	}
	plugin := gjson.Parse(`{"SecretString":"super-secret"}`)

	out := r.preserveRefMetadata(orig, plugin)

	assert.Equal(t, "Opaque", out.Get("SecretString.$visibility").String())
	assert.Equal(t, "super-secret", out.Get("SecretString.$value").String())
}

func TestPreserveRefMetadata_PreservesEnvelopedOpaqueValue(t *testing.T) {
	r := ResolveCache{}
	orig := pkgmodel.Resource{
		Properties: json.RawMessage(`{"SecretString":{"$value":"x","$visibility":"Opaque"}}`),
	}
	plugin := gjson.Parse(`{"SecretString":"super-secret"}`)

	out := r.preserveRefMetadata(orig, plugin)

	assert.Equal(t, "Opaque", out.Get("SecretString.$visibility").String())
	assert.Equal(t, "super-secret", out.Get("SecretString.$value").String())
}

func TestPreserveRefMetadata_NoOpaqueFields_ReturnsUnchanged(t *testing.T) {
	r := ResolveCache{}
	orig := pkgmodel.Resource{
		Properties: json.RawMessage(`{"Name":"x"}`),
	}
	plugin := gjson.Parse(`{"Name":"plain-value"}`)

	out := r.preserveRefMetadata(orig, plugin)

	assert.Equal(t, "plain-value", out.Get("Name").String())
	assert.False(t, out.Get("Name.$visibility").Exists())
}

func TestResolvedValueAt_MapSecretSubPath(t *testing.T) {
	// preserveRefMetadata wraps an opaque field as {$value:<v>,$visibility:Opaque}.
	// A ref selecting a key of a MAP-shaped opaque secret lives beneath the wrapper
	// at "<field>.$value.<key>"; resolvedValueAt must descend into the envelope and
	// re-wrap the leaf in the same shape a scalar secret produces.
	wrapped := gjson.Parse(`{"decodedData":{"$value":{"username":"admin","password":"p"},"$visibility":"Opaque"}}`)

	v := resolvedValueAt(wrapped, "decodedData.username")
	assert.True(t, v.Exists())
	assert.Equal(t, "admin", v.Get("$value").String())
	assert.Equal(t, "Opaque", v.Get("$visibility").String())

	// A scalar secret whose path IS the field name resolves to its envelope directly.
	scalar := gjson.Parse(`{"SecretString":{"$value":"s","$visibility":"Opaque"}}`)
	sv := resolvedValueAt(scalar, "SecretString")
	assert.True(t, sv.Exists())
	assert.Equal(t, "s", sv.Get("$value").String())

	// A missing key does not resolve.
	assert.False(t, resolvedValueAt(wrapped, "decodedData.nope").Exists())
}
