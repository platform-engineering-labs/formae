// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package components

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- private helper tests (relocated from renderer/patches_test.go) ---

// Tags are no longer special-cased — an added tag object flows through the
// generic property path and renders as a readable object (no Tags[key] map
// syntax, no raw JSON), with the array index stripped from the path.
func TestExtractChanges_TagsRenderAsGenericObject(t *testing.T) {
	patchDoc := json.RawMessage(`[{"op":"add","path":"/Tags/1","value":{"Key":"oob-test","Value":"soft-reconcile"}}]`)
	props := json.RawMessage(`{"Tags":[{"Key":"Name","Value":"n"},{"Key":"oob-test","Value":"soft-reconcile"}]}`)

	cs, err := ExtractChanges(patchDoc, props, json.RawMessage(`{}`), nil)
	require.NoError(t, err)
	require.Len(t, cs.Properties, 1)

	ch := cs.Properties[0]
	assert.Equal(t, "add", ch.Operation)
	assert.Equal(t, "Tags[1]", ch.Path)
	// Composite renders readably, not as raw JSON.
	assert.Equal(t, "{Key: \"oob-test\", Value: \"soft-reconcile\"}", ch.Value)

	// The rendered line strips the numeric index and doesn't quote the composite.
	doneSt := lipgloss.NewStyle()
	line := FormatPropertyChange(ch, "", doneSt, doneSt, doneSt)
	assert.Contains(t, line, "add")
	assert.Contains(t, line, "Tags:")
	assert.NotContains(t, line, "Tags[1]")
	assert.Contains(t, line, "{Key: \"oob-test\", Value: \"soft-reconcile\"}")
}

func TestFormatValueForDisplay(t *testing.T) {
	t.Run("opaque value detection", func(t *testing.T) {
		value := map[string]any{
			"$visibility": "Opaque",
			"$value":      "secret",
		}

		got := formatValueForDisplay(value)
		want := "(opaque value)"

		assert.Equal(t, want, got)
	})

	t.Run("value extraction", func(t *testing.T) {
		value := map[string]any{
			"$value": "extracted_value",
		}

		got := formatValueForDisplay(value)
		want := "extracted_value"

		assert.Equal(t, want, got)
	})

	t.Run("regular value fallback", func(t *testing.T) {
		value := "regular_string"

		got := formatValueForDisplay(value)
		want := "regular_string"

		assert.Equal(t, want, got)
	})
}

func TestIsOpaqueProperty(t *testing.T) {
	t.Run("is opaque", func(t *testing.T) {
		properties := json.RawMessage(`{"prop": {"$visibility": "Opaque"}}`)

		got := isOpaqueProperty("prop", properties)

		assert.True(t, got)
	})

	t.Run("is not opaque", func(t *testing.T) {
		properties := json.RawMessage(`{"prop": {"$value": "test"}}`)

		got := isOpaqueProperty("prop", properties)

		assert.False(t, got)
	})

	t.Run("property doesn't exist", func(t *testing.T) {
		properties := json.RawMessage(`{}`)

		got := isOpaqueProperty("missing", properties)

		assert.False(t, got)
	})
}

func TestExtractPropertyChange_ForceResentNoOp(t *testing.T) {
	prev := json.RawMessage(`{"LoginProfile":{"Password":"samepass"}}`)

	t.Run("unchanged force-resend is marked NoOp", func(t *testing.T) {
		patch := patchOperation{Op: "add", Path: "/LoginProfile/Password", Value: "samepass"}
		change, err := extractPropertyChange(patch, map[string]any{}, prev, nil)
		assert.NoError(t, err)
		assert.True(t, change.NoOp, "an add whose value equals the previous value is a no-op force-resend")
	})

	t.Run("changed force-resend is not NoOp and carries the previous value", func(t *testing.T) {
		patch := patchOperation{Op: "add", Path: "/LoginProfile/Password", Value: "newpass"}
		change, err := extractPropertyChange(patch, map[string]any{}, prev, nil)
		assert.NoError(t, err)
		assert.False(t, change.NoOp)
		assert.True(t, change.HasOld)
	})

	t.Run("type-changing re-send is not a NoOp (string vs number)", func(t *testing.T) {
		typed := json.RawMessage(`{"Port":"8080"}`)
		patch := patchOperation{Op: "add", Path: "/Port", Value: float64(8080)}
		change, err := extractPropertyChange(patch, map[string]any{}, typed, nil)
		assert.NoError(t, err)
		assert.False(t, change.NoOp, "a string->number re-send is a real change, not a no-op")
	})

	t.Run("unchanged opaque re-send is a NoOp (unwrap $value)", func(t *testing.T) {
		opaque := json.RawMessage(`{"SecretString":{"$value":"sekret","$visibility":"Opaque"}}`)
		patch := patchOperation{Op: "add", Path: "/SecretString", Value: "sekret"}
		change, err := extractPropertyChange(patch, map[string]any{}, opaque, nil)
		assert.NoError(t, err)
		assert.True(t, change.NoOp, "an opaque re-send whose underlying value is unchanged is a no-op")
	})

	t.Run("changed opaque re-send is not a NoOp", func(t *testing.T) {
		opaque := json.RawMessage(`{"SecretString":{"$value":"old","$visibility":"Opaque"}}`)
		patch := patchOperation{Op: "add", Path: "/SecretString", Value: "new"}
		change, err := extractPropertyChange(patch, map[string]any{}, opaque, nil)
		assert.NoError(t, err)
		assert.False(t, change.NoOp)
	})
}

func TestPropertyExistsInPrevious(t *testing.T) {
	t.Run("property exists", func(t *testing.T) {
		prev := json.RawMessage(`{"SecretString": {"$value": "hash", "$visibility": "Opaque"}}`)

		assert.True(t, propertyExistsInPrevious("SecretString", prev))
	})

	t.Run("property does not exist", func(t *testing.T) {
		prev := json.RawMessage(`{"OtherProp": "value"}`)

		assert.False(t, propertyExistsInPrevious("SecretString", prev))
	})

	t.Run("nested property exists", func(t *testing.T) {
		prev := json.RawMessage(`{"LoginProfile": {"Password": "hash"}}`)

		assert.True(t, propertyExistsInPrevious("LoginProfile/Password", prev))
	})

	t.Run("empty previous properties", func(t *testing.T) {
		assert.False(t, propertyExistsInPrevious("SecretString", nil))
	})
}

func TestFormatValueForDisplay_CompositeValuesReadable(t *testing.T) {
	t.Run("map renders as readable object", func(t *testing.T) {
		v := map[string]any{"k": "v", "n": float64(1)}
		got := formatValueForDisplay(v)
		assert.Equal(t, `{k: "v", n: 1}`, got)
	})

	t.Run("slice renders as readable array", func(t *testing.T) {
		v := []any{"a", float64(2), map[string]any{"k": "v"}}
		got := formatValueForDisplay(v)
		assert.Equal(t, `["a", 2, {k: "v"}]`, got)
	})

	t.Run("scalar still uses %v", func(t *testing.T) {
		assert.Equal(t, "42", formatValueForDisplay(42))
		assert.Equal(t, "hello", formatValueForDisplay("hello"))
	})

	t.Run("opaque Value wrapper still returns opaque marker", func(t *testing.T) {
		v := map[string]any{"$visibility": "Opaque", "$value": "secret"}
		assert.Equal(t, "(opaque value)", formatValueForDisplay(v))
	})
}

func TestFormatReferenceValue(t *testing.T) {
	t.Run("formats KSUID reference with label mapping", func(t *testing.T) {
		ref := "formae://ksuid-12345#/SubnetId"
		refLabels := map[string]string{
			"ksuid-12345": "my-subnet",
		}

		result := formatReferenceValue(ref, refLabels)

		assert.Equal(t, "my-subnet.SubnetId", result)
	})

	t.Run("formats KSUID reference without label mapping", func(t *testing.T) {
		ref := "formae://ksuid-12345#/VpcId"
		refLabels := map[string]string{}

		result := formatReferenceValue(ref, refLabels)

		assert.Equal(t, "ksuid-12345.VpcId", result)
	})

	t.Run("handles malformed reference", func(t *testing.T) {
		ref := "invalid-reference"
		refLabels := map[string]string{}

		result := formatReferenceValue(ref, refLabels)

		assert.Equal(t, "$ref:invalid-reference", result)
	})
}

func TestCleanPatchPath(t *testing.T) {
	t.Run("simple path without indices", func(t *testing.T) {
		assert.Equal(t, "spec.replicas", cleanPatchPath("/spec/replicas"))
	})

	t.Run("single array index", func(t *testing.T) {
		assert.Equal(t, "Tags[3].Value", cleanPatchPath("/Tags/3/Value"))
	})

	t.Run("multiple array indices", func(t *testing.T) {
		assert.Equal(t,
			"spec.template.spec.containers[0].args[0]",
			cleanPatchPath("/spec/template/spec/containers/0/args/0"),
		)
	})

	t.Run("consecutive array indices", func(t *testing.T) {
		assert.Equal(t, "matrix[1][2]", cleanPatchPath("/matrix/1/2"))
	})

	t.Run("path without leading slash", func(t *testing.T) {
		assert.Equal(t, "metadata.name", cleanPatchPath("metadata/name"))
	})

	t.Run("single segment", func(t *testing.T) {
		assert.Equal(t, "name", cleanPatchPath("/name"))
	})

	t.Run("empty path", func(t *testing.T) {
		assert.Equal(t, "", cleanPatchPath(""))
	})

	t.Run("index at end of path", func(t *testing.T) {
		assert.Equal(t, "items[0]", cleanPatchPath("/items/0"))
	})
}

func TestExtractChanges_MovedToComponents(t *testing.T) {
	patch := json.RawMessage(`[{"op":"replace","path":"/BucketName","value":"b2"}]`)
	props := json.RawMessage(`{"BucketName":"b2"}`)
	prev := json.RawMessage(`{"BucketName":"b1"}`)
	cs, err := ExtractChanges(patch, props, prev, nil)
	require.NoError(t, err)
	require.Len(t, cs.Properties, 1)
	assert.Equal(t, "BucketName", cs.Properties[0].Path)
	assert.Equal(t, "b2", cs.Properties[0].Value)
	assert.Equal(t, "b1", cs.Properties[0].OldValue)
}

func TestMutableChangesForReplace_MovedToComponents(t *testing.T) {
	// patchDoc has two ops; createOnlyPatch marks /BucketName as immutable
	patchDoc := json.RawMessage(`[
		{"op":"replace","path":"/BucketName","value":"b2"},
		{"op":"replace","path":"/Region","value":"us-west-2"}
	]`)
	createOnlyPatch := json.RawMessage(`[{"op":"replace","path":"/BucketName","value":"b2"}]`)
	props := json.RawMessage(`{"BucketName":"b2","Region":"us-west-2"}`)
	prev := json.RawMessage(`{"BucketName":"b1","Region":"us-east-1"}`)

	cs, err := MutableChangesForReplace(patchDoc, createOnlyPatch, props, prev, nil)
	require.NoError(t, err)
	// Only Region should appear (BucketName was in createOnlyPatch)
	require.Len(t, cs.Properties, 1)
	assert.Equal(t, "Region", cs.Properties[0].Path)
}

func TestChangeSet_TagChanges(t *testing.T) {
	patchDoc := json.RawMessage(`[{"op":"add","path":"/Tags/0","value":{"Key":"Env","Value":"prod"}}]`)
	props := json.RawMessage(`{"Tags":[{"Key":"Env","Value":"prod"}]}`)
	prev := json.RawMessage(`{}`)

	cs, err := ExtractChanges(patchDoc, props, prev, nil)
	require.NoError(t, err)
	// Tags flow through the generic property path now — no dedicated Tags slice.
	require.Len(t, cs.Properties, 1)
	assert.Equal(t, "add", cs.Properties[0].Operation)
	assert.Equal(t, "Tags[0]", cs.Properties[0].Path)
	assert.Equal(t, `{Key: "Env", Value: "prod"}`, cs.Properties[0].Value)
}
