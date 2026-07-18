// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// RenderSimulationPlain renders a Simulation as a styled but non-interactive
// string suitable for non-TTY output. It reuses simview's internal builders
// (buildSimGroups, renderCard) so the card format is identical to the TUI.
func RenderSimulationPlain(th *theme.Theme, sim *apimodel.Simulation, width int) string {
	groups := buildSimGroups(&sim.Command)
	p := th.Palette
	var sb strings.Builder

	// Summary counts line — same op ordering and colors as renderSummaryCounts.
	counts := opCounts(groups)
	opColor := func(o opKind) lipgloss.AdaptiveColor {
		switch o {
		case opCreate:
			return p.Done
		case opUpdate:
			return p.PrimaryAccent
		case opDelete:
			return p.Warning
		case opReplace:
			return p.SecondaryAccent
		case opDetach:
			return p.TextSecondary
		case opKeep:
			return p.TextSubtle
		}
		return p.TextPrimary
	}
	ordered := []opKind{opCreate, opUpdate, opDelete, opReplace, opDetach, opKeep}
	var parts []string
	for _, op := range ordered {
		n := counts[op]
		if n == 0 {
			continue
		}
		st := lipgloss.NewStyle().Foreground(opColor(op))
		token := st.Render(op.symbol()) + " " + fmt.Sprintf("%d", n) + " " + st.Render(op.word())
		parts = append(parts, token)
	}
	if len(parts) > 0 {
		sb.WriteString(strings.Join(parts, "  "))
		sb.WriteString("\n")
	}

	// Groups: section header + one card per row.
	for _, g := range groups {
		sb.WriteString("\n  ")
		sb.WriteString(components.SectionHeader(th, g.title))
		sb.WriteString("\n")
		for _, r := range g.rows {
			cardLines := renderCard(th, r, width)
			for _, cl := range cardLines {
				sb.WriteString(cl)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}
