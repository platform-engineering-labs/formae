// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package driftview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
)

// header renders the branded driftview header: "formae apply" (white/orange,
// consistent with every other screen) plus a distinct alert-colored
// "reconcile rejected" indicator to flag that this screen needs attention.
func (m Model) header() string {
	alert := lipgloss.NewStyle().Foreground(m.th.Palette.Error).Bold(true).Render("reconcile rejected")
	return components.HeaderBarBranded(m.th, "apply", alert, m.width)
}

// View implements tea.Model. Every screen renders exactly m.height lines.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	switch m.screen {
	case screenFilePrompt:
		return m.viewFilePrompt()
	case screenRevertConfirm:
		return m.viewRevertConfirm()
	default:
		return m.viewList()
	}
}

// padToHeight pads (or truncates) s to exactly m.height lines.
func (m Model) padToHeight(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// viewList renders the main drift list screen.
func (m Model) viewList() string {
	p := m.th.Palette
	header := m.header()

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")

	if m.opts.Notice != "" {
		noticeSt := lipgloss.NewStyle().Foreground(p.Warning)
		sb.WriteString("  " + noticeSt.Render(m.opts.Notice) + "\n")
	}

	alertSt := lipgloss.NewStyle().Foreground(p.Error)
	introSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	sb.WriteString("\n")
	sb.WriteString("  " + alertSt.Render("Your infrastructure has changed since the last reconcile.") + "\n")
	sb.WriteString("  " + introSt.Render("Select resources to extract, or revert all changes.") + "\n")
	sb.WriteString("\n")

	// Viewport body.
	vpH := max(m.height-m.chromeLines(), 1)
	m.vp.Width = m.width
	m.vp.Height = vpH

	if m.showHelp {
		// Render the help overlay centered in the remaining body + footer area.
		overlayHeight := m.height - lipgloss.Height(sb.String())
		if overlayHeight < 1 {
			overlayHeight = 1
		}
		overlay := components.HelpOverlay(m.th, m.width, overlayHeight, driftHelpGroups())
		sb.WriteString(overlay)
		return m.padToHeight(sb.String())
	}

	var body string
	var cursorLine int
	body, cursorLine = m.renderBody()
	m.vp.SetContent(body)

	// Scroll to keep the cursor row in view.
	if cursorLine < m.vp.YOffset {
		m.vp.YOffset = cursorLine
	}
	if cursorLine >= m.vp.YOffset+vpH {
		m.vp.YOffset = cursorLine - vpH + 1
	}

	sb.WriteString(m.vp.View())
	sb.WriteString("\n")
	sb.WriteString(m.renderFooter())

	return m.padToHeight(sb.String())
}

// renderBody builds the scrollable stack/row list and returns it with the
// line index of the cursor row (for scroll-to-cursor).
func (m Model) renderBody() (string, int) {
	p := m.th.Palette
	var sb strings.Builder
	cursorLine := 0
	lineCount := 0
	rowIdx := 0

	headSt := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	countSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	ruleSt := lipgloss.NewStyle().Foreground(p.Border)
	treeSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	guideSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	ruleW := min(58, max(m.width-4, 10))

	for _, st := range m.stacks {
		n := len(st.rows)
		noun := "changes"
		if n == 1 {
			noun = "change"
		}
		sb.WriteString("\n")
		sb.WriteString("  " + headSt.Render("Stack: "+st.name) + " " + countSt.Render(fmt.Sprintf("(%d %s)", n, noun)) + "\n")
		sb.WriteString("  " + ruleSt.Render(strings.Repeat("─", ruleW)) + "\n")
		lineCount += 3

		for _, r := range st.rows {
			isCursor := rowIdx == m.cursor
			if isCursor {
				cursorLine = lineCount
			}
			sb.WriteString(m.renderRow(r, isCursor) + "\n")
			lineCount++

			switch r.class {
			case rowClassDelete:
				sb.WriteString("        " + guideSt.Render("Deleted outside of formae. Remove from your code to align.") + "\n")
				lineCount++
			case rowClassUpdate:
				if m.expanded[r.key] {
					changeLines, err := components.RenderChangeLinesFromPatch(
						m.th, r.mod.PatchDocument, r.mod.Properties, r.mod.OldProperties, nil)
					if err == nil {
						for i, cl := range changeLines {
							connector := "├"
							if i == len(changeLines)-1 {
								connector = "└"
							}
							sb.WriteString("        " + treeSt.Render(connector) + " " + cl + "\n")
							lineCount++
						}
					}
				}
			}
			rowIdx++
		}
	}

	return sb.String(), cursorLine
}

// renderRow renders one resource row line (no trailing newline).
func (m Model) renderRow(r driftRow, isCursor bool) string {
	p := m.th.Palette

	// The cursor background uses the .Dark value explicitly (same documented
	// compromise as simview) so the bg-filled trailing spaces and the styled
	// cells always merge to one uniform band.
	var bg lipgloss.Color
	if isCursor {
		bg = lipgloss.Color(p.Selection.Dark)
	}

	style := func(c lipgloss.AdaptiveColor) lipgloss.Style {
		st := lipgloss.NewStyle().Foreground(c)
		if isCursor {
			st = st.Foreground(p.TextPrimary).Background(bg)
		}
		return st
	}

	// Operation word color per class. Only deletes are colored (Error, as
	// they're destructive); creates and updates stay white so we're not
	// coloring routine changes. No gold anywhere.
	var opColor lipgloss.AdaptiveColor
	switch r.class {
	case rowClassDelete:
		opColor = p.Error
	default:
		opColor = p.TextPrimary
	}

	frameSt := style(p.TextSecondary)
	opSt := style(opColor)
	labelSt := style(p.TextPrimary)
	typeSt := style(p.TextSubtle)

	checkbox := "[ ]"
	if m.selected[r.key] {
		checkbox = "[x]"
	}

	var line string
	switch r.class {
	case rowClassUpdate:
		tri := "▸"
		if m.expanded[r.key] {
			tri = "▾"
		}
		line = frameSt.Render("  "+tri+" "+checkbox+" ") +
			opSt.Render("update") +
			labelSt.Render("  "+r.mod.Label) +
			typeSt.Render(" ("+r.mod.Type+")")
	case rowClassCreate:
		line = frameSt.Render("    "+checkbox+" ") +
			opSt.Render("create") +
			labelSt.Render("  "+r.mod.Label) +
			typeSt.Render(" ("+r.mod.Type+")")
	default: // delete
		// Leading "·" sits in the same column as the update triangle so the
		// non-selectable deletes don't read as more indented than updates.
		line = frameSt.Render("  ·     ") +
			opSt.Render("delete") +
			labelSt.Render("  "+r.mod.Label) +
			typeSt.Render(" ("+r.mod.Type+")")
	}

	// Extend the highlight band to the full width on the cursor row.
	if isCursor {
		if w := lipgloss.Width(line); w < m.width {
			line += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", m.width-w))
		}
	}
	return line
}

// driftHelpGroups returns the grouped keybinding hints for the driftview help overlay.
func driftHelpGroups() []components.HelpGroup {
	return []components.HelpGroup{
		{
			Title: "Navigate",
			Hints: []components.KeyHint{
				{Key: "↑↓ / j k", Desc: "move cursor"},
			},
		},
		{
			Title: "Actions",
			Hints: []components.KeyHint{
				{Key: "space", Desc: "toggle select"},
				{Key: "enter", Desc: "expand details"},
				{Key: "a / n", Desc: "select all / none"},
				{Key: "e", Desc: "extract selected"},
				{Key: "r", Desc: "revert (--force)"},
			},
		},
		{
			Title: "General",
			Hints: []components.KeyHint{
				{Key: "esc / q", Desc: "abort"},
				{Key: "?", Desc: "close help"},
			},
		},
	}
}

// renderFooter renders the selected-count line plus the key-hint bar.
func (m Model) renderFooter() string {
	p := m.th.Palette
	s := m.th.Styles
	count := m.selectedCount()

	countSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	countLine := "  " + countSt.Render(fmt.Sprintf("%d selected", count))

	// Under SimulateOnly, the revert action is a cloud mutation and must be
	// suppressed. Extract remains available (local file write only).
	hints := []components.KeyHint{
		{Key: "e", Desc: "extract selected"},
		{Key: "r", Desc: "revert all (--force)"},
		{Key: "q", Desc: "abort"},
	}
	simulateHints := []components.KeyHint{
		{Key: "e", Desc: "extract selected"},
		{Key: "q", Desc: "abort"},
	}
	activeHints := hints
	if m.opts.SimulateOnly {
		activeHints = simulateHints
	}

	var bar string
	if count > 0 {
		bar = components.FooterBar(m.th, m.width, activeHints, "")
	} else {
		// Same layout as components.FooterBar, but the extract hint is dimmed
		// because there is nothing to extract.
		dimSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
		parts := make([]string, 0, len(activeHints))
		for i, h := range activeHints {
			if i == 0 {
				parts = append(parts, dimSt.Render(h.Key+": "+h.Desc))
				continue
			}
			parts = append(parts, s.KeybindingKey.Render(h.Key)+s.KeybindingDesc.Render(": "+h.Desc))
		}
		left := "  " + strings.Join(parts, "  ")
		right := s.KeybindingKey.Render("?") + s.KeybindingDesc.Render(": help") + "  "
		content := left + components.PadBetween(m.width, left, right) + right
		bar = lipgloss.NewStyle().
			Width(m.width).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(p.Border).
			Render(content)
	}

	return countLine + "\n" + bar
}

// viewFilePrompt renders the extract-to-file prompt screen.
func (m Model) viewFilePrompt() string {
	p := m.th.Palette
	s := m.th.Styles
	header := m.header()

	textSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	labelSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	pathSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	cursorSt := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	typeSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	subtleSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	rows := m.selectedRows()

	// Build the fixed bottom section (prompt + keybindings): always 3 lines.
	promptLine := "  " + labelSt.Render("Extract to: ") + pathSt.Render(m.fileInput) + cursorSt.Render("█")
	keybindLine := "  " + s.KeybindingKey.Render("enter") + s.KeybindingDesc.Render(": confirm") +
		"  " + s.KeybindingKey.Render("esc") + s.KeybindingDesc.Render(": cancel")
	// bottom = blank + prompt + blank + keybindings = 4 lines
	const bottomLines = 4

	// Fixed top section: header (2) + blank + count line = 4 lines
	countLine := "  " + textSt.Render(fmt.Sprintf("Extracting %d %s as code:", len(rows), pluralize(len(rows), "resource", "resources")))
	const topLines = 4 // header(2) + "\n" + count

	// Lines available for the resource list.
	listBudget := m.height - topLines - bottomLines
	if listBudget < 1 {
		listBudget = 1
	}

	// Collect resource list lines, truncating with "… and N more" if needed.
	var listLines []string
	for _, r := range rows {
		opSt := lipgloss.NewStyle().Foreground(m.opColorForClass(r.class))
		listLines = append(listLines, "    "+opSt.Render(r.classWord())+
			textSt.Render("  "+r.mod.Label)+
			typeSt.Render(" ("+r.mod.Type+")"))
	}
	if len(listLines) > listBudget {
		hidden := len(listLines) - (listBudget - 1) // reserve 1 for the "… and N more" line
		if hidden > 0 {
			listLines = listLines[:listBudget-1]
			listLines = append(listLines, "    "+subtleSt.Render(fmt.Sprintf("… and %d more", hidden)))
		}
	}

	// Assemble the frame.
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString(countLine + "\n")
	for _, l := range listLines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(promptLine + "\n")
	sb.WriteString("\n")
	sb.WriteString(keybindLine)

	return m.padToHeight(sb.String())
}

// viewRevertConfirm renders the revert-all confirmation screen.
func (m Model) viewRevertConfirm() string {
	p := m.th.Palette
	header := m.header()

	textSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	warnSt := lipgloss.NewStyle().Foreground(p.Warning)
	typeSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	consSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	subtleSt := lipgloss.NewStyle().Foreground(p.TextSubtle)

	rows := m.navigableRows()

	// Align consequences past the longest "revert  label (type)" prefix.
	lefts := make([]string, len(rows))
	maxLeft := 0
	for i, r := range rows {
		lefts[i] = "    revert  " + r.mod.Label + " (" + r.mod.Type + ")"
		maxLeft = max(maxLeft, len([]rune(lefts[i])))
	}
	consCol := maxLeft + 6

	// Build all resource list lines upfront.
	allResourceLines := make([]string, len(rows))
	for i, r := range rows {
		pad := strings.Repeat(" ", consCol-len([]rune(lefts[i])))
		allResourceLines[i] = "    " + warnSt.Render("revert") +
			textSt.Render("  "+r.mod.Label) +
			typeSt.Render(" ("+r.mod.Type+")") +
			pad + consSt.Render(m.revertConsequence(r))
	}

	// Fixed bottom section: blank + confirm line = 2 lines.
	confirmLine := "  " + textSt.Render("Are you sure? This cannot be undone. ") + warnSt.Render("(y/N)")
	const bottomLines = 2

	// Fixed top section: header(2) + blank + intro + blank = 5 lines.
	const topLines = 5

	// Lines available for the resource list.
	listBudget := m.height - topLines - bottomLines
	if listBudget < 1 {
		listBudget = 1
	}

	// Truncate the resource list with "… and N more" if needed.
	resourceLines := allResourceLines
	if len(resourceLines) > listBudget {
		hidden := len(resourceLines) - (listBudget - 1)
		if hidden > 0 {
			resourceLines = resourceLines[:listBudget-1]
			resourceLines = append(resourceLines, "    "+subtleSt.Render(fmt.Sprintf("… and %d more", hidden)))
		}
	}

	// Assemble the frame.
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	sb.WriteString("  " + textSt.Render("This will revert ALL out-of-band changes by re-applying with --force:") + "\n")
	sb.WriteString("\n")
	for _, l := range resourceLines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(confirmLine)

	return m.padToHeight(sb.String())
}

// selectedRows returns the selected rows in list order.
func (m Model) selectedRows() []driftRow {
	var rows []driftRow
	for _, r := range m.navigableRows() {
		if m.selected[r.key] {
			rows = append(rows, r)
		}
	}
	return rows
}

// opColorForClass returns the operation-word color for a row class. Only
// deletes are colored (Error); creates and updates stay white.
func (m Model) opColorForClass(c rowClass) lipgloss.AdaptiveColor {
	p := m.th.Palette
	if c == rowClassDelete {
		return p.Error
	}
	return p.TextPrimary
}

// revertConsequence describes what a forced re-apply does to one drifted row.
// Update rows show the first entry of their change set reverting to the old
// value (truncated); create/delete rows get a fixed consequence phrase.
func (m Model) revertConsequence(r driftRow) string {
	switch r.class {
	case rowClassCreate:
		return "will be removed"
	case rowClassDelete:
		return "will be re-created"
	}

	properties := r.mod.Properties
	if len(properties) == 0 {
		properties = []byte("{}")
	}
	oldProperties := r.mod.OldProperties
	if len(oldProperties) == 0 {
		oldProperties = []byte("{}")
	}
	cs, err := components.ExtractChanges(r.mod.PatchDocument, properties, oldProperties, nil)
	if err != nil {
		return ""
	}

	for _, ch := range cs.Properties {
		if ch.NoOp {
			continue
		}
		path := components.StripCardArrayIndices(ch.Path)
		if !ch.HasOld {
			return path
		}
		old := components.TruncateCascadeValue(ch.OldValue, 30)
		return path + " → " + components.QuoteCardValue(old)
	}
	return ""
}

// pluralize returns singular when n == 1, otherwise plural.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
