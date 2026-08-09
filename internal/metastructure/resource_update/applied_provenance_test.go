// © 2025 Platform Engineering Labs Inc.
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

// A write-origin merge (the echo of formae's own Create/Update) records the
// resolution that was sent as $applied alongside the absorbed echo.
func TestMergeRefs_WriteOrigin_StampsApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, true)
	require.NoError(t, err)

	env := gjson.GetBytes(merged, "TargetKeyId")
	assert.Equal(t, "4711", env.Get("$value").String(), "echo absorbed into $value")
	assert.Equal(t, "arn:aws:kms:us-east-1:111122223333:key/4711", env.Get("$applied").String(), "sent resolution kept as $applied")
}

// A read-origin merge never creates $applied.
func TestMergeRefs_ReadOrigin_DoesNotStampApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(merged, "TargetKeyId.$applied").Exists())
}

// Opaque envelopes never receive $applied, even on write-origin merges.
func TestMergeRefs_WriteOrigin_OpaqueEnvelopeExempt(t *testing.T) {
	user := json.RawMessage(`{"Secret": {"$ref": "formae://abc#/Value", "$value": "cleartext", "$visibility": "Opaque"}}`)
	plugin := json.RawMessage(`{"Secret": "cleartext"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Secret"}}, true)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(merged, "Secret.$applied").Exists())
}

// $res envelopes get the same write-origin stamping as $ref envelopes. The
// dispatcher in mergeObject keys on "$res":true (a boolean discriminator,
// not the string "$res"), so the envelope mirrors the real pre-resolution
// shape used elsewhere in this package.
func TestMergeRes_WriteOrigin_StampsApplied(t *testing.T) {
	user := json.RawMessage(`{"Image": {"$res": true, "$label": "the-image", "$type": "FakeAWS::Resource", "$stack": "s", "$property": "id", "$value": "ami-sent"}}`)
	plugin := json.RawMessage(`{"Image": "ami-echoed"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Image"}}, true)
	require.NoError(t, err)
	env := gjson.GetBytes(merged, "Image")
	assert.Equal(t, "ami-echoed", env.Get("$value").String())
	assert.Equal(t, "ami-sent", env.Get("$applied").String())
}

// A write-origin merge with no pre-merge $value (nothing was resolved and
// sent for this path) must not fabricate an $applied baseline.
func TestMergeRefs_WriteOrigin_NoSentValue_NoApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, true)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(merged, "TargetKeyId.$applied").Exists())
}

// A sync read that echoes the same value leaves $applied untouched.
func TestMergeRefs_ReadOrigin_SameEcho_PreservesApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "4711", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(merged, "TargetKeyId.$applied").Exists())
}

// A sync read that adopts a DIFFERENT echo is real out-of-band drift on the
// path: the baseline is invalidated so the next plan falls back to the
// corrective fresh-vs-echo diff.
func TestMergeRefs_ReadOrigin_ChangedEcho_InvalidatesApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "4711", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "9988"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
	require.NoError(t, err)
	env := gjson.GetBytes(merged, "TargetKeyId")
	assert.Equal(t, "9988", env.Get("$value").String())
	assert.False(t, env.Get("$applied").Exists(), "a differing adopted echo must invalidate the baseline")
}

// A plugin that omits the path (or returns null/empty) is an unobservable
// read, not drift: the stored value is kept and $applied survives.
func TestMergeRefs_ReadOrigin_OmittedEcho_PreservesApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "4711", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)

	for _, plugin := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"TargetKeyId": null}`),
		json.RawMessage(`{"TargetKeyId": ""}`),
	} {
		merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
		require.NoError(t, err)
		env := gjson.GetBytes(merged, "TargetKeyId")
		assert.Equal(t, "4711", env.Get("$value").String())
		assert.True(t, env.Get("$applied").Exists(), "unobservable reads must not invalidate: %s", plugin)
	}
}

// A write-origin merge refreshes $applied rather than invalidating it, even
// though the echo differs from the pre-merge $value.
func TestMergeRefs_WriteOrigin_RestampsOverInvalidation(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "arn:aws:kms:us-east-1:111122223333:key/9988", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "9988"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, true)
	require.NoError(t, err)
	env := gjson.GetBytes(merged, "TargetKeyId")
	assert.Equal(t, "9988", env.Get("$value").String())
	assert.Equal(t, "arn:aws:kms:us-east-1:111122223333:key/9988", env.Get("$applied").String())
}
