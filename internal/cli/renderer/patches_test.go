// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/ddddddO/gtree"
	"github.com/stretchr/testify/assert"
)

func TestExtractTagChange(t *testing.T) {
	t.Run("complete tag object for add operation", func(t *testing.T) {
		patch := patchOperation{
			Op:    "add",
			Path:  "/Tags/0",
			Value: map[string]any{"Key": "Environment", "Value": "production"},
		}

		change, err := extractTagChange(patch, nil)

		assert.NoError(t, err)
		assert.Equal(t, "Environment", change.Key)
		assert.Equal(t, "production", change.Value)
		assert.Equal(t, "add", change.Operation)
	})

	t.Run("partial tag value update", func(t *testing.T) {
		oldProps := `{"Tags":[{"Key":"Environment","Value":"dev"}]}`
		patch := patchOperation{
			Op:    "replace",
			Path:  "/Tags/0/Value",
			Value: "production",
		}

		change, err := extractTagChange(patch, json.RawMessage(oldProps))

		assert.NoError(t, err)
		assert.Equal(t, "Environment", change.Key)
		assert.Equal(t, "production", change.Value)
		assert.Equal(t, "dev", change.OldValue)
		assert.True(t, change.HasOld)
	})

	t.Run("returns error for add op with nil value", func(t *testing.T) {
		patch := patchOperation{Op: "add", Path: "/Tags/0", Value: nil}

		_, err := extractTagChange(patch, nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nil value")
	})

	t.Run("tag removal", func(t *testing.T) {
		oldProps := `{"Tags":[{"Key":"Environment","Value":"dev"}]}`
		patch := patchOperation{Op: "remove", Path: "/Tags/0", Value: nil}

		change, err := extractTagChange(patch, json.RawMessage(oldProps))

		assert.NoError(t, err)
		assert.Equal(t, "Environment", change.Key)
		assert.Equal(t, "remove", change.Operation)
	})
}

func TestFormatTagChange(t *testing.T) {
	t.Run("formats add operation", func(t *testing.T) {
		change := TagChange{Operation: "add", Key: "Environment", Value: "prod"}

		result := formatTagChange(change)

		assert.Contains(t, result, `add new Tag "Environment" with the value "prod"`)
	})

	t.Run("formats replace with old value", func(t *testing.T) {
		change := TagChange{
			Operation: "replace",
			Key:       "Environment",
			Value:     "prod",
			OldValue:  "dev",
			HasOld:    true,
		}

		result := formatTagChange(change)

		assert.Contains(t, result, `change Tag "Environment" from "dev" to "prod"`)
	})
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

func TestFormatPropertyChange_OpaqueAdd(t *testing.T) {
	t.Run("opaque add on existing resource shows set with opaque value", func(t *testing.T) {
		change := PropertyChange{
			Path:             "SecretString",
			Value:            "L4clqcm50IFl",
			Operation:        "add",
			IsOpaque:         true,
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `set property "SecretString" (opaque value)`)
		assert.NotContains(t, result, "L4clqcm50IFl")
	})

	t.Run("opaque add on new resource shows add with opaque value", func(t *testing.T) {
		change := PropertyChange{
			Path:      "SecretString",
			Value:     "L4clqcm50IFl",
			Operation: "add",
			IsOpaque:  true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `add new property "SecretString" (opaque value)`)
		assert.NotContains(t, result, "L4clqcm50IFl")
	})

	t.Run("opaque add array entry shows opaque value", func(t *testing.T) {
		change := PropertyChange{
			Path:      "Secrets[0]",
			Value:     "secret123",
			Operation: "add",
			IsOpaque:  true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `add new entry to "Secrets" (opaque value)`)
		assert.NotContains(t, result, "secret123")
	})
}

func TestFormatPropertyChange_WriteOnlyAdd(t *testing.T) {
	t.Run("write-only add on existing resource shows set with write-only", func(t *testing.T) {
		change := PropertyChange{
			Path:             "LoginProfile.Password",
			Value:            "newpass",
			Operation:        "add",
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `set property "LoginProfile.Password" to "newpass" (write-only)`)
		assert.NotContains(t, result, "add new property")
	})

	t.Run("write-only add array entry on existing resource shows set with write-only", func(t *testing.T) {
		change := PropertyChange{
			Path:             "Tokens[0]",
			Value:            "tok-123",
			Operation:        "add",
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `set entry "tok-123" in "Tokens" (write-only)`)
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

func TestFormatPatchDocument_OpaqueWriteOnlyField(t *testing.T) {
	t.Run("opaque write-only field on update shows set with opaque value", func(t *testing.T) {
		node := gtree.NewRoot("")
		patchDoc := []map[string]any{
			{
				"op":    "add",
				"path":  "/SecretString",
				"value": "L4clqcm50IFl",
			},
		}
		serialized, err := json.Marshal(patchDoc)
		assert.NoError(t, err)

		// Properties (desired) contain the opaque Value wrapper
		properties := json.RawMessage(`{"SecretString": {"$value": "L4clqcm50IFl", "$visibility": "Opaque", "$strategy": "Update"}}`)
		// Previous properties (from DB) also have the opaque marker
		previousProperties := json.RawMessage(`{"SecretString": {"$value": "oldhash", "$visibility": "Opaque", "$strategy": "Update"}}`)

		refLabels := make(map[string]string)
		FormatPatchDocument(node, serialized, properties, previousProperties, refLabels, "")

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "opaque value")
		assert.Contains(t, nodes[1].Name(), `set property "SecretString"`)
		assert.NotContains(t, nodes[1].Name(), "L4clqcm50IFl")
	})
}

func TestFormatPatchDocument_RemoveArrayObject_RendersAsJSON(t *testing.T) {
	// Regression test: remove ops on array-of-objects were previously rendered
	// using Go's default map formatter (`map[k:v ...]`), while add ops used
	// JSON. That made array-set diffs visually uncomparable. Both sides must
	// now emit JSON.
	node := gtree.NewRoot("")
	patchDoc := []map[string]any{
		{
			"op":   "remove",
			"path": "/volumeClaimTemplates/0",
		},
	}
	serialized, err := json.Marshal(patchDoc)
	assert.NoError(t, err)

	previousProperties := json.RawMessage(`{
		"volumeClaimTemplates": [
			{"metadata": {"name": "data"}, "spec": {"accessModes": ["ReadWriteOnce"], "volumeMode": "Filesystem"}}
		]
	}`)

	FormatPatchDocument(node, serialized, json.RawMessage("{}"), previousProperties, map[string]string{}, "")

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	// Expect one line like: `remove entry "<json>" from "volumeClaimTemplates"`
	var removeLine string
	for _, n := range nodes {
		if name := n.Name(); strings.Contains(name, "remove entry") {
			removeLine = name
			break
		}
	}
	assert.NotEmpty(t, removeLine, "expected a 'remove entry' line in rendered output")
	assert.Contains(t, removeLine, `"metadata":{"name":"data"}`,
		"remove entry should render the removed object as JSON, not Go map syntax")
	assert.NotContains(t, removeLine, "map[metadata:",
		"remove entry must not leak Go's default map formatter")
}

func TestFormatValueForDisplay_CompositeValuesAsJSON(t *testing.T) {
	t.Run("map renders as JSON", func(t *testing.T) {
		v := map[string]any{"k": "v", "n": float64(1)}
		got := formatValueForDisplay(v)
		// json.Marshal sorts map keys alphabetically, so output is deterministic.
		assert.Equal(t, `{"k":"v","n":1}`, got)
	})

	t.Run("slice renders as JSON", func(t *testing.T) {
		v := []any{"a", float64(2), map[string]any{"k": "v"}}
		got := formatValueForDisplay(v)
		assert.Equal(t, `["a",2,{"k":"v"}]`, got)
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

func TestFormatPatchDocument_TagsPropertyCreated_ShowsTagsAndManagementMessage(t *testing.T) {
	node := gtree.NewRoot("")
	patchDoc := []map[string]any{
		{
			"op":   "add",
			"path": "/Tags",
			"value": []any{
				map[string]any{"Key": "Environment", "Value": "production"},
				map[string]any{"Key": "Team", "Value": "platform-eng"},
			},
		},
	}
	serialized, err := json.Marshal(patchDoc)
	assert.NoError(t, err)

	refLabels := make(map[string]string)
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels, "$unmanaged")

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	// Should have: root node, tags display, and management message
	assert.GreaterOrEqual(t, len(nodes), 2, "Expected at least root + management message")

	nodeNames := make([]string, len(nodes))
	for i, n := range nodes {
		nodeNames[i] = n.Name()
	}

	// Verify "put resource under management" message is present
	hasManagementMessage := false
	for _, name := range nodeNames {
		if contains(name, "put resource under management") {
			hasManagementMessage = true
			break
		}
	}
	assert.True(t, hasManagementMessage, "Should show management message when oldStackName is $unmanaged")
}

func TestFormatPatchDocument_TagsPropertyCreatedWithOnlyCustomTags_ShowsOnlyCustomTags(t *testing.T) {
	node := gtree.NewRoot("")
	patchDoc := []map[string]any{
		{
			"op":   "add",
			"path": "/Tags",
			"value": []any{
				map[string]any{"Key": "Environment", "Value": "staging"},
				map[string]any{"Key": "Owner", "Value": "devops"},
			},
		},
	}
	serialized, err := json.Marshal(patchDoc)
	assert.NoError(t, err)

	refLabels := make(map[string]string)
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels, "")

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	// Should have: root node + custom tags display (no management message)
	assert.GreaterOrEqual(t, len(nodes), 2, "Expected at least root + custom tags")

	nodeNames := make([]string, len(nodes))
	for i, n := range nodes {
		nodeNames[i] = n.Name()
	}

	// Verify NO "put resource under management" message
	hasManagementMessage := false
	for _, name := range nodeNames {
		if contains(name, "put resource under management") {
			hasManagementMessage = true
			break
		}
	}
	assert.False(t, hasManagementMessage, "Should not show management message when no Formae tags")

	// Verify custom tags are shown
	hasCustomTagsOutput := false
	for _, name := range nodeNames {
		if containsAny(name, []string{"Environment", "Owner", "staging", "devops", "Tags"}) {
			hasCustomTagsOutput = true
			break
		}
	}
	assert.True(t, hasCustomTagsOutput, "Should show custom tags in output")
}

// Helper function to check if a string contains any of the given substrings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if contains(s, substr) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findInString(s, substr))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFormatPatchDocument_WithReferences(t *testing.T) {
	t.Run("formats reference values with label mapping", func(t *testing.T) {
		node := gtree.NewRoot("")
		patchDoc := []map[string]any{
			{
				"op":   "add",
				"path": "/VpcId",
				"value": map[string]any{
					"$ref": "formae://ksuid-vpc-123#/VpcId",
				},
			},
		}
		serialized, err := json.Marshal(patchDoc)
		assert.NoError(t, err)

		refLabels := map[string]string{
			"ksuid-vpc-123": "my-vpc",
		}

		FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels, "")

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "my-vpc.VpcId")
	})

	t.Run("formats reference values without label mapping", func(t *testing.T) {
		node := gtree.NewRoot("")
		patchDoc := []map[string]any{
			{
				"op":   "add",
				"path": "/VpcId",
				"value": map[string]any{
					"$ref": "formae://ksuid-vpc-456#/VpcId",
				},
			},
		}
		serialized, err := json.Marshal(patchDoc)
		assert.NoError(t, err)

		refLabels := map[string]string{}

		FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels, "")

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "ksuid-vpc-456.VpcId")
	})
}

func TestFormatPatchDocument_RemoveArrayEntry_ShowsRemovedValue(t *testing.T) {
	t.Run("displays value of removed array entry from previous properties", func(t *testing.T) {
		node := gtree.NewRoot("")

		patchDoc := []map[string]any{
			{
				"op":   "remove",
				"path": "/networks/0",
			},
		}
		serialized, err := json.Marshal(patchDoc)
		assert.NoError(t, err)

		properties := json.RawMessage(`{"networks": ["net-b", "net-c"]}`)
		previousProperties := json.RawMessage(`{"networks": ["net-a", "net-b", "net-c"]}`)

		FormatPatchDocument(node, serialized, properties, previousProperties, nil, "")

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "net-a")
		assert.NotContains(t, nodes[1].Name(), "(empty)")
	})
}

func TestFormatPatchDocument_EmptyPatchWithUnmanagedOldStack_ShowsManagementMessage(t *testing.T) {
	t.Run("empty patch document with $unmanaged oldStackName shows management message", func(t *testing.T) {
		node := gtree.NewRoot("")
		emptyPatchDoc := json.RawMessage("[]")
		properties := json.RawMessage("{}")
		refLabels := map[string]string{}

		FormatPatchDocument(node, emptyPatchDoc, properties, json.RawMessage("{}"), refLabels, "$unmanaged")

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		// Should have root node + management message
		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "put resource under management")
	})

	t.Run("empty patch document without $unmanaged oldStackName shows nothing", func(t *testing.T) {
		node := gtree.NewRoot("")
		emptyPatchDoc := json.RawMessage("[]")
		properties := json.RawMessage("{}")
		refLabels := map[string]string{}

		FormatPatchDocument(node, emptyPatchDoc, properties, json.RawMessage("{}"), refLabels, "some-other-stack")

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		// Should only have root node, no management message
		assert.Len(t, nodes, 1)
	})
}

func collectNodes(it iter.Seq2[*gtree.WalkerNode, error]) ([]*gtree.WalkerNode, error) {
	var nodes []*gtree.WalkerNode
	for node, err := range it {
		if err != nil {
			return nodes, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}
