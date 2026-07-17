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
)

// renderSummaryCounts returns the styled op-count summary line.
// Zero-count tokens are omitted. Order: create, update, delete, replace, detach, keep.
func (m Model) renderSummaryCounts() string {
	groups := m.groups
	counts := opCounts(groups)
	p := m.th.Palette

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
	return strings.Join(parts, "  ")
}

// renderBody builds the scrollable viewport body and returns the content string
// plus the line index of the cursor row (for scroll-to-cursor).
func (m Model) renderBody() (string, int) {
	var body strings.Builder
	nav := m.navLines()
	cursorLine := 0
	lineCount := 0

	for _, g := range m.groups {
		lim := m.visible[g.kind]
		shown, remaining := simVisibleRows(g, lim)
		opW, labelW, typeW, stackW := groupLayout(g.kind, m.width)

		// Blank line + section header
		body.WriteString("\n  " + components.SectionHeader(m.th, g.title) + "\n")
		lineCount += 2

		// Column header
		colHdr := m.renderGroupColHeader(g.kind, opW, labelW, typeW, stackW)
		body.WriteString(colHdr + "\n")
		lineCount++

		// Rows
		for i, r := range shown {
			navIdx := findNavIndex(nav, g.kind, r.key, i)
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
		}

		// Show-more row
		if remaining > 0 {
			smNavIdx := findShowMoreNavIndex(nav, g.kind)
			isCursor := smNavIdx == m.cursor
			if isCursor {
				cursorLine = lineCount
			}
			moreText := fmt.Sprintf("      ↓ show 10 more (%d remaining)", remaining)
			p := m.th.Palette
			if isCursor {
				body.WriteString(lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true).Render(moreText) + "\n")
			} else {
				body.WriteString(lipgloss.NewStyle().Foreground(p.TextSubtle).Render(moreText) + "\n")
			}
			lineCount++
		}
	}

	return body.String(), cursorLine
}

// groupLayout returns opW, labelW, typeW, stackW column widths for a group.
// Total used: 2 (indent) + opW + labelW [+ typeW [+ stackW]] = width.
func groupLayout(kind rowKind, w int) (opW, labelW, typeW, stackW int) {
	const opColW = 14
	const indent = 2
	rem := w - indent - opColW
	if rem < 10 {
		rem = 10
	}
	opW = opColW
	switch kind {
	case kindPolicy:
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
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	hiStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Background(p.Selection).Bold(true)
	accentStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true)

	grpHi := m.sortHi[kind]
	grpAct := m.sortCol[kind]
	grpDir := m.sortDir[kind]

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
		switch {
		case isHL:
			return hiStyle.Render(padded)
		case isAct:
			return accentStyle.Render(padded)
		default:
			return dimStyle.Render(padded)
		}
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
		switch {
		case isHL:
			return hiStyle.Render(text)
		case isAct:
			return accentStyle.Render(text)
		default:
			return dimStyle.Render(text)
		}
	}

	var sb strings.Builder
	sb.WriteString("  ")
	sb.WriteString(renderHdr("Operation", colOp, opW))

	switch kind {
	case kindPolicy:
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
	isDelete := r.op == opDelete

	// The cursor background uses the .Dark value explicitly (same documented
	// compromise as statuswatch's detailmodel) so the bg-filled trailing spaces
	// and the styled cells always merge to one uniform band. All FOREGROUND
	// colors below stay adaptive so light terminals resolve correctly.
	var bg lipgloss.Color
	if isCursor {
		bg = lipgloss.Color(p.Selection.Dark)
	}

	// Determine base foreground color
	var fgColor lipgloss.AdaptiveColor
	switch {
	case isDelete && !isCursor:
		fgColor = p.Warning
	case isCursor:
		fgColor = p.TextPrimary
	default:
		fgColor = p.TextSecondary
	}

	// Label color (more prominent than other fields)
	var labelColor lipgloss.AdaptiveColor
	switch {
	case isDelete && !isCursor:
		labelColor = p.Warning
	case isCursor:
		labelColor = p.TextPrimary
	default:
		labelColor = p.PrimaryAccent
	}

	baseSt := lipgloss.NewStyle().Foreground(fgColor)
	labelSt := lipgloss.NewStyle().Foreground(labelColor)
	if isCursor {
		baseSt = baseSt.Background(bg)
		labelSt = labelSt.Background(bg)
	}

	// Operation color (always per-op role, even on delete rows)
	opColor := opStyleColor(m.th, r.op, isCursor, bg)
	opSt := lipgloss.NewStyle().Foreground(opColor)
	if isCursor {
		opSt = opSt.Background(bg)
	}

	// Build op plain string and pad
	opPlain := r.op.symbol() + " " + r.op.word()
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

	switch kind {
	case kindPolicy:
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

// opStyleColor returns the foreground color for an operation symbol/word.
// On cursor rows, all text uses TextPrimary (the bg handles highlighting).
func opStyleColor(th *theme.Theme, op opKind, isCursor bool, _ lipgloss.Color) lipgloss.AdaptiveColor {
	p := th.Palette
	if isCursor {
		return p.TextPrimary
	}
	switch op {
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
	return p.TextSecondary
}

// findNavIndex returns the nav list index for a specific row in a group.
func findNavIndex(nav []navLine, kind rowKind, key string, rowIdx int) int {
	for i, n := range nav {
		if n.kind == navRow && n.rowKind == kind && n.rowKey == key && n.rowIdx == rowIdx {
			return i
		}
	}
	return -1
}

// findShowMoreNavIndex returns the nav index for a show-more row in a group.
func findShowMoreNavIndex(nav []navLine, kind rowKind) int {
	for i, n := range nav {
		if n.kind == navShowMore && n.rowKind == kind {
			return i
		}
	}
	return -1
}
