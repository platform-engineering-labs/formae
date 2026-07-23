// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strings"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// policyRow converts a apimodel.PolicyInventoryItem into a render-ready row.
// Cells: [Label, Type, compactKV(Config), attachedStr].
// attachedStr = strings.Join(AttachedStacks, ", ") or "none" when empty.
func policyRow(p apimodel.PolicyInventoryItem) row {
	attached := "none"
	if len(p.AttachedStacks) > 0 {
		attached = strings.Join(p.AttachedStacks, ", ")
	}
	return row{
		cells: []string{p.Label, p.Type, compactKV(p.Config), attached},
		title: fmt.Sprintf("%s (policy)", p.Label),
		detail: func(width int) []string {
			return policyDetail(p, width)
		},
	}
}

// policyDetail renders the detail panel for a policy.
// Identity lines (Label, Type), blank, "Config:" + jsonTree, blank,
// "Attached stacks:" + one "  - <label>" per stack or "  none".
func policyDetail(p apimodel.PolicyInventoryItem, _ int) []string {
	// Keys: Label(5), Type(4). Longest = "Label" (5). Format: "%-6s" (key + ":" = 6 chars).
	lines := []string{
		fmt.Sprintf("%-6s %s", "Label:", p.Label),
		fmt.Sprintf("%-6s %s", "Type:", p.Type),
	}

	lines = append(lines, "")
	lines = append(lines, "Config:")
	lines = append(lines, jsonTree(p.Config, 1)...)

	lines = append(lines, "")
	lines = append(lines, "Attached stacks:")
	if len(p.AttachedStacks) == 0 {
		lines = append(lines, "  none")
	} else {
		for _, s := range p.AttachedStacks {
			lines = append(lines, fmt.Sprintf("  - %s", s))
		}
	}

	return lines
}
