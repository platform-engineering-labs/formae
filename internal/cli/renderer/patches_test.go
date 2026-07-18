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
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
)

func TestFormatTagChange(t *testing.T) {
	t.Run("formats add operation", func(t *testing.T) {
		change := components.TagChange{Operation: "add", Key: "Environment", Value: "prod"}

		result := formatTagChange(change)

		assert.Contains(t, result, `add new Tag "Environment" with the value "prod"`)
	})

	t.Run("formats replace with old value", func(t *testing.T) {
		change := components.TagChange{
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

func TestFormatPropertyChange_OpaqueAdd(t *testing.T) {
	t.Run("opaque add on existing resource shows set with opaque value", func(t *testing.T) {
		change := components.PropertyChange{
			Path:             "SecretString",
			Value:            "L4clqcm50IFl",
			Operation:        "add",
			IsOpaque:         true,
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `change property "SecretString" (opaque value changed)`)
		assert.NotContains(t, result, "L4clqcm50IFl")
	})

	t.Run("opaque add on new resource shows add with opaque value", func(t *testing.T) {
		change := components.PropertyChange{
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
		change := components.PropertyChange{
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

func TestFormatPropertyChange_ForceResentField(t *testing.T) {
	t.Run("changed force-resent scalar shows the new value only, never the old secret", func(t *testing.T) {
		change := components.PropertyChange{
			Path:             "LoginProfile.Password",
			Value:            "newpass",
			OldValue:         "oldpass",
			HasOld:           true,
			Operation:        "add",
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `change property "LoginProfile.Password" to "newpass"`)
		assert.NotContains(t, result, "oldpass")
		assert.NotContains(t, result, "write-only")
		assert.NotContains(t, result, "add new property")
	})

	t.Run("force-resent scalar with no known previous renders as a plain change", func(t *testing.T) {
		change := components.PropertyChange{
			Path:             "LoginProfile.Password",
			Value:            "newpass",
			Operation:        "add",
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `change property "LoginProfile.Password" to "newpass"`)
		assert.NotContains(t, result, "write-only")
	})

	t.Run("force-resent array entry renders as a change, no write-only jargon", func(t *testing.T) {
		change := components.PropertyChange{
			Path:             "Tokens[0]",
			Value:            "tok-123",
			Operation:        "add",
			ExistsInPrevious: true,
		}

		result := formatPropertyChange(change)

		assert.Contains(t, result, `change entry "tok-123" in "Tokens"`)
		assert.NotContains(t, result, "write-only")
	})
}

func TestHasVisibleChanges(t *testing.T) {
	refLabels := map[string]string{}

	t.Run("all-NoOp force-resend patch has no visible changes", func(t *testing.T) {
		patchDoc := json.RawMessage(`[{"op":"add","path":"/LoginProfile/Password","value":"samepass"}]`)
		prev := json.RawMessage(`{"LoginProfile":{"Password":"samepass"}}`)
		assert.False(t, HasVisibleChanges(patchDoc, json.RawMessage("{}"), prev, refLabels))
	})

	t.Run("a real property change is visible", func(t *testing.T) {
		patchDoc := json.RawMessage(`[{"op":"add","path":"/LoginProfile/Password","value":"newpass"}]`)
		prev := json.RawMessage(`{"LoginProfile":{"Password":"oldpass"}}`)
		assert.True(t, HasVisibleChanges(patchDoc, json.RawMessage("{}"), prev, refLabels))
	})

	t.Run("a tag change is visible", func(t *testing.T) {
		patchDoc := json.RawMessage(`[{"op":"add","path":"/Tags/0","value":{"Key":"k","Value":"v"}}]`)
		assert.True(t, HasVisibleChanges(patchDoc, json.RawMessage("{}"), json.RawMessage("{}"), refLabels))
	})

	t.Run("empty patch has no visible changes", func(t *testing.T) {
		assert.False(t, HasVisibleChanges(json.RawMessage("[]"), json.RawMessage("{}"), json.RawMessage("{}"), refLabels))
	})

	t.Run("a NoOp re-send alongside a real change is still visible", func(t *testing.T) {
		patchDoc := json.RawMessage(`[
			{"op":"add","path":"/LoginProfile/Password","value":"samepass"},
			{"op":"replace","path":"/Description","value":"new"}
		]`)
		prev := json.RawMessage(`{"LoginProfile":{"Password":"samepass"},"Description":"old"}`)
		assert.True(t, HasVisibleChanges(patchDoc, json.RawMessage("{}"), prev, refLabels))
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
		FormatPatchDocument(node, serialized, properties, previousProperties, refLabels)

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "opaque value changed")
		assert.Contains(t, nodes[1].Name(), `change property "SecretString"`)
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

	FormatPatchDocument(node, serialized, json.RawMessage("{}"), previousProperties, map[string]string{})

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

// RFC-0041: the "put resource under management" patch-document subnode is
// gone — formatSimulatedResourceUpdate now emits dedicated `label: <old> -> <new>`
// and `from unmanaged to <stack>` sub-lines on the parent entry, so the
// patch document stays focused on actual property changes.
func TestFormatPatchDocument_TagsPropertyCreated_NoManagementMessage(t *testing.T) {
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
	FormatPatchDocument(node, serialized, json.RawMessage("{}"), json.RawMessage("{}"), refLabels)

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	nodeNames := make([]string, len(nodes))
	for i, n := range nodes {
		nodeNames[i] = n.Name()
	}

	// Tags must still surface in the patch document.
	hasTags := false
	for _, name := range nodeNames {
		if containsAny(name, []string{"Environment", "Team", "production", "platform-eng", "Tags"}) {
			hasTags = true
			break
		}
	}
	assert.True(t, hasTags, "Should still render tag patch entries")

	// "put resource under management" must NOT appear inside the patch document.
	for _, name := range nodeNames {
		assert.False(t, contains(name, "put resource under management"),
			"RFC-0041 dropped the redundant management message from the patch document")
	}
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

		FormatPatchDocument(node, serialized, properties, previousProperties, nil)

		nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
		assert.NoError(t, err)

		assert.Len(t, nodes, 2)
		assert.Contains(t, nodes[1].Name(), "net-a")
		assert.NotContains(t, nodes[1].Name(), "(empty)")
	})
}

// RFC-0041: empty patch must NOT add a child entry. The parent update entry's
// `from unmanaged to <stack>` sub-line (and the `label:` sub-line if a rename
// is happening) cover the transition; the patch document is reserved for
// actual property changes.
func TestFormatPatchDocument_EmptyPatch_NoChildEntries(t *testing.T) {
	node := gtree.NewRoot("")
	emptyPatchDoc := json.RawMessage("[]")
	properties := json.RawMessage("{}")
	refLabels := map[string]string{}

	FormatPatchDocument(node, emptyPatchDoc, properties, json.RawMessage("{}"), refLabels)

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	assert.NoError(t, err)

	// Root only — no "put resource under management" child anymore.
	assert.Len(t, nodes, 1)
}

// TestFormatPatchDocument_CascadeResolvableMarker covers simulate-time
// UX for cascade-updates.
func TestFormatPatchDocument_CascadeResolvableMarker(t *testing.T) {
	node := gtree.NewRoot("")
	patchDoc := json.RawMessage(`[
		{
			"op": "replace",
			"path": "/TaskDefinition",
			"value": {
				"$cascade-resolvable": true,
				"$source-label": "test-taskdef-for-service",
				"$current-value": "arn:aws:ecs:us-east-1:0:task-definition/test:1"
			}
		}
	]`)
	properties := json.RawMessage(`{"TaskDefinition": {"$ref": "formae://x#/TaskDefinitionArn", "$value": "arn:aws:ecs:us-east-1:0:task-definition/test:1"}}`)

	FormatPatchDocument(node, patchDoc, properties, json.RawMessage("{}"), map[string]string{})

	nodes, err := collectNodes(gtree.WalkIterFromRoot(node))
	require.NoError(t, err)
	require.NotEmpty(t, nodes)

	var combined string
	for _, n := range nodes {
		combined += n.Name() + "\n"
	}

	assert.Contains(t, combined, `change property "TaskDefinition" to point at the new test-taskdef-for-service`,
		"renderer must use the friendly cascade-update phrasing, not raw JSON")
	assert.Contains(t, combined, `(current: "arn:aws:ecs:us-east-1:0:task-definition/test:1")`,
		"renderer must surface the current value so the user can compare")
	assert.NotContains(t, combined, "$cascade-resolvable",
		"marker JSON must never leak into user-facing output")
	assert.NotContains(t, combined, "$source-label",
		"marker metadata fields must never leak into user-facing output")
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
