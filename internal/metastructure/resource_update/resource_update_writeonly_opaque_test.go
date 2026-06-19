// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sha256Hex = regexp.MustCompile(`[0-9a-f]{64}`)

// writeOnlyOpaqueSchema builds a schema with a single writeOnly field plus a
// sibling, mirroring an AWS::SecretsManager::Secret shape.
func writeOnlyOpaqueSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Fields: []string{"secret", "name"},
		Hints: map[string]pkgmodel.FieldHint{
			"secret": {WriteOnly: true},
		},
	}
}

// opsByPath unmarshals a patch document and indexes the ops by RFC-6902 path.
func opsByPath(t *testing.T, patchDoc json.RawMessage) map[string]jsonpatch.JsonPatchOperation {
	t.Helper()
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	byPath := map[string]jsonpatch.JsonPatchOperation{}
	for _, op := range ops {
		byPath[op.Path] = op
	}
	return byPath
}

// genPatchFor runs the same factory-side pipeline that NewResourceUpdateForExisting
// uses: setOnce filter, compute the writeOnly exclude list from the WRAPPED props,
// convert both sides to plugin format, then GeneratePatch with the exclude list.
func genPatchFor(t *testing.T, existing, newRes *pkgmodel.Resource) json.RawMessage {
	t.Helper()
	_, filteredProps, err := resource_update.EnforceSetOnceAndCompareResourceForUpdate(existing, newRes)
	require.NoError(t, err)

	exclude := resource_update.WriteOnlyPathsToExclude(existing.Properties, filteredProps, newRes.Schema.WriteOnly())

	existingPP, err := resolver.ConvertToPluginFormat(existing.Properties)
	require.NoError(t, err)
	newPP, err := resolver.ConvertToPluginFormat(filteredProps)
	require.NoError(t, err)

	patchDoc, createOnlyPatch, err := patch.GeneratePatch(existingPP, newPP,
		resolver.NewResolvableProperties(), newRes.Schema, pkgmodel.FormaApplyModePatch, exclude...)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	return patchDoc
}

// (a) setOnce + opaque value frozen, sibling changes -> no op on value path, no sha256 anywhere.
func TestGeneratePatch_SetOnceOpaqueFrozen_SiblingChange_NoValueOpNoHash(t *testing.T) {
	existing := &pkgmodel.Resource{
		Label:  "r",
		Schema: writeOnlyOpaqueSchema(),
		Properties: json.RawMessage(`{
			"secret": {"$strategy": "SetOnce", "$visibility": "Opaque", "$value": "old-secret"},
			"name": "old"
		}`),
	}
	newRes := &pkgmodel.Resource{
		Label:  "r",
		Schema: writeOnlyOpaqueSchema(),
		Properties: json.RawMessage(`{
			"secret": {"$strategy": "SetOnce", "$visibility": "Opaque", "$value": "rotated-secret"},
			"name": "new"
		}`),
	}

	patchDoc := genPatchFor(t, existing, newRes)
	byPath := opsByPath(t, patchDoc)

	assert.NotContains(t, byPath, "/secret", "frozen setOnce opaque value must not enter the patch")
	require.Contains(t, byPath, "/name", "sibling change must still produce an op")
	assert.NotRegexp(t, sha256Hex, string(patchDoc), "no sha256 hash may appear anywhere in the patch")
}

// (b) Update-strategy + opaque value CHANGED -> patch carries the value path with CLEARTEXT new value.
func TestGeneratePatch_UpdateOpaqueChanged_CarriesCleartext(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"secret"},
		Hints:  map[string]pkgmodel.FieldHint{"secret": {WriteOnly: true}},
	}
	// Stored Opaque value is the hash of the old cleartext (persist-time form).
	storedHash := pkgmodel.ComputeValueHash("old")
	existing := &pkgmodel.Resource{
		Label:      "r",
		Schema:     schema,
		Properties: json.RawMessage(`{"secret": {"$strategy": "Update", "$visibility": "Opaque", "$value": "` + storedHash + `"}}`),
	}
	newRes := &pkgmodel.Resource{
		Label:      "r",
		Schema:     schema,
		Properties: json.RawMessage(`{"secret": {"$strategy": "Update", "$visibility": "Opaque", "$value": "new-cleartext"}}`),
	}

	patchDoc := genPatchFor(t, existing, newRes)
	byPath := opsByPath(t, patchDoc)

	require.Contains(t, byPath, "/secret", "changed Update opaque value must appear on /secret")
	assert.Equal(t, "new-cleartext", byPath["/secret"].Value)
	assert.NotRegexp(t, sha256Hex, string(patchDoc))
}

// (c) Update-strategy + opaque UNCHANGED, sibling change -> no op on value path.
func TestGeneratePatch_UpdateOpaqueUnchanged_SiblingChange_NoValueOp(t *testing.T) {
	// The stored Opaque value is the SHA-256 hash of the cleartext (written by
	// PersistValueTransformer at persist time). The desired side carries the
	// matching cleartext "same", so it is unchanged.
	storedHash := pkgmodel.ComputeValueHash("same")
	existing := &pkgmodel.Resource{
		Label:  "r",
		Schema: writeOnlyOpaqueSchema(),
		Properties: json.RawMessage(`{
			"secret": {"$strategy": "Update", "$visibility": "Opaque", "$value": "` + storedHash + `"},
			"name": "old"
		}`),
	}
	newRes := &pkgmodel.Resource{
		Label:  "r",
		Schema: writeOnlyOpaqueSchema(),
		Properties: json.RawMessage(`{
			"secret": {"$strategy": "Update", "$visibility": "Opaque", "$value": "same"},
			"name": "new"
		}`),
	}

	patchDoc := genPatchFor(t, existing, newRes)
	byPath := opsByPath(t, patchDoc)

	assert.NotContains(t, byPath, "/secret", "unchanged opaque value must not enter the patch")
	require.Contains(t, byPath, "/name", "sibling change must still produce an op")
	assert.NotRegexp(t, sha256Hex, string(patchDoc))
}

// (d) First set (value absent from existing) -> patch carries cleartext value.
func TestGeneratePatch_OpaqueFirstSet_CarriesCleartext(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{"secret"},
		Hints:  map[string]pkgmodel.FieldHint{"secret": {WriteOnly: true}},
	}
	existing := &pkgmodel.Resource{
		Label:      "r",
		Schema:     schema,
		Properties: json.RawMessage(`{}`),
	}
	newRes := &pkgmodel.Resource{
		Label:      "r",
		Schema:     schema,
		Properties: json.RawMessage(`{"secret": {"$strategy": "Update", "$visibility": "Opaque", "$value": "first-cleartext"}}`),
	}

	patchDoc := genPatchFor(t, existing, newRes)
	byPath := opsByPath(t, patchDoc)

	require.Contains(t, byPath, "/secret", "first set of opaque value must appear on /secret")
	assert.Equal(t, "first-cleartext", byPath["/secret"].Value)
	assert.NotRegexp(t, sha256Hex, string(patchDoc))
}

// WriteOnlyPathsToExclude unit coverage: a non-opaque writeOnly field that
// changed must NOT be excluded (no over-stripping of non-opaque writeOnly).
func TestWriteOnlyPathsToExclude_NonOpaqueChanged_NotExcluded(t *testing.T) {
	existing := json.RawMessage(`{"token": {"$visibility": "Clear", "$value": "old"}}`)
	desired := json.RawMessage(`{"token": {"$visibility": "Clear", "$value": "new"}}`)

	exclude := resource_update.WriteOnlyPathsToExclude(existing, desired, []string{"token"})
	assert.NotContains(t, exclude, "token", "changed non-opaque writeOnly field must not be excluded")
}
