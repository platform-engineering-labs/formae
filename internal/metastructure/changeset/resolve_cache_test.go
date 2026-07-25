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
