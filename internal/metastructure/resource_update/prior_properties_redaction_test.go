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
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func opaqueSchema(fields ...string) pkgmodel.Schema {
	hints := make(map[string]pkgmodel.FieldHint, len(fields))
	for _, f := range fields {
		hints[f] = pkgmodel.FieldHint{Opaque: true}
	}
	return pkgmodel.Schema{Hints: hints}
}

// strip runs the redaction with one schema on both sides, which is the ordinary
// case (prior and desired declare the same opaque fields).
func strip(t *testing.T, props string, schema pkgmodel.Schema, resourceType string) string {
	t.Helper()
	stripped, _, err := StripOpaqueFieldsForPriorProperties(json.RawMessage(props), schema, schema, resourceType)
	require.NoError(t, err)
	return string(stripped)
}

// TestStripOpaqueFieldsForPriorProperties_RedactsHashedDigest verifies that
// PriorProperties must never carry a
// stored $hashed digest through to a plugin. This mirrors the shape
// convertResourceForPluginRead produces for a non-enriching secret: once the
// $hashed envelope is unwrapped for plugin format, only a bare 64-hex digest
// remains, indistinguishable from a real value.
func TestStripOpaqueFieldsForPriorProperties_RedactsHashedDigest(t *testing.T) {
	const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a5" // sha256("test")
	props := `{"Name":"my-secret","SecretString":"` + digest + `","Description":"unrelated"}`

	stripped := strip(t, props, opaqueSchema("SecretString"), "")

	assert.NotContains(t, stripped, digest, "the stored digest must not survive in PriorProperties")
	assert.Contains(t, stripped, `"$opaque":"redacted"`, "the opaque field must be replaced by the redaction sentinel")
	assert.Contains(t, stripped, `"Name":"my-secret"`, "non-opaque fields must be left untouched")
	assert.Contains(t, stripped, `"Description":"unrelated"`, "non-opaque fields must be left untouched")
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

	stripped := strip(t, `{"Name":"my-secret","SecretString":`+string(hashedJSON)+`}`,
		opaqueSchema("SecretString"), "")

	assert.NotContains(t, stripped, envelope.Value.(string), "the stored digest must not survive in PriorProperties")
	assert.NotContains(t, stripped, `"$hashed"`, "the $hashed marker must not survive in PriorProperties")
}

// TestStripOpaqueFieldsForPriorProperties_NoOpaqueFieldsIsNoop ensures a
// resource with no schema-opaque fields passes through unchanged.
func TestStripOpaqueFieldsForPriorProperties_NoOpaqueFieldsIsNoop(t *testing.T) {
	props := `{"Name":"n","Description":"d"}`
	assert.JSONEq(t, props, strip(t, props, pkgmodel.Schema{}, ""))
}

// TestStripOpaqueFieldsForPriorProperties_FieldAbsentIsNoop ensures a
// schema-opaque field that simply isn't present in props (e.g. never set) does
// not cause an error or spuriously add the field.
func TestStripOpaqueFieldsForPriorProperties_FieldAbsentIsNoop(t *testing.T) {
	props := `{"Name":"n"}`
	assert.JSONEq(t, props, strip(t, props, opaqueSchema("SecretString"), ""))
}

// After nested fields are hashed at rest, a nested opaque field reaches the
// plugin as a bare digest unless redaction matches the nested path too.
func TestStripOpaqueFieldsForPriorProperties_RedactsNestedPath(t *testing.T) {
	stripped := strip(t, `{"name":"cp","settings":{"host":"smtp.example.com","password":"digest"}}`,
		opaqueSchema("settings.password"), "")

	parsed := gjson.Parse(stripped)
	assert.Equal(t, "redacted", parsed.Get("settings.password.$opaque").String())
	assert.Equal(t, "smtp.example.com", parsed.Get("settings.host").String(), "non-opaque sibling preserved")
	assert.Equal(t, "cp", parsed.Get("name").String())
	assert.NotContains(t, stripped, "digest")
}

// sjson.Set("webhooks.password", …) does not mean what an index-free hint name
// means, which is why the array case needed the shared walker.
func TestStripOpaqueFieldsForPriorProperties_RedactsEveryArrayElement(t *testing.T) {
	stripped := strip(t, `{"webhooks":[{"url":"u1","password":"d1"},{"url":"u2","password":"d2"}]}`,
		opaqueSchema("webhooks.password"), "")

	parsed := gjson.Parse(stripped)
	for i, url := range []string{"u1", "u2"} {
		assert.Equal(t, "redacted", parsed.Get("webhooks").Array()[i].Get("password.$opaque").String())
		assert.Equal(t, url, parsed.Get("webhooks").Array()[i].Get("url").String())
	}
	assert.NotContains(t, stripped, "d1")
	assert.NotContains(t, stripped, "d2")
}

func TestStripOpaqueFieldsForPriorProperties_ParentHintWins(t *testing.T) {
	stripped := strip(t, `{"settings":{"user":"admin","password":"digest"}}`,
		opaqueSchema("settings", "settings.password"), "")

	parsed := gjson.Parse(stripped)
	assert.Equal(t, "redacted", parsed.Get("settings.$opaque").String())
	assert.NotContains(t, stripped, "admin", "the whole declared secret is redacted, not just its descendant")
}

// The caller used to pass only the desired schema's declared opaque fields, so
// the agent-side known-opaque table was hashed at rest but its digest still
// reached the plugin — exactly the plugin-schema-drops-Opaque case the table
// exists for.
func TestStripOpaqueFieldsForPriorProperties_HonoursKnownOpaqueTable(t *testing.T) {
	stripped := strip(t, `{"Name":"n","SecretString":"digest"}`,
		pkgmodel.Schema{}, "AWS::SecretsManager::Secret")

	assert.Equal(t, "redacted", gjson.Get(stripped, "SecretString.$opaque").String())
	assert.NotContains(t, stripped, "digest")
	assert.Contains(t, stripped, `"Name":"n"`)
}

// A hint removed or renamed between prior and desired would otherwise expose a
// value that was opaque when it was stored.
func TestStripOpaqueFieldsForPriorProperties_UnionsPriorAndDesiredSchemas(t *testing.T) {
	props := json.RawMessage(`{"Retired":"digest","Current":"other-digest"}`)

	stripped, _, err := StripOpaqueFieldsForPriorProperties(props,
		opaqueSchema("Retired"), opaqueSchema("Current"), "")
	require.NoError(t, err)

	assert.Equal(t, "redacted", gjson.GetBytes(stripped, "Retired.$opaque").String())
	assert.Equal(t, "redacted", gjson.GetBytes(stripped, "Current.$opaque").String())
	assert.NotContains(t, string(stripped), "digest")
}

// Redaction runs on what convertResourceForPluginRead actually produces, not
// only on hand-written input: conversion unwraps the stored envelope, and it is
// the unwrapped shape that must still be caught.
func TestStripOpaqueFieldsForPriorProperties_RedactsConvertedPriorState(t *testing.T) {
	envelope := (&pkgmodel.Value{Value: "super-secret", Visibility: pkgmodel.VisibilityOpaque}).Hash()
	hashedJSON, err := json.Marshal(map[string]any{
		"$value":      envelope.Value,
		"$visibility": pkgmodel.VisibilityOpaque,
		"$strategy":   pkgmodel.StrategyUpdate,
		"$hashed":     true,
	})
	require.NoError(t, err)

	schema := opaqueSchema("settings.password")
	prior := pkgmodel.Resource{
		Label:      "cp",
		Type:       "contactpoint.grafana",
		Schema:     schema,
		Properties: json.RawMessage(`{"settings":{"host":"h","password":` + string(hashedJSON) + `}}`),
	}

	converted, err := convertResourceForPluginRead(prior)
	require.NoError(t, err)

	stripped, _, err := StripOpaqueFieldsForPriorProperties(converted.Properties, schema, schema, prior.Type)
	require.NoError(t, err)

	assert.Equal(t, "redacted", gjson.GetBytes(stripped, "settings.password.$opaque").String())
	assert.NotContains(t, string(stripped), envelope.Value.(string),
		"the bare digest conversion leaves behind must not reach the plugin")
	assert.Equal(t, "h", gjson.GetBytes(stripped, "settings.host").String())
}
