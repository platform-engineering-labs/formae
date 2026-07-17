// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractChanges is the primary TDD entry point for the pure extraction API.
// Expected values are mined from existing extractPropertyChange/extractTagChange
// test fixtures in this package.
func TestExtractChanges(t *testing.T) {
	t.Run("basic replace lands in Properties", func(t *testing.T) {
		// The brief specifies this test verbatim. Note: formatPatchValue returns
		// string values unquoted (the quoting is done by formatPropertyChange in
		// the display layer), so Value/OldValue store the raw string without JSON
		// quotes.
		patch := json.RawMessage(`[{"op":"replace","path":"/InstanceClass","value":"db.t3.large"}]`)
		props := json.RawMessage(`{"InstanceClass":"db.t3.large"}`)
		prev := json.RawMessage(`{"InstanceClass":"db.t3.medium"}`)
		cs, err := ExtractChanges(patch, props, prev, nil)
		require.NoError(t, err)
		require.Len(t, cs.Properties, 1)
		c := cs.Properties[0]
		assert.Equal(t, "InstanceClass", c.Path)
		assert.Equal(t, "db.t3.large", c.Value)
		assert.Equal(t, "db.t3.medium", c.OldValue)
		assert.True(t, c.HasOld)
		assert.False(t, c.NoOp)
	})

	t.Run("tag op lands in Tags not Properties", func(t *testing.T) {
		patch := json.RawMessage(`[{"op":"add","path":"/Tags/0","value":{"Key":"Env","Value":"prod"}}]`)
		cs, err := ExtractChanges(patch, json.RawMessage(`{}`), json.RawMessage(`{}`), nil)
		require.NoError(t, err)
		assert.Len(t, cs.Properties, 0)
		require.Len(t, cs.Tags, 1)
		assert.Equal(t, "Env", cs.Tags[0].Key)
		assert.Equal(t, "prod", cs.Tags[0].Value)
		assert.Equal(t, "add", cs.Tags[0].Operation)
	})

	t.Run("NoOp force-resent field is NoOp true", func(t *testing.T) {
		// Unchanged requiredOnUpdate field: value matches previous → NoOp.
		patch := json.RawMessage(`[{"op":"add","path":"/LoginProfile/Password","value":"samepass"}]`)
		prev := json.RawMessage(`{"LoginProfile":{"Password":"samepass"}}`)
		cs, err := ExtractChanges(patch, json.RawMessage(`{}`), prev, nil)
		require.NoError(t, err)
		require.Len(t, cs.Properties, 1)
		assert.True(t, cs.Properties[0].NoOp)
	})

	t.Run("composite object value renders as compact JSON", func(t *testing.T) {
		patch := json.RawMessage(`[{"op":"replace","path":"/Config","value":{"key":"val","num":1}}]`)
		cs, err := ExtractChanges(patch, json.RawMessage(`{"Config":{"key":"val","num":1}}`), json.RawMessage(`{}`), nil)
		require.NoError(t, err)
		require.Len(t, cs.Properties, 1)
		// formatPatchValue serializes maps to JSON (keys sorted alphabetically by json.Marshal)
		assert.Equal(t, `{"key":"val","num":1}`, cs.Properties[0].Value)
	})

	t.Run("malformed patch returns error", func(t *testing.T) {
		_, err := ExtractChanges(json.RawMessage(`not json`), json.RawMessage(`{}`), json.RawMessage(`{}`), nil)
		require.Error(t, err)
	})

	t.Run("malformed properties returns error", func(t *testing.T) {
		_, err := ExtractChanges(json.RawMessage(`[]`), json.RawMessage(`not json`), json.RawMessage(`{}`), nil)
		require.Error(t, err)
	})

	t.Run("reference values resolve to labels", func(t *testing.T) {
		patch := json.RawMessage(`[{"op":"add","path":"/VpcId","value":{"$ref":"formae://ksuid-vpc-123#/VpcId"}}]`)
		refLabels := map[string]string{"ksuid-vpc-123": "my-vpc"}
		cs, err := ExtractChanges(patch, json.RawMessage(`{"VpcId":{"$ref":"formae://ksuid-vpc-123#/VpcId"}}`), json.RawMessage(`{}`), refLabels)
		require.NoError(t, err)
		require.Len(t, cs.Properties, 1)
		assert.Equal(t, "my-vpc.VpcId", cs.Properties[0].Value)
		assert.True(t, cs.Properties[0].IsRef)
	})

	t.Run("empty patch returns empty ChangeSet", func(t *testing.T) {
		cs, err := ExtractChanges(json.RawMessage(`[]`), json.RawMessage(`{}`), json.RawMessage(`{}`), nil)
		require.NoError(t, err)
		assert.Len(t, cs.Properties, 0)
		assert.Len(t, cs.Tags, 0)
	})
}

// TestMutableChangesForReplace verifies path-subtraction: ops whose path appears
// in createOnlyPatch are excluded; others survive.
func TestMutableChangesForReplace(t *testing.T) {
	t.Run("immutable path is excluded, mutable path survives", func(t *testing.T) {
		// Two ops: /ImmutableField (in createOnlyPatch) and /MutableField (not in it).
		fullPatch := json.RawMessage(`[
			{"op":"replace","path":"/ImmutableField","value":"new-immutable"},
			{"op":"replace","path":"/MutableField","value":"new-mutable"}
		]`)
		createOnlyPatch := json.RawMessage(`[
			{"op":"replace","path":"/ImmutableField","value":"new-immutable"}
		]`)
		props := json.RawMessage(`{"ImmutableField":"new-immutable","MutableField":"new-mutable"}`)
		prev := json.RawMessage(`{"ImmutableField":"old-immutable","MutableField":"old-mutable"}`)

		cs, err := MutableChangesForReplace(fullPatch, createOnlyPatch, props, prev, nil)
		require.NoError(t, err)
		require.Len(t, cs.Properties, 1)
		assert.Equal(t, "MutableField", cs.Properties[0].Path)
	})

	t.Run("when all paths are immutable the ChangeSet is empty", func(t *testing.T) {
		patch := json.RawMessage(`[{"op":"replace","path":"/Immutable","value":"v"}]`)
		createOnlyPatch := json.RawMessage(`[{"op":"replace","path":"/Immutable","value":"v"}]`)
		props := json.RawMessage(`{"Immutable":"v"}`)

		cs, err := MutableChangesForReplace(patch, createOnlyPatch, props, json.RawMessage(`{}`), nil)
		require.NoError(t, err)
		assert.Len(t, cs.Properties, 0)
		assert.Len(t, cs.Tags, 0)
	})

	t.Run("nil createOnlyPatch returns all changes", func(t *testing.T) {
		patch := json.RawMessage(`[{"op":"replace","path":"/Field","value":"v"}]`)
		props := json.RawMessage(`{"Field":"v"}`)
		prev := json.RawMessage(`{"Field":"old"}`)

		cs, err := MutableChangesForReplace(patch, nil, props, prev, nil)
		require.NoError(t, err)
		require.Len(t, cs.Properties, 1)
		assert.Equal(t, "Field", cs.Properties[0].Path)
	})
}
