// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
)

// renderSummaryCounts returns the styled op-count summary line.
// Zero-count tokens are omitted. Order: create, update, delete, replace, detach, keep.
func (m Model) renderSummaryCounts() string {
	groups := m.groups
	counts := opCounts(groups)
	p := m.th.Palette

	// The glyph is colored per-op from the theme palette; the count and word
	// stay in the row's base text color.
	wordSt := lipgloss.NewStyle().Foreground(p.TextPrimary)

	ordered := []opKind{opCreate, opUpdate, opDelete, opReplace, opDetach, opKeep}
	var parts []string
	for _, op := range ordered {
		n := counts[op]
		if n == 0 {
			continue
		}
		glyphSt := lipgloss.NewStyle().Foreground(opColor(p, op))
		token := glyphSt.Render(opGlyph(m.th.Glyphs, op)) + " " + fmt.Sprintf("%d", n) + " " + wordSt.Render(op.word())
		parts = append(parts, token)
	}
	return strings.Join(parts, "  ")
}

// renderBody builds the scrollable viewport body and returns the content string
// plus the line index of the cursor row (for scroll-to-cursor).
func (m Model) renderBody() (string, int) {
	var body strings.Builder
	cursorLine := 0
	lineCount := 0
	// navIdx is a running index into the flat nav list. Rendering visits groups
	// and rows in the exact order navLines builds them (one nav entry per row),
	// so a counter incremented per row matches the nav index without an O(n)
	// lookup per row.
	navIdx := 0

	// Destroy cascade warning banner: shown at the top of the viewport when
	// KindDestroy and any resource row has cascade=true.
	if m.opts.Kind == KindDestroy {
		_, cascades := countDestroyResources(m.groups)
		if cascades > 0 {
			bannerText := "Destroying these resources will also delete other resources that depend on them."
			innerW := m.width - 8 // 2 indent + border 2 + padding 2 + 2 margin
			if innerW < 20 {
				innerW = 20
			}
			wrapped := wrapText(bannerText, innerW)
			panelLines := strings.Split(wrapped, "\n")
			panelW := m.width - 4
			if panelW < 24 {
				panelW = 24
			}
			panel := components.Panel(m.th, m.th.Palette.Warning, "Warning", panelLines, panelW)
			body.WriteString("\n")
			lineCount++
			for _, pl := range strings.Split(panel, "\n") {
				body.WriteString("  " + pl + "\n")
				lineCount++
			}
			body.WriteString("\n")
			lineCount++
		}
	}

	for _, g := range m.groups {
		shown := g.rows
		opW, labelW, typeW, stackW := groupLayout(g.kind, m.width)

		// Blank line + section header
		body.WriteString("\n  " + components.SectionHeader(m.th, g.title) + "\n")
		lineCount += 2

		// Column header
		colHdr := m.renderGroupColHeader(g.kind, opW, labelW, typeW, stackW)
		body.WriteString(colHdr + "\n")
		lineCount++

		// Rows
		for _, r := range shown {
			isCursor := navIdx == m.cursor
			if isCursor {
				cursorLine = lineCount
			}

			rowStr := m.renderRow(r, g.kind, opW, labelW, typeW, stackW, isCursor)
			body.WriteString(rowStr)
			lineCount += strings.Count(rowStr, "\n")

			// Inline card expansion: render card below the row when expanded.
			if m.expanded[r.key] {
				cardLines := renderCard(m.th, r, m.width)
				for _, cl := range cardLines {
					body.WriteString(cl + "\n")
					lineCount++
				}
			}
			navIdx++
		}
	}

	return body.String(), cursorLine
}

// simLeadW is the width of the blank column that leads each simulation row,
// aligning the operation column under the state-glyph column of the status
// detail view so the two screens read the same (the status view fills this
// column with the live state glyph; simulation has no state yet).
const simLeadW = 6

// groupLayout returns opW, labelW, typeW, stackW column widths for a group.
// Total used: 2 (indent) + simLeadW + opW + labelW [+ typeW [+ stackW]] = width.
func groupLayout(kind rowKind, w int) (opW, labelW, typeW, stackW int) {
	const opColW = 12
	const indent = 2
	rem := w - indent - simLeadW - opColW
	if rem < 10 {
		rem = 10
	}
	opW = opColW
	switch kind {
	case kindPolicy, kindGenerator:
		labelW = max(rem*2/5, 10)
		typeW = max(rem/5, 8)
		stackW = max(rem-labelW-typeW, 8)
	case kindResource:
		typeW = max(rem*2/5, 14)
		labelW = max(rem-typeW, 10)
	default: // targets, stacks
		labelW = rem
		typeW = 0
		stackW = 0
	}
	return
}

// renderGroupColHeader renders the column header row for a group.
// Plain text is padded before styling — never pad styled strings.
func (m Model) renderGroupColHeader(kind rowKind, opW, labelW, typeW, stackW int) string {
	p := m.th.Palette
	// Inactive headers are dim but still bold, so all column headers read as
	// headers regardless of navigation/sort state.
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary).Bold(true)

	// How the navigated/sorted header is emphasized is theme-driven:
	//   - "background": the navigated column gets a background highlight (like
	//     the row cursor); the active-sort column gets the accent color. The
	//     highlight band uses Selection.Dark explicitly (same documented
	//     compromise as renderRow), so its foreground uses TextPrimary.Dark too
	//     — the band is always dark, so light-side text would be unreadable.
	//   - "brighten" (default for unknown values): navigated OR active-sort
	//     both render bright white, no background — today's quiet/colorblind
	//     behavior.
	background := m.th.Header.Highlight == "background"
	accentStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true)
	hiStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	// hlStyle colours the pending sort-target column (moved by ←/→). It uses the
	// SecondaryAccent foreground in every header mode so it reads as a distinct
	// "cursor" colour — never an underline, and never the Selection background
	// used by the row cursor (which would make the two indistinguishable).
	hlStyle := lipgloss.NewStyle().Foreground(p.SecondaryAccent).Bold(true)

	grpHi := m.sortHi[kind]
	grpAct := m.sortCol[kind]
	grpDir := m.sortDir[kind]

	styleFor := func(isHL, isAct bool) lipgloss.Style {
		switch {
		case isHL:
			// The highlight colour shows wherever the ←/→ cursor is, including on
			// the active sort column (which also keeps its ▲/▼ arrow).
			return hlStyle
		case isAct:
			if background {
				return accentStyle
			}
			return hiStyle
		default:
			return dimStyle
		}
	}

	renderHdr := func(name string, col int, w int) string {
		isHL := col == grpHi
		isAct := col == grpAct
		arrow := ""
		if isAct {
			if grpDir == components.SortDesc {
				arrow = " ▼"
			} else {
				arrow = " ▲"
			}
		}
		text := name + arrow
		padded := components.Pad(text, w) // pad PLAIN text first
		return styleFor(isHL, isAct).Render(padded)
	}
	renderHdrLast := func(name string, col int) string {
		isHL := col == grpHi
		isAct := col == grpAct
		arrow := ""
		if isAct {
			if grpDir == components.SortDesc {
				arrow = " ▼"
			} else {
				arrow = " ▲"
			}
		}
		text := name + arrow
		return styleFor(isHL, isAct).Render(text)
	}

	var sb strings.Builder
	sb.WriteString("  ")
	// Blank lead column, aligning Operation under the status view's state glyph.
	sb.WriteString(strings.Repeat(" ", simLeadW))
	sb.WriteString(renderHdr("Operation", colOp, opW))

	switch kind {
	case kindPolicy, kindGenerator:
		sb.WriteString(renderHdr("Label", colLabel, labelW))
		sb.WriteString(renderHdr("Type", colType, typeW))
		sb.WriteString(renderHdrLast("Stack", colStack))
	case kindResource:
		sb.WriteString(renderHdr("Label", colLabel, labelW))
		sb.WriteString(renderHdrLast("Type", colType))
	default:
		sb.WriteString(renderHdrLast("Label", colLabel))
	}
	return sb.String()
}

// renderRow renders a single sim row (summary, no card expansion).
// Returns the line string including trailing newline(s) for sub-lines.
func (m Model) renderRow(r simRow, kind rowKind, opW, labelW, typeW, stackW int, isCursor bool) string {
	p := m.th.Palette

	// The cursor background uses the .Dark value explicitly (same documented
	// compromise as statuswatch's detailmodel) so the bg-filled trailing spaces
	// and the styled cells always merge to one uniform band. Because that band is
	// always dark, cursor-row foregrounds also resolve to their .Dark side (via
	// fgFor below) so text stays light and readable in light mode instead of
	// resolving to a near-black foreground on the dark band.
	var bg lipgloss.Color
	if isCursor {
		bg = lipgloss.Color(p.Selection.Dark)
	}
	fgFor := func(c lipgloss.AdaptiveColor) lipgloss.TerminalColor {
		if isCursor {
			return lipgloss.Color(c.Dark)
		}
		return c
	}

	// Determine base foreground color for non-operation cells (label/type/stack).
	// The operation cell is colored separately below via opColor.
	var fgColor lipgloss.AdaptiveColor
	if isCursor {
		fgColor = p.TextPrimary
	} else {
		fgColor = p.TextSecondary
	}

	// Label color. Themes decide whether the label is the accent-colored
	// primary identifier (rich) or just another column in the same color as
	// type/stack (quiet — nothing special about a label).
	var labelColor lipgloss.AdaptiveColor
	switch {
	case isCursor:
		labelColor = p.TextPrimary
	case m.th.Rows.LabelAccent:
		labelColor = p.PrimaryAccent
	default:
		labelColor = fgColor
	}

	// Delete rows: when the theme opts in (rich), the whole row takes the
	// delete op color instead of the default label/column colors, so
	// destructive changes stand out. quiet leaves this off — the delete glyph
	// and the word "delete" carry that meaning instead. Applied last so it
	// overrides the isCursor branches above too.
	if r.op == opDelete && m.th.Rows.DeleteWholeRow {
		labelColor = opColor(p, opDelete)
		fgColor = opColor(p, opDelete)
	}

	baseSt := lipgloss.NewStyle().Foreground(fgFor(fgColor))
	labelSt := lipgloss.NewStyle().Foreground(fgFor(labelColor))
	if isCursor {
		baseSt = baseSt.Background(bg)
		labelSt = labelSt.Background(bg)
	}

	// Operation text is colored per-op from the theme palette.
	opSt := lipgloss.NewStyle().Foreground(fgFor(opColor(p, r.op)))
	if isCursor {
		opSt = opSt.Background(bg)
	}

	// Build op plain string and pad
	opPlain := opGlyph(m.th.Glyphs, r.op) + " " + r.op.word()
	opPadded := components.Pad(opPlain, opW)

	trunc := func(s string, maxW int) string {
		if maxW < 4 {
			maxW = 4
		}
		return components.Truncate(s, maxW-1)
	}

	padStr := func(plain string, w int, st lipgloss.Style) string {
		padded := components.Pad(plain, w)
		return st.Render(padded)
	}

	var rowStr string
	indent := "  "
	if isCursor {
		indent = lipgloss.NewStyle().Background(bg).Render("  ")
	}
	// Blank lead column (aligns Operation under the status view's state glyph).
	lead := strings.Repeat(" ", simLeadW)
	if isCursor {
		lead = lipgloss.NewStyle().Background(bg).Render(lead)
	}
	indent += lead

	switch kind {
	case kindPolicy, kindGenerator:
		rowStr = indent +
			opSt.Render(opPadded) +
			padStr(trunc(r.label, labelW), labelW, labelSt) +
			padStr(trunc(r.typ, typeW), typeW, baseSt) +
			baseSt.Render(trunc(r.stack, stackW))
	case kindResource:
		rowStr = indent +
			opSt.Render(opPadded) +
			padStr(trunc(r.label, labelW), labelW, labelSt) +
			baseSt.Render(trunc(r.typ, typeW))
	default: // targets, stacks
		rowStr = indent +
			opSt.Render(opPadded) +
			labelSt.Render(trunc(r.label, labelW))
	}

	// Pad to full width for cursor highlight
	if isCursor {
		rowWidth := lipgloss.Width(rowStr)
		if rowWidth < m.width {
			rowStr += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", m.width-rowWidth))
		}
	}

	result := rowStr + "\n"

	// Sub-lines: rendered as indented secondary lines
	dimSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	if r.cascade && r.cascadeSrc != "" {
		result += "      " + dimSt.Render("depends on: "+r.cascadeSrc) + "\n"
	}
	if r.detail != "" && !r.cascade {
		result += "      " + dimSt.Render(r.detail) + "\n"
	}

	return result
}
