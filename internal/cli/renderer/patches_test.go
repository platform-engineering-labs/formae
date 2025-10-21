// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"iter"
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

func TestFormatPatchDocument_TagsPropertyCreated_ReplacesAddOpsForFormaeTagsWithHumanReadableMessage(t *testing.T) {
	node := gtree.NewRoot("")
	patchDoc := []map[string]string{
		{
			"op":    "add",
			"path":  "/Tags",
			"value": `[{"Key":"FormaeResourceLabel","Value":"one-more-buuuuuuucket"},{"Key":"FormaeStackLabel","Value":"buc-ees"}]`,
		},
	}
	serialized, err := json.Marshal(patchDoc)
	assert.NoError(t, err)

	refLabels := make(map[string]string)
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	assert.Len(t, nodes, 2)
	assert.Contains(t, nodes[1].Name(), "put resource under management")
}

func TestFormatPatchDocument_ElementsAddedToTagProperty_ReplacesAddOpsForFormaeTagsWithHumanReadableMessage(t *testing.T) {
	node := gtree.NewRoot("")
	patchDoc := []map[string]any{
		{
			"op":   "add",
			"path": "/Tags/1",
			"value": map[string]any{
				"Key":   "FormaeStackLabel",
				"Value": "buc-ees"},
		},
		{
			"op":   "add",
			"path": "/Tags/2",
			"value": map[string]any{
				"Key":   "FormaeResourceLabel",
				"Value": "one-more-buuuuuuucket",
			},
		},
	}
	serialized, err := json.Marshal(patchDoc)
	assert.NoError(t, err)

	refLabels := make(map[string]string)
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	assert.Len(t, nodes, 2)
	assert.Contains(t, nodes[1].Name(), "put resource under management")
}

func TestFormatPatchDocument_TagsPropertyCreatedWithMixedTags_ShowsBothFormaeMessageAndCustomTags(t *testing.T) {
	node := gtree.NewRoot("")
	patchDoc := []map[string]any{
		{
			"op":   "add",
			"path": "/Tags",
			"value": []any{
				map[string]any{"Key": "FormaeResourceLabel", "Value": "my-resource"},
				map[string]any{"Key": "FormaeStackLabel", "Value": "my-stack"},
				map[string]any{"Key": "Environment", "Value": "production"},
				map[string]any{"Key": "Team", "Value": "platform-eng"},
			},
		},
	}
	serialized, err := json.Marshal(patchDoc)
	assert.NoError(t, err)

	refLabels := make(map[string]string)
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	// Should have: root node, "put resource under management" message, and custom tags display
	assert.GreaterOrEqual(t, len(nodes), 3, "Expected at least root + management message + custom tags")

	nodeNames := make([]string, len(nodes))
	for i, n := range nodes {
		nodeNames[i] = n.Name()
	}

	// Verify "put resource under management" message is present (may contain ANSI color codes)
	hasManagementMessage := false
	for _, name := range nodeNames {
		if contains(name, "put resource under management") {
			hasManagementMessage = true
			break
		}
	}
	assert.True(t, hasManagementMessage, "Should show management message for Formae tags")

	// Verify custom tags are shown (as a Tags property with the custom tags array)
	hasCustomTagsOutput := false
	for _, name := range nodeNames {
		if name != "" && name != "put resource under management" {
			// The custom tags should appear in some form in the output
			if containsAny(name, []string{"Environment", "Team", "production", "platform-eng", "Tags"}) {
				hasCustomTagsOutput = true
				break
			}
		}
	}
	assert.True(t, hasCustomTagsOutput, "Should show custom tags in output")
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
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

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

		FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

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

		FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "ksuid-vpc-456.VpcId")
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
