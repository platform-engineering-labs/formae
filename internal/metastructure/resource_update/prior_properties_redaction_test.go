// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TestStripOpaqueFieldsForPriorProperties_RedactsHashedDigest verifies that
// PriorProperties must never carry a
// stored $hashed digest through to a plugin. This mirrors the shape
// convertResourceForPluginRead produces for a non-enriching secret: once the
// $hashed envelope is unwrapped for plugin format, only a bare 64-hex digest
// remains, indistinguishable from a real value.
func TestStripOpaqueFieldsForPriorProperties_RedactsHashedDigest(t *testing.T) {
	const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a5" // sha256("test")
	props := json.RawMessage(`{"Name":"my-secret","SecretString":"` + digest + `","Description":"unrelated"}`)

	stripped, err := StripOpaqueFieldsForPriorProperties(props, []string{"SecretString"})
	require.NoError(t, err)

	assert.NotContains(t, string(stripped), digest, "the stored digest must not survive in PriorProperties")
	assert.Contains(t, string(stripped), `"$opaque":"redacted"`, "the opaque field must be replaced by the redaction sentinel")
	assert.Contains(t, string(stripped), `"Name":"my-secret"`, "non-opaque fields must be left untouched")
	assert.Contains(t, string(stripped), `"Description":"unrelated"`, "non-opaque fields must be left untouched")
}

// TestStripOpaqueFieldsForPriorProperties_RedactsHashedEnvelope covers the
// still-enveloped form (before convertResourceForPluginRead unwraps $value):
// the entire envelope — including its $hashed marker — must be replaced, not
// merely have its marker dropped.
func TestStripOpaqueFieldsForPriorProperties_RedactsHashedEnvelope(t *testing.T) {
	envelope := (&pkgmodel.Value{Value: "super-secret", Visibility: pkgmodel.VisibilityOpaque}).Hash()
	hashedJSON, err := json.Marshal(map[string]any{
		"$value":      envelope.Value,
		"$visibility": pkgmodel.VisibilityOpaque,
		"$hashed":     true,
	})
	require.NoError(t, err)

	props := json.RawMessage(`{"Name":"my-secret","SecretString":` + string(hashedJSON) + `}`)

	stripped, err := StripOpaqueFieldsForPriorProperties(props, []string{"SecretString"})
	require.NoError(t, err)

	assert.NotContains(t, string(stripped), envelope.Value.(string), "the stored digest must not survive in PriorProperties")
	assert.NotContains(t, string(stripped), `"$hashed"`, "the $hashed marker must not survive in PriorProperties")
}

// TestStripOpaqueFieldsForPriorProperties_NoOpaqueFieldsIsNoop ensures a
// resource with no schema-opaque fields passes through unchanged.
func TestStripOpaqueFieldsForPriorProperties_NoOpaqueFieldsIsNoop(t *testing.T) {
	props := json.RawMessage(`{"Name":"n","Description":"d"}`)
	stripped, err := StripOpaqueFieldsForPriorProperties(props, nil)
	require.NoError(t, err)
	assert.JSONEq(t, string(props), string(stripped))
}

// TestStripOpaqueFieldsForPriorProperties_FieldAbsentIsNoop ensures a
// schema-opaque field that simply isn't present in props (e.g. never set) does
// not cause an error or spuriously add the field.
func TestStripOpaqueFieldsForPriorProperties_FieldAbsentIsNoop(t *testing.T) {
	props := json.RawMessage(`{"Name":"n"}`)
	stripped, err := StripOpaqueFieldsForPriorProperties(props, []string{"SecretString"})
	require.NoError(t, err)
	assert.JSONEq(t, string(props), string(stripped))
}
