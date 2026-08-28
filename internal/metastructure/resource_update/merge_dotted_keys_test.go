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

// Map keys are literal JSON keys, not gjson path expressions. A key containing
// dots (e.g. the ubiquitous "app.kubernetes.io/name" Kubernetes label) must
// survive the merge as a single key, not be exploded into nested objects.
func TestMerge_DottedMapKeysStayLiteral(t *testing.T) {
	user := json.RawMessage(`{"spec":{"template":{"metadata":{"labels":{"app.kubernetes.io/name":"nginx"}}}}}`)
	plugin := json.RawMessage(`{"spec":{"template":{"metadata":{"labels":{"app.kubernetes.io/name":"nginx"}}}}}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	labels := out["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)

	assert.Equal(t, "nginx", labels["app.kubernetes.io/name"])
	assert.NotContains(t, labels, "app",
		"dotted key must not be split into a nested object tree")
}

// When the plugin response omits a user-provided field whose key contains dots,
// the kept user value must land under the literal key.
func TestMerge_DottedMapKeyKeptFromUserAtLiteralKey(t *testing.T) {
	user := json.RawMessage(`{"labels":{"app.kubernetes.io/instance":"web"}}`)
	plugin := json.RawMessage(`{"labels":{}}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	labels := out["labels"].(map[string]any)

	assert.Equal(t, "web", labels["app.kubernetes.io/instance"])
	assert.NotContains(t, labels, "app")
}

// Array element matching compares a user element's fields against the plugin
// element's. A field whose key contains dots must be looked up as a literal
// key; otherwise a matching element is declared non-matching and its $ref
// envelope collapses to the plugin's plain scalar, losing the resolvable link.
func TestMerge_ArrayElementMatchingWithDottedKeys(t *testing.T) {
	user := json.RawMessage(`{"rules":[{"app.kubernetes.io/name":"nginx","target":{"$ref":"formae://x#/id","$value":"same"}}]}`)
	plugin := json.RawMessage(`{"rules":[{"app.kubernetes.io/name":"nginx","target":"same"}]}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	target := gjson.GetBytes(merged, "rules.0.target")
	require.True(t, target.IsObject(),
		"element must match on its dotted key so the $ref envelope is preserved")
	assert.Equal(t, "formae://x#/id", target.Get("$ref").String())
}

// Keys containing gjson/sjson wildcard characters must also be treated as
// literal keys.
func TestMerge_WildcardCharactersInMapKeysStayLiteral(t *testing.T) {
	user := json.RawMessage(`{"data":{"glob*key":"a","which?key":"b"}}`)
	plugin := json.RawMessage(`{"data":{}}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{}, false, nil)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(merged, &out))
	data := out["data"].(map[string]any)

	assert.Equal(t, "a", data["glob*key"])
	assert.Equal(t, "b", data["which?key"])
}
