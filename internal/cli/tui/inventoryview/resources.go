// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// resourceRow converts a pkgmodel.Resource into a render-ready row.
// Cells: [NativeID, Stack, Type, Label].
// The Stack cell for "unmanaged" carries the "⚠ " prefix; other stacks are verbatim.
func resourceRow(r pkgmodel.Resource) row {
	stackCell := r.Stack
	if r.Stack == "unmanaged" {
		stackCell = "⚠ unmanaged"
	}

	return row{
		cells: []string{r.NativeID, stackCell, r.Type, r.Label},
		title: fmt.Sprintf("%s (%s)", r.Label, r.Type),
		detail: func(width int) []string {
			return resourceDetail(r, width)
		},
	}
}

// resourceDetail renders the detail panel for a resource.
// Identity lines are key-aligned; Properties tree follows, then ReadOnlyProperties
// when non-empty.
func resourceDetail(r pkgmodel.Resource, _ int) []string {
	managed := "no"
	if r.Managed {
		managed = "yes"
	}

	// Identity block — keys padded to one column (longest key = "NativeID" = 8 chars,
	// so "Managed:" also needs alignment to match "NativeID:").
	// Pad format: "%-8s" for the key name (without colon), then ": " separator.
	// Keys: Label(5), Type(4), Stack(5), Target(6), NativeID(8), Managed(7).
	// Longest = "NativeID" (8). Padding: key + ":" left-padded to 9 chars total.
	lines := []string{
		fmt.Sprintf("%-9s %s", "Label:", r.Label),
		fmt.Sprintf("%-9s %s", "Type:", r.Type),
		fmt.Sprintf("%-9s %s", "Stack:", r.Stack),
		fmt.Sprintf("%-9s %s", "Target:", r.Target),
		fmt.Sprintf("%-9s %s", "NativeID:", r.NativeID),
		fmt.Sprintf("%-9s %s", "Managed:", managed),
	}

	lines = append(lines, "")
	lines = append(lines, "Properties:")
	lines = append(lines, jsonTree(r.Properties, 1)...)

	if isNonEmptyJSON(r.ReadOnlyProperties) {
		lines = append(lines, "")
		lines = append(lines, "ReadOnlyProperties:")
		lines = append(lines, jsonTree(r.ReadOnlyProperties, 1)...)
	}

	return lines
}

// isNonEmptyJSON reports whether raw is a non-nil, non-empty JSON value other
// than "null" or "{}".
func isNonEmptyJSON(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	s := strings.TrimSpace(string(raw))
	return s != "null" && s != "{}"
}
