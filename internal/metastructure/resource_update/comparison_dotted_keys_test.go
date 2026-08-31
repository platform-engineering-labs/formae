// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A literal map key may contain any character, including the dots and wildcards
// gjson and sjson read as path syntax. The comparison walks address values by
// the paths they build out of those keys, so a key like a Kubernetes annotation
// must be escaped at construction or the walk reads and writes the wrong place.

const dottedKey = "objectset.rio.cattle.io/applied"

func dottedSuppressionSchema() pkgmodel.Schema {
	return pkgmodel.Schema{Fields: []string{"annotations", "name"}}
}

// An unchanged opaque value under a literal dotted key must be suppressed from
// both sides. Unescaped, the delete path addresses a nested tree that does not
// exist and the suppression silently misses.
func TestSuppressUnchangedOpaqueValues_DottedKey(t *testing.T) {
	schema := dottedSuppressionSchema()
	existing := json.RawMessage(`{"annotations":{"` + dottedKey + `":` +
		opaqueLeaf("Update", pkgmodel.ComputeValueHash("s")) + `},"name":"old"}`)
	desired := json.RawMessage(`{"annotations":{"` + dottedKey + `":` +
		opaqueLeaf("Update", "s") + `},"name":"new"}`)

	strippedExisting, strippedDesired, err := SuppressUnchangedOpaqueValues(
		existing, desired, schema, "K8s::Core::ConfigMap")
	require.NoError(t, err)

	assert.False(t, gjson.GetBytes(strippedDesired, gjson.Escape("annotations")+"."+gjson.Escape(dottedKey)).Exists(),
		"unchanged opaque value under a dotted key must be dropped from desired, got %s", strippedDesired)
	assert.False(t, gjson.GetBytes(strippedExisting, gjson.Escape("annotations")+"."+gjson.Escape(dottedKey)).Exists(),
		"and from existing, got %s", strippedExisting)
	assert.Equal(t, "new", gjson.GetBytes(strippedDesired, "name").String(),
		"the sibling field must survive")
}

// The same, for an opaque value under a dotted key inside an array element.
func TestSuppressUnchangedOpaqueValues_DottedKeyInsideArray(t *testing.T) {
	schema := pkgmodel.Schema{Fields: []string{"items", "name"}}
	existing := json.RawMessage(`{"items":[{"` + dottedKey + `":` +
		opaqueLeaf("Update", pkgmodel.ComputeValueHash("s")) + `}],"name":"old"}`)
	desired := json.RawMessage(`{"items":[{"` + dottedKey + `":` +
		opaqueLeaf("Update", "s") + `}],"name":"new"}`)

	_, strippedDesired, err := SuppressUnchangedOpaqueValues(
		existing, desired, schema, "K8s::Core::ConfigMap")
	require.NoError(t, err)

	assert.False(t, gjson.GetBytes(strippedDesired, "items.0."+gjson.Escape(dottedKey)).Exists(),
		"unchanged opaque value under a dotted key in an array must be dropped, got %s", strippedDesired)
}

// A schema-declared opaque field whose own name carries a dot is addressed by
// that name at the traversal root, which bypasses buildPath entirely.
func TestSuppressUnchangedOpaqueValues_DottedTopLevelFieldName(t *testing.T) {
	schema := pkgmodel.Schema{
		Fields: []string{dottedKey, "name"},
		Hints:  map[string]pkgmodel.FieldHint{dottedKey: {Opaque: true}},
	}
	existing := json.RawMessage(`{"` + dottedKey + `":` +
		opaqueLeaf("Update", pkgmodel.ComputeValueHash("s")) + `,"name":"old"}`)
	desired := json.RawMessage(`{"` + dottedKey + `":` +
		opaqueLeaf("Update", "s") + `,"name":"new"}`)

	_, strippedDesired, err := SuppressUnchangedOpaqueValues(
		existing, desired, schema, "K8s::Core::ConfigMap")
	require.NoError(t, err)

	assert.False(t, gjson.GetBytes(strippedDesired, gjson.Escape(dottedKey)).Exists(),
		"unchanged opaque top-level dotted field must be dropped, got %s", strippedDesired)
}

// A SetOnce field under a literal dotted key must be frozen back to its stored
// value in place. Unescaped, the write lands at a nested path and mints the
// exploded duplicate beside the literal key.
func TestFilterSetOnceProps_DottedKey(t *testing.T) {
	existing := json.RawMessage(`{"annotations":{"` + dottedKey + `":` +
		opaqueLeaf("SetOnce", "frozen") + `}}`)
	newProps := json.RawMessage(`{"annotations":{"` + dottedKey + `":` +
		opaqueLeaf("SetOnce", "attempted-rotation") + `}}`)

	filtered, err := filterSetOnceProps(existing, newProps, "r")
	require.NoError(t, err)

	annotations := gjson.GetBytes(filtered, "annotations")
	require.True(t, annotations.IsObject(), "annotations must stay an object, got %s", filtered)
	require.Len(t, annotations.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", filtered)
	require.Contains(t, annotations.Map(), dottedKey, "the literal key must survive, got %s", filtered)

	assert.Equal(t, "frozen",
		gjson.GetBytes(filtered, gjson.Escape("annotations")+"."+gjson.Escape(dottedKey)+".$value").String(),
		"the stored value must be frozen back in place, got %s", filtered)
}

// The same, for a SetOnce field under a dotted key inside an array element.
func TestFilterSetOnceProps_DottedKeyInsideArray(t *testing.T) {
	existing := json.RawMessage(`{"items":[{"` + dottedKey + `":` + opaqueLeaf("SetOnce", "frozen") + `}]}`)
	newProps := json.RawMessage(`{"items":[{"` + dottedKey + `":` + opaqueLeaf("SetOnce", "attempted") + `}]}`)

	filtered, err := filterSetOnceProps(existing, newProps, "r")
	require.NoError(t, err)

	element := gjson.GetBytes(filtered, "items.0")
	require.True(t, element.IsObject(), "the array element must stay an object, got %s", filtered)
	require.Len(t, element.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", filtered)

	assert.Equal(t, "frozen",
		gjson.GetBytes(filtered, "items.0."+gjson.Escape(dottedKey)+".$value").String(),
		"the stored value must be frozen back in place, got %s", filtered)
}

// A top-level SetOnce field whose own name carries a dot is reached from the
// traversal root, which bypasses buildPath.
func TestFilterSetOnceProps_DottedTopLevelFieldName(t *testing.T) {
	existing := json.RawMessage(`{"` + dottedKey + `":` + opaqueLeaf("SetOnce", "frozen") + `}`)
	newProps := json.RawMessage(`{"` + dottedKey + `":` + opaqueLeaf("SetOnce", "attempted") + `}`)

	filtered, err := filterSetOnceProps(existing, newProps, "r")
	require.NoError(t, err)

	parsed := gjson.ParseBytes(filtered)
	require.Len(t, parsed.Map(), 1, "no exploded sibling may appear beside the literal key, got %s", filtered)
	assert.Equal(t, "frozen", gjson.GetBytes(filtered, gjson.Escape(dottedKey)+".$value").String(),
		"the stored value must be frozen back in place, got %s", filtered)
}

// Plain keys must be untouched by the escaping: the paths the walks build for
// them stay byte-identical, so every existing comparison keeps its behavior.
func TestComparisonWalks_PlainKeysUnchanged(t *testing.T) {
	existing := json.RawMessage(`{"spec":{"replicas":` + opaqueLeaf("SetOnce", "3") + `}}`)
	newProps := json.RawMessage(`{"spec":{"replicas":` + opaqueLeaf("SetOnce", "5") + `}}`)

	filtered, err := filterSetOnceProps(existing, newProps, "r")
	require.NoError(t, err)
	assert.Equal(t, "3", gjson.GetBytes(filtered, "spec.replicas.$value").String())

	schema := pkgmodel.Schema{Fields: []string{"secret", "name"}}
	existingOpaque := json.RawMessage(`{"secret":` + opaqueLeaf("Update", pkgmodel.ComputeValueHash("s")) + `,"name":"old"}`)
	desiredOpaque := json.RawMessage(`{"secret":` + opaqueLeaf("Update", "s") + `,"name":"new"}`)
	strippedExisting, strippedDesired, err := SuppressUnchangedOpaqueValues(
		existingOpaque, desiredOpaque, schema, "AWS::SecretsManager::Secret")
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(strippedDesired, "secret").Exists())
	assert.False(t, gjson.GetBytes(strippedExisting, "secret").Exists())
	assert.Equal(t, "new", gjson.GetBytes(strippedDesired, "name").String())
}
