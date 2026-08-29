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
	// The glyph is colored per-op from the theme palette; the count and word
	// stay in the base text color.
	wordSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	ordered := []opKind{opCreate, opUpdate, opDelete, opReplace, opDetach, opKeep}
	var parts []string
	for _, op := range ordered {
		n := counts[op]
		if n == 0 {
			continue
		}
		glyphSt := lipgloss.NewStyle().Foreground(opColor(p, op))
		token := glyphSt.Render(opGlyph(th.Glyphs, op)) + " " + fmt.Sprintf("%d", n) + " " + wordSt.Render(op.word())
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

	if len(sim.SuppressedDrift) > 0 {
		sb.WriteString("\n")
		sb.WriteString(RenderSuppressedDrift(th, sim.SuppressedDrift, width))
	}
	return sb.String()
}

// suppressedDriftLines renders one text line per suppressed-drift note plus a
// trailing explainer chosen by disposition. Values are compacted and
// truncated for display; an opaque note names the path and the fact of
// movement only.
func suppressedDriftLines(notes []apimodel.SuppressedDriftNote) []string {
	compact := func(raw []byte) string {
		s := string(raw)
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > 48 {
			s = s[:45] + "..."
		}
		return s
	}

	var lines []string
	remaining := false
	for _, n := range notes {
		if n.Disposition == "remaining" {
			remaining = true
		}
		if n.Opaque {
			lines = append(lines, fmt.Sprintf("%s/%s %s: changed (opaque value)", n.Stack, n.Label, n.Path))
			continue
		}
		from := "unset"
		if len(n.From) > 0 {
			from = compact(n.From)
		}
		to := "unset"
		if len(n.To) > 0 {
			to = compact(n.To)
		}
		lines = append(lines, fmt.Sprintf("%s/%s %s: %s -> %s", n.Stack, n.Label, n.Path, from, to))
	}
	if remaining {
		lines = append(lines, "This apply will not modify them and nothing else changes, so they stay pending in the drift list.")
	} else {
		lines = append(lines, "This apply will not modify them; completing it absorbs them into the baseline.")
	}
	lines = append(lines, "To manage such a field, declare it in the forma.")
	return lines
}

// RenderSuppressedDrift renders the suppressed-drift notes as a styled,
// non-interactive block: out-of-band changes on provider-defaulted fields
// the forma does not declare, which the plan cannot address.
func RenderSuppressedDrift(th *theme.Theme, notes []apimodel.SuppressedDriftNote, width int) string {
	if len(notes) == 0 {
		return ""
	}
	p := th.Palette
	var sb strings.Builder
	sb.WriteString("  ")
	sb.WriteString(lipgloss.NewStyle().Foreground(p.Warning).Render("Out-of-band changes on provider-defaulted fields this forma does not declare:"))
	sb.WriteString("\n")
	lines := suppressedDriftLines(notes)
	bodySt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	subtleSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	for i, line := range lines {
		st := bodySt
		if i >= len(notes) {
			st = subtleSt
		}
		sb.WriteString("    ")
		sb.WriteString(st.Render(line))
		sb.WriteString("\n")
	}
	return sb.String()
}
