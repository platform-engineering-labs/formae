// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/constants"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// resourceRow converts a pkgmodel.Resource into a render-ready row.
// Cells: [Label, Stack, Type, NativeID].
// Unmanaged resources live on the "$unmanaged" sentinel stack; display it as a
// plain "⚠ unmanaged" (no "$") so it reads as a distinct, non-managed grouping.
func resourceRow(r pkgmodel.Resource) row {
	stackCell := r.Stack
	if r.Stack == constants.UnmanagedStack {
		stackCell = "⚠ unmanaged"
	}

	return row{
		cells: []string{r.Label, stackCell, r.Type, r.NativeID},
		title: fmt.Sprintf("%s (%s)", r.Label, r.Type),
		detail: func(width int) []string {
			return resourceDetail(r, width)
		},
	}
}

// resourceDetail renders the detail panel for a resource.
// Identity lines are key-aligned; a single Properties tree follows, merging the
// desired Properties with the cloud-computed ReadOnlyProperties — the
// distinction is an implementation detail users don't need.
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
	lines = append(lines, jsonTreeMap(mergeProperties(r.Properties, r.ReadOnlyProperties), 1)...)

	return lines
}

// mergeProperties unmarshals and merges the desired and read-only property
// objects into a single map for rendering. Desired properties win on the (rare)
// key collision. Invalid/empty inputs contribute nothing.
func mergeProperties(desired, readOnly json.RawMessage) map[string]any {
	merged := map[string]any{}
	for _, raw := range []json.RawMessage{readOnly, desired} {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}
