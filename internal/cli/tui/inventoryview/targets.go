// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// targetRow converts a *pkgmodel.Target into a render-ready row.
// Cells: [Label, Namespace, Discoverable, Config].
// Discoverable is rendered as "yes"/"no"; Config is compacted via compactKV.
func targetRow(t *pkgmodel.Target) row {
	discoverable := "no"
	if t.Discoverable {
		discoverable = "yes"
	}

	return row{
		cells: []string{t.Label, t.Namespace, discoverable, compactKV(t.Config)},
		detail: func(width int) []string {
			return targetDetail(t, width)
		},
	}
}

// targetDetail renders the detail panel for a target.
// Identity lines for Label, Namespace, Discoverable, Version; then Config tree.
func targetDetail(t *pkgmodel.Target, _ int) []string {
	discoverable := "no"
	if t.Discoverable {
		discoverable = "yes"
	}

	// Keys: Label(5), Namespace(9), Discoverable(12), Version(7).
	// Longest = "Discoverable" (12). Format: "%-13s" (key + ":" = 13 chars).
	lines := []string{
		fmt.Sprintf("%-13s %s", "Label:", t.Label),
		fmt.Sprintf("%-13s %s", "Namespace:", t.Namespace),
		fmt.Sprintf("%-13s %s", "Discoverable:", discoverable),
		fmt.Sprintf("%-13s %d", "Version:", t.Version),
	}

	lines = append(lines, "")
	lines = append(lines, "Config:")
	lines = append(lines, jsonTree(t.Config, 1)...)

	return lines
}
