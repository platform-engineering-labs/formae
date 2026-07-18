// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ddddddO/gtree"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
)

// FormatPatchDocument formats JSON Patch operations for cli display.
//
// RFC-0041: the "put resource under management" sub-line was removed.
// formatSimulatedResourceUpdate now emits dedicated `label: <old> -> <new>`
// and `from unmanaged to <stack>` sub-lines on the parent update entry, so
// re-stating "put resource under management" inside `by doing the following:`
// is redundant noise. With an empty patch this function emits nothing — the
// parent's sub-lines convey the transition.
func FormatPatchDocument(node *gtree.Node, patchDoc json.RawMessage, properties json.RawMessage, previousProperties json.RawMessage, refLabels map[string]string) {
	cs, err := components.ExtractChanges(patchDoc, properties, previousProperties, refLabels)
	if err != nil {
		node.Add(display.Red("Error processing patch document: " + err.Error()))
		return
	}

	for _, change := range cs.Tags {
		node.Add(formatTagChange(change))
	}
	for _, change := range cs.Properties {
		if !change.NoOp {
			node.Add(formatPropertyChange(change))
		}
	}
}

// HasVisibleChanges reports whether formatting the patch document would emit at
// least one change line. A patch whose only op is a suppressed NoOp (a
// force-resent requiredOnUpdate field whose value is unchanged) renders
// nothing, and the caller must not open an empty "by doing the following:"
// block for it.
func HasVisibleChanges(patchDoc json.RawMessage, properties json.RawMessage, previousProperties json.RawMessage, refLabels map[string]string) bool {
	cs, err := components.ExtractChanges(patchDoc, properties, previousProperties, refLabels)
	if err != nil {
		return true // a malformed patch document surfaces as a visible error line
	}
	if len(cs.Tags) > 0 {
		return true // tag changes always render
	}
	for _, change := range cs.Properties {
		if !change.NoOp {
			return true
		}
	}
	return false
}

// formatTagChange formats a TagChange for display
func formatTagChange(change components.TagChange) string {
	switch change.Operation {
	case "add":
		return display.Green(fmt.Sprintf(`add new Tag "%s" with the value "%s"`, change.Key, change.Value))
	case "remove":
		return display.Red(fmt.Sprintf(`remove Tag "%s"`, change.Key))
	case "replace":
		if change.HasOld {
			return display.Gold(fmt.Sprintf(`change Tag "%s" from "%s" to "%s"`, change.Key, change.OldValue, change.Value))
		} else {
			return display.Gold(fmt.Sprintf(`change Tag "%s" to "%s"`, change.Key, change.Value))
		}
	default:
		return display.Grey(fmt.Sprintf(`%s Tag "%s" with the value"%s"`, change.Operation, change.Key, change.Value))
	}
}

// formatPropertyChange formats a PropertyChange for display
func formatPropertyChange(change components.PropertyChange) string {
	displayPath := stripArrayIndices(change.Path)

	if change.IsCascadeResolvable {
		// Cascade-update where the new value is provider-assigned (e.g.
		// an AWS ARN). Render the friendly form rather than trying to
		// show `from X to Y` with a missing Y.
		if change.CascadeCurrentValue != "" {
			return display.Gold(fmt.Sprintf(`change property "%s" to point at the new %s (current: "%s")`,
				displayPath, change.CascadeSourceLabel, change.CascadeCurrentValue))
		}
		return display.Gold(fmt.Sprintf(`change property "%s" to point at the new %s`,
			displayPath, change.CascadeSourceLabel))
	}

	switch change.Operation {
	case "add":
		if change.IsOpaque {
			if change.ExistsInPrevious {
				return display.Gold(fmt.Sprintf(`change property "%s" (opaque value changed)`, displayPath))
			}
			if isArrayProperty(change.Path) {
				return display.Green(fmt.Sprintf(`add new entry to "%s" (opaque value)`, displayPath))
			}
			return display.Green(fmt.Sprintf(`add new property "%s" (opaque value)`, displayPath))
		}
		if change.ExistsInPrevious {
			if isArrayProperty(change.Path) {
				return display.Gold(fmt.Sprintf(`change entry "%s" in "%s"`, change.Value, displayPath))
			}
			// Force-resent (writeOnly/requiredOnUpdate) field: it surfaces as an
			// "add" because it was stripped before the diff. Show only the new
			// value — the previous value is a secret we must not leak, and it
			// exists here solely for the no-op suppression test.
			return display.Gold(fmt.Sprintf(`change property "%s" to "%s"`, displayPath, change.Value))
		}
		if isArrayProperty(change.Path) {
			return display.Green(fmt.Sprintf(`add new entry "%s" to "%s"`, change.Value, displayPath))
		} else {
			return display.Green(fmt.Sprintf(`add new property "%s" with the value "%s"`, displayPath, change.Value))
		}
	case "remove":
		if isArrayProperty(change.Path) {
			return display.Red(fmt.Sprintf(`remove entry "%s" from "%s"`, change.Value, displayPath))
		} else {
			return display.Red(fmt.Sprintf(`remove property "%s" from "%s"`, change.Value, displayPath))
		}
	case "replace":
		if change.HasOld {
			if change.IsOpaque {
				return display.Gold(fmt.Sprintf(`change property "%s" (opaque value changed)`, displayPath))
			}
			return display.Gold(fmt.Sprintf(`change property "%s" from "%s" to "%s"`, displayPath, change.OldValue, change.Value))
		} else {
			return display.Gold(fmt.Sprintf(`change property "%s" to "%s"`, displayPath, change.Value))
		}
	default:
		return display.Grey(fmt.Sprintf(`%s property "%s"`, change.Operation, displayPath))
	}
}

// isArrayProperty checks if a path contains array indices.
func isArrayProperty(path string) bool {
	return strings.Contains(path, "[") && strings.Contains(path, "]")
}

// stripArrayIndices removes array indices from a path for cleaner display
func stripArrayIndices(path string) string {
	// Remove [index] patterns like "Statement[1]" -> "Statement"
	parts := strings.Split(path, "[")
	if len(parts) == 1 {
		return path // No brackets found
	}

	result := parts[0]
	for i := 1; i < len(parts); i++ {
		// Find closing bracket and append everything after it
		if bracketEnd := strings.Index(parts[i], "]"); bracketEnd != -1 {
			result += parts[i][bracketEnd+1:]
		}
	}
	return result
}
