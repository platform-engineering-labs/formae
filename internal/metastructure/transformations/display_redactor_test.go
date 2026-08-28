// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package transformations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func displayOpaqueFields(fields ...string) map[string]bool {
	set := map[string]bool{}
	for _, f := range fields {
		set[f] = true
	}
	return set
}

// A schema-opaque bare scalar (a declared secret before persist-time hashing)
// must leave the projection as an opaque marker, never as its plaintext;
// sibling fields are untouched.
func TestRedactPropertiesForDisplay_SchemaOpaqueScalar(t *testing.T) {
	props := json.RawMessage(`{"Name":"my-secret","SecretString":"hunter2"}`)

	out := RedactPropertiesForDisplay(props, displayOpaqueFields("SecretString"))

	assert.NotContains(t, string(out), "hunter2")
	assert.Equal(t, "my-secret", gjson.GetBytes(out, "Name").String())
	assert.Equal(t, pkgmodel.VisibilityOpaque, gjson.GetBytes(out, "SecretString.$visibility").String())
	assert.Equal(t, pkgmodel.RedactedForLog, gjson.GetBytes(out, "SecretString.$value").String())
}

// A stored opaque envelope projects with its reference metadata intact but
// carries neither its digest nor its resolution provenance: digests live at
// rest, never in an API payload.
func TestRedactPropertiesForDisplay_EnvelopeKeepsMetadataDropsDigests(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("hunter2")
	props := json.RawMessage(`{"Settings":{"url":{
		"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/SecretString",
		"$value":"` + digest + `","$hashed":true,"$visibility":"Opaque",
		"$strategy":"Update","$resolvedFrom":"v1:` + digest + `"}}}`)

	out := RedactPropertiesForDisplay(props, nil)

	assert.NotContains(t, string(out), digest)
	env := gjson.GetBytes(out, "Settings.url")
	assert.Contains(t, env.Get("$ref").String(), "#/SecretString", "reference metadata survives")
	assert.Equal(t, pkgmodel.VisibilityOpaque, env.Get("$visibility").String())
	assert.False(t, env.Get("$resolvedFrom").Exists(), "provenance digests never display")
	assert.Equal(t, pkgmodel.RedactedForLog, env.Get("$value").String())
}

// Non-opaque values are untouched, byte-for-byte semantics preserved.
func TestRedactPropertiesForDisplay_NonOpaqueUntouched(t *testing.T) {
	props := json.RawMessage(`{"Name":"web","Count":3,"Tags":[{"Key":"env","Value":"prod"}]}`)

	out := RedactPropertiesForDisplay(props, displayOpaqueFields("SecretString"))

	var got, want any
	require.NoError(t, json.Unmarshal(out, &got))
	require.NoError(t, json.Unmarshal(props, &want))
	assert.Equal(t, want, got)
}

// A patch op whose path names a schema-opaque field carries the declared
// plaintext at plan time; the projected op keeps the path but withholds the
// value.
func TestRedactPatchDocumentForDisplay_OpaquePathValueWithheld(t *testing.T) {
	patch := json.RawMessage(`[
		{"op":"replace","path":"/SecretString","value":"rotated-plaintext"},
		{"op":"replace","path":"/Description","value":"visible"}
	]`)

	out := RedactPatchDocumentForDisplay(patch, displayOpaqueFields("SecretString"))

	assert.NotContains(t, string(out), "rotated-plaintext")
	assert.Equal(t, pkgmodel.VisibilityOpaque, gjson.GetBytes(out, "0.value.$visibility").String())
	assert.Equal(t, "visible", gjson.GetBytes(out, "1.value").String(), "non-opaque op values are untouched")
	assert.Equal(t, "/SecretString", gjson.GetBytes(out, "0.path").String())
}

// A whole-container op can nest an opaque envelope inside its value; the
// envelope is redacted in place without disturbing its clear siblings.
func TestRedactPatchDocumentForDisplay_NestedEnvelopeInContainerOp(t *testing.T) {
	digest := pkgmodel.ComputeValueHash("hunter2")
	patch := json.RawMessage(`[{"op":"replace","path":"/Settings","value":{
		"recipient":"#infra",
		"url":{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S","$value":"` + digest + `","$hashed":true,"$visibility":"Opaque"}}}]`)

	out := RedactPatchDocumentForDisplay(patch, nil)

	assert.NotContains(t, string(out), digest)
	assert.Equal(t, "#infra", gjson.GetBytes(out, "0.value.recipient").String())
	assert.Equal(t, pkgmodel.RedactedForLog, gjson.GetBytes(out, "0.value.url.$value").String())
}

// A cascade marker op is presentation data constructed with its value already
// withheld for opaque sources; the redactor must not clobber the marker.
func TestRedactPatchDocumentForDisplay_CascadeMarkerUntouched(t *testing.T) {
	patch := json.RawMessage(`[{"op":"replace","path":"/SecretString","value":{
		"$cascade-resolvable":true,"$source-label":"the-source","$current-value":""}}]`)

	out := RedactPatchDocumentForDisplay(patch, displayOpaqueFields("SecretString"))

	assert.True(t, gjson.GetBytes(out, "0.value.$cascade-resolvable").Bool())
	assert.Equal(t, "the-source", gjson.GetBytes(out, "0.value.$source-label").String())
}

// Redaction of presentation data fails closed: input that cannot be decoded
// projects as nothing rather than as a possibly-secret raw payload.
func TestRedactForDisplay_MalformedFailsClosed(t *testing.T) {
	assert.Nil(t, RedactPropertiesForDisplay(json.RawMessage(`{not json`), nil))
	assert.Nil(t, RedactPatchDocumentForDisplay(json.RawMessage(`{not json`), nil))
}
