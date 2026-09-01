// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"fmt"
	"strconv"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// notApplicable is the cell for a cadence question a generator does not answer:
// it declares no rotation, so nothing advances it on its own and it has no
// rotation history. Distinct from rotationNever, which is a generator that
// WILL rotate and has not yet.
const notApplicable = "-"

// rotationNever is the cell for a generator that declares a cadence but whose
// last rotation has never committed. It is due immediately, which is exactly
// what an operator opening this tab needs to see.
const rotationNever = "never"

// generatorRow converts an apimodel.GeneratorInventoryItem into a render-ready
// row. Cells: [Label, Stack, Type, Every, LastRotated, Destinations].
//
// Every and LastRotated are the pair that answers when this generator last
// rotated and when it next will. LastRotated is derived from command history
// (see apimodel.GeneratorInventoryItem), so it is only meaningful for a
// generator that declares a cadence; one that declares none renders
// notApplicable in both rather than claiming it has never rotated.
//
// Destinations is a count. The labels themselves are in the detail panel: a
// generator can feed many resources, and the number is what belongs in a
// table cell.
func generatorRow(g apimodel.GeneratorInventoryItem, now time.Time) row {
	return row{
		cells: []string{
			g.Label,
			g.Stack,
			g.Type,
			everyCell(g.EverySeconds),
			lastRotatedCell(g, now),
			strconv.Itoa(len(g.Destinations)),
		},
		title: fmt.Sprintf("%s (generator)", g.Label),
		detail: func(width int) []string {
			return generatorDetail(g, now, width)
		},
	}
}

// everyCell renders the rotation cadence, or notApplicable when there is none.
func everyCell(everySeconds int) string {
	if everySeconds <= 0 {
		return notApplicable
	}
	return formatTTLDur(time.Duration(everySeconds) * time.Second)
}

// lastRotatedCell renders the derived instant of the last committed rotation.
func lastRotatedCell(g apimodel.GeneratorInventoryItem, now time.Time) string {
	if g.EverySeconds <= 0 {
		return notApplicable
	}
	if g.LastRotatedAt.IsZero() {
		return rotationNever
	}
	return formatLastReconcileTimeInj(g.LastRotatedAt, now)
}

// generatorDetail renders the detail panel for a generator.
//
// Identity lines (Label, Stack, Type, Every, LastRotated, Generation), blank,
// "Config:" + jsonTree, blank, "Destinations (N):" + one line per bound
// resource naming the stack that holds it, or "  none".
//
// Generation is the identity of the generation the generator currently holds —
// a KSUID minted per draw. It names which generation is live and says nothing
// about its value; the value itself exists in no readable form, and every
// opaque field in Config renders as the mask.
func generatorDetail(g apimodel.GeneratorInventoryItem, now time.Time, _ int) []string {
	// Keys: Label(5), Stack(5), Type(4), Every(5), LastRotated(11),
	// Generation(10). Longest = "LastRotated" (11). Format: "%-13s"
	// (key + ":" = 12 chars, plus one column of padding).
	generation := "none drawn yet"
	if g.GenerationID != "" {
		generation = g.GenerationID
	}

	lines := []string{
		fmt.Sprintf("%-13s %s", "Label:", g.Label),
		fmt.Sprintf("%-13s %s", "Stack:", g.Stack),
		fmt.Sprintf("%-13s %s", "Type:", g.Type),
		fmt.Sprintf("%-13s %s", "Every:", everyCell(g.EverySeconds)),
		fmt.Sprintf("%-13s %s", "LastRotated:", lastRotatedCell(g, now)),
		fmt.Sprintf("%-13s %s", "Generation:", generation),
	}

	lines = append(lines, "")
	lines = append(lines, "Config:")
	lines = append(lines, jsonTree(g.Config, 1)...)

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Destinations (%d):", len(g.Destinations)))
	if len(g.Destinations) == 0 {
		lines = append(lines, "  none")
	} else {
		for _, d := range g.Destinations {
			lines = append(lines, fmt.Sprintf("  - %s (stack %s)", d.ResourceLabel, d.StackLabel))
		}
	}

	return lines
}
