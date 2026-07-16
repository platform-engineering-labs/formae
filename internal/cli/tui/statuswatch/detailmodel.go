// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// navigableLineKind classifies a line in the flat navigation list.
type navigableLineKind int

const (
	navRow      navigableLineKind = iota // a summary or card row
	navShowMore                          // a "show N more" row
)

// navigableLine tracks one navigable cursor position.
type navigableLine struct {
	kind      navigableLineKind
	groupKind updateKind // which group this line belongs to
	rowKey    string     // updateRow.key (for navRow only)
	rowIdx    int        // index into the group's visible rows slice (for navRow only)
}

type detailModel struct {
	th         *theme.Theme
	cmdID      string
	groups     []group
	cursor     int                // index into the flat navigable lines list
	expanded   map[string]bool    // keyed by updateRow.key
	detailMode bool               // 'd': every row renders as card
	visible    map[updateKind]int // pagination limit per group (starts at 10)
	sortHi     map[updateKind]int // column highlighted by →←
	sortCol    map[updateKind]int // active sort column per group
	sortDir    map[updateKind]components.SortDirection
	vp         viewport.Model
	width      int
	// The pinned header row + separator lines (injected during SetCommand, not rebuilt every View)
	pinnedHeader string
	pinnedRow    string
	// spinner view injected from outside
	spinView string
}

func newDetailModel(th *theme.Theme, width, height int) detailModel {
	vp := viewport.New(width, max(height-chromeLines-2, 1)) // -2 for pinned cmd row + separator
	return detailModel{
		th:      th,
		visible: map[updateKind]int{kindTarget: 10, kindStack: 10, kindPolicy: 10, kindResource: 10},
		sortHi:  map[updateKind]int{kindTarget: 0, kindStack: 0, kindPolicy: 0, kindResource: 0},
		sortCol: map[updateKind]int{kindTarget: 0, kindStack: 0, kindPolicy: 0, kindResource: 0},
		sortDir: map[updateKind]components.SortDirection{
			kindTarget:   components.SortAsc,
			kindStack:    components.SortAsc,
			kindPolicy:   components.SortAsc,
			kindResource: components.SortAsc,
		},
		expanded: make(map[string]bool),
		vp:       vp,
		width:    width,
	}
}

// SetCommand rebuilds the groups from the command data, preserving expanded/pagination/sort state.
func (d detailModel) SetCommand(c apimodel.Command, r row, spinView string, now time.Time) detailModel {
	d.cmdID = c.CommandID
	d.spinView = spinView

	// Rebuild groups, preserving sort state
	newGroups := buildGroups(c)
	for i := range newGroups {
		k := newGroups[i].kind
		sortGroup(newGroups[i].rows, d.sortCol[k], d.sortDir[k])
	}
	d.groups = newGroups

	// Pinned header + row reuse the multi view's renderers so they share the
	// responsive column drops and stay aligned; Age is omitted per mockup
	// VIEW 2, and sortHi -1 suppresses the sort-navigation highlight.
	mv := multiView{th: d.th, rows: []row{r}, cursor: -1, sortHi: -1, width: d.width, spinView: spinView, now: now, hideAge: true}
	d.pinnedHeader = mv.headerRow()
	rows := mv.renderRows(1)
	if len(rows) > 0 {
		d.pinnedRow = rows[0]
	}

	return d
}

// navLines computes the flat list of navigable cursor positions given current state.
func (d detailModel) navLines() []navigableLine {
	var lines []navigableLine
	for _, g := range d.groups {
		lim := d.visible[g.kind]
		shown, remaining := visibleRows(g, lim)
		for i, r := range shown {
			lines = append(lines, navigableLine{
				kind:      navRow,
				groupKind: g.kind,
				rowKey:    r.key,
				rowIdx:    i,
			})
		}
		if remaining > 0 {
			lines = append(lines, navigableLine{
				kind:      navShowMore,
				groupKind: g.kind,
			})
		}
	}
	return lines
}

// groupForKind finds the group in d.groups with the given kind.
func (d detailModel) groupForKind(k updateKind) *group {
	for i := range d.groups {
		if d.groups[i].kind == k {
			return &d.groups[i]
		}
	}
	return nil
}

// cursorGroupKind returns the group kind the cursor is currently in.
func (d detailModel) cursorGroupKind() updateKind {
	nav := d.navLines()
	if d.cursor >= 0 && d.cursor < len(nav) {
		return nav[d.cursor].groupKind
	}
	return kindResource
}

// Update handles key events and returns the updated model and a back flag.
func (d detailModel) Update(msg tea.KeyMsg, keys tui.KeyMap) (detailModel, bool) {
	nav := d.navLines()
	total := len(nav)

	switch {
	case msg.Type == tea.KeyEsc || msg.Type == tea.KeyBackspace:
		return d, true

	case key.Matches(msg, keys.Up):
		if d.cursor > 0 {
			d.cursor--
		}

	case key.Matches(msg, keys.Down):
		if d.cursor < total-1 {
			d.cursor++
		}

	case key.Matches(msg, keys.PageUp):
		d.cursor -= d.vp.Height
		if d.cursor < 0 {
			d.cursor = 0
		}

	case key.Matches(msg, keys.PageDown):
		d.cursor += d.vp.Height
		if d.cursor >= total {
			d.cursor = total - 1
		}
		if d.cursor < 0 {
			d.cursor = 0
		}

	case msg.Type == tea.KeyLeft || key.Matches(msg, keys.Left):
		grpKind := d.cursorGroupKind()
		cols := validSortCols(grpKind)
		hi := d.sortHi[grpKind]
		idx := sortColIndex(cols, hi)
		idx = (idx - 1 + len(cols)) % len(cols)
		d.sortHi[grpKind] = cols[idx]

	case msg.Type == tea.KeyRight || key.Matches(msg, keys.Right):
		grpKind := d.cursorGroupKind()
		cols := validSortCols(grpKind)
		hi := d.sortHi[grpKind]
		idx := sortColIndex(cols, hi)
		idx = (idx + 1) % len(cols)
		d.sortHi[grpKind] = cols[idx]

	case key.Matches(msg, keys.Sort):
		grpKind := d.cursorGroupKind()
		hi := d.sortHi[grpKind]
		act := d.sortCol[grpKind]
		dir := d.sortDir[grpKind]
		if act == hi {
			if dir == components.SortAsc {
				d.sortDir[grpKind] = components.SortDesc
			} else {
				d.sortDir[grpKind] = components.SortAsc
			}
		} else {
			d.sortCol[grpKind] = hi
			d.sortDir[grpKind] = components.SortDesc
		}
		// Re-sort the group
		g := d.groupForKind(grpKind)
		if g != nil {
			sortGroup(g.rows, d.sortCol[grpKind], d.sortDir[grpKind])
		}
		// Reset pagination for this group
		d.visible[grpKind] = 10
		// Reset cursor to top
		d.cursor = 0

	case key.Matches(msg, keys.Enter) || msg.Type == tea.KeySpace:
		if d.cursor >= 0 && d.cursor < total {
			line := nav[d.cursor]
			if line.kind == navShowMore {
				d.visible[line.groupKind] += 10
			} else {
				// Toggle expansion for this row key
				if d.expanded[line.rowKey] {
					delete(d.expanded, line.rowKey)
				} else {
					d.expanded[line.rowKey] = true
				}
			}
		}

	case key.Matches(msg, keys.ToggleDetail):
		d.detailMode = !d.detailMode
	}

	return d, false
}

// groupLayout returns label/type/stack column widths for a group at total width w.
// Fixed budget: status 6 + operation 12 + time 6 = 24.
func groupLayout(kind updateKind, w int) (labelW, typeW, stackW int) {
	rem := w - 24
	switch kind {
	case kindPolicy:
		labelW = max(rem*2/5, 12)
		typeW = max(rem/5, 10)
		stackW = max(rem-labelW-typeW, 10)
	case kindResource:
		typeW = max(rem*2/5, 16)
		labelW = max(rem-typeW, 12)
	default: // targets, stacks
		labelW = max(rem, 20)
	}
	return
}

// View renders the full detail view for the given terminal height.
func (d detailModel) View(height int) string {
	p := d.th.Palette
	w := d.width

	sep := lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", w))

	// Build scrollable body
	var body strings.Builder
	nav := d.navLines()
	cursorLine := 0 // line index within body where cursor is

	lineCount := 0 // running count of lines emitted to body

	for _, g := range d.groups {
		lim := d.visible[g.kind]
		shown, remaining := visibleRows(g, lim)

		labelW, typeW, _ := groupLayout(g.kind, w)
		var stackW int
		_, _, stackW = groupLayout(g.kind, w)

		// Section header
		headerStr := "\n  " + lipgloss.NewStyle().
			Foreground(p.SecondaryAccent).
			Bold(true).
			Render("▌ "+g.title) + "\n"
		body.WriteString(headerStr)
		lineCount += 2 // blank + header line

		// Column header for this group
		colHeader := d.renderGroupColHeader(g.kind, labelW, typeW, stackW)
		body.WriteString(colHeader + "\n")
		lineCount++

		isActiveGroup := g.kind == d.cursorGroupKind()
		_ = isActiveGroup

		// Rows
		for i, r := range shown {
			// Find this row's nav index
			navIdx := d.findNavIndex(nav, g.kind, r.key, i)
			isCursor := navIdx == d.cursor
			if isCursor {
				cursorLine = lineCount
			}

			isExpanded := d.expanded[r.key] || d.detailMode

			if isExpanded {
				card := d.renderCard(r, w, isCursor)
				cardLines := strings.Split(card, "\n")
				for _, cl := range cardLines {
					body.WriteString(cl + "\n")
					lineCount++
				}
			} else {
				rowStr := d.renderSummaryRow(r, g.kind, labelW, typeW, stackW, isCursor)
				body.WriteString(rowStr)
				// Count lines including error/cascade second lines
				rowLines := strings.Count(rowStr, "\n")
				lineCount += rowLines
			}
		}

		// Show-more row
		if remaining > 0 {
			showMoreNavIdx := d.findShowMoreNavIndex(nav, g.kind)
			isCursor := showMoreNavIdx == d.cursor
			if isCursor {
				cursorLine = lineCount
			}
			moreText := fmt.Sprintf("      ↓ show 10 more (%d remaining)", remaining)
			if isCursor {
				body.WriteString(lipgloss.NewStyle().
					Foreground(p.PrimaryAccent).
					Bold(true).
					Render(moreText) + "\n")
			} else {
				body.WriteString(lipgloss.NewStyle().
					Foreground(p.TextSubtle).
					Render(moreText) + "\n")
			}
			lineCount++
		}
	}

	// Viewport scrolls to keep cursor in view
	vpHeight := height - chromeLines - 2 // subtract pinned header row + separator
	if vpHeight < 1 {
		vpHeight = 1
	}
	d.vp.Width = w
	d.vp.Height = vpHeight
	d.vp.SetContent(body.String())

	// Scroll to keep cursor visible
	if cursorLine < d.vp.YOffset {
		d.vp.YOffset = cursorLine
	}
	if cursorLine >= d.vp.YOffset+vpHeight {
		d.vp.YOffset = cursorLine - vpHeight + 1
	}

	// Footer hints in detail view
	footer := components.FooterBar(d.th, w, detailFooterHints(), "")

	return d.pinnedHeader + "\n" +
		d.pinnedRow + "\n" +
		sep + "\n" +
		d.vp.View() + "\n" +
		footer
}

func detailFooterHints() []components.KeyHint {
	return []components.KeyHint{
		{Key: "space", Desc: "expand"},
		{Key: "d", Desc: "details"},
		{Key: "/", Desc: "query"},
		{Key: "esc", Desc: "back"},
	}
}

// findNavIndex finds the nav cursor index for a specific row in a group.
func (d detailModel) findNavIndex(nav []navigableLine, kind updateKind, key string, rowIdx int) int {
	for i, n := range nav {
		if n.kind == navRow && n.groupKind == kind && n.rowKey == key && n.rowIdx == rowIdx {
			return i
		}
	}
	return -1
}

// findShowMoreNavIndex finds the nav cursor index for a show-more row.
func (d detailModel) findShowMoreNavIndex(nav []navigableLine, kind updateKind) int {
	for i, n := range nav {
		if n.kind == navShowMore && n.groupKind == kind {
			return i
		}
	}
	return -1
}

// renderGroupColHeader renders the column header row for a group.
func (d detailModel) renderGroupColHeader(kind updateKind, labelW, typeW, stackW int) string {
	p := d.th.Palette
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	hiStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Background(p.Selection).Bold(true)
	accentStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true)

	grpHi := d.sortHi[kind]
	grpAct := d.sortCol[kind]
	grpDir := d.sortDir[kind]

	renderColHdr := func(name string, col int) string {
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

	statusHdr := renderColHdr("", detailColStatus)
	var sb strings.Builder
	sb.WriteString(pad(statusHdr, 6))

	switch kind {
	case kindPolicy:
		sb.WriteString(pad(renderColHdr("Label", detailColLabel), labelW))
		sb.WriteString(pad(renderColHdr("Type", detailColType), typeW))
		sb.WriteString(pad(renderColHdr("Stack", detailColStack), stackW))
		sb.WriteString(pad(renderColHdr("Operation", detailColOperation), 12))
		sb.WriteString(renderColHdr("Time", detailColTime))
	case kindResource:
		sb.WriteString(pad(renderColHdr("Label", detailColLabel), labelW))
		sb.WriteString(pad(renderColHdr("Type", detailColType), typeW))
		sb.WriteString(pad(renderColHdr("Operation", detailColOperation), 12))
		sb.WriteString(renderColHdr("Time", detailColTime))
	default: // targets, stacks
		sb.WriteString(pad(renderColHdr("Label", detailColLabel), labelW))
		sb.WriteString(pad(renderColHdr("Operation", detailColOperation), 12))
		sb.WriteString(renderColHdr("Time", detailColTime))
	}
	return sb.String()
}

// renderSummaryRow renders a single summary row (non-expanded).
func (d detailModel) renderSummaryRow(r updateRow, kind updateKind, labelW, typeW, stackW int, isCursor bool) string {
	p := d.th.Palette
	bg := lipgloss.Color("")
	if isCursor {
		bg = lipgloss.Color(p.Selection.Dark)
	}
	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isCursor {
			return s.Background(bg)
		}
		return s
	}
	padStr := func(s string, w int) string {
		n := lipgloss.Width(s)
		if n >= w {
			return s
		}
		spaces := strings.Repeat(" ", w-n)
		if isCursor {
			return s + lipgloss.NewStyle().Background(bg).Render(spaces)
		}
		return s + spaces
	}

	isFailed := r.state == components.StateFailed || r.state == components.StateSkipped
	isDone := r.state == components.StateDone

	// Glyph
	glyphStr := d.renderStateGlyph(r, bg, isCursor)
	glyphStr = padStr(glyphStr, 2)

	// Styles
	labelSt := withBg(lipgloss.NewStyle().Foreground(p.PrimaryAccent))
	textSt := withBg(lipgloss.NewStyle().Foreground(p.TextSecondary))
	dimSt := withBg(lipgloss.NewStyle().Foreground(p.TextSubtle))

	if isDone && !isCursor {
		dim := withBg(lipgloss.NewStyle().Foreground(p.TextSubtle))
		labelSt, textSt, dimSt = dim, dim, dim
	}
	if isFailed && !isCursor {
		red := withBg(lipgloss.NewStyle().Foreground(p.Error))
		labelSt, textSt, dimSt = red, red, red
	}
	if isCursor {
		if isFailed {
			bright := withBg(lipgloss.NewStyle().Foreground(p.ErrorBright))
			labelSt, textSt, dimSt = bright, bright, bright
		} else if isDone {
			med := withBg(lipgloss.NewStyle().Foreground(p.TextSecondary))
			labelSt, textSt, dimSt = med, med, med
		} else {
			labelSt = withBg(lipgloss.NewStyle().Foreground(p.PrimaryAccent))
			textSt = withBg(lipgloss.NewStyle().Foreground(p.TextPrimary))
			dimSt = withBg(lipgloss.NewStyle().Foreground(p.TextPrimary))
		}
	}

	trunc := func(s string, maxW int) string {
		if maxW < 4 {
			maxW = 4
		}
		runes := []rune(s)
		if len(runes) > maxW-1 {
			return string(runes[:maxW-2]) + "…"
		}
		return s
	}

	timeStr := formatDetailDuration(r)

	// State label suffix (for finishing/canceled)
	glyphCell := " " + glyphStr + " "
	if r.stateLabel != "" {
		glyphCell = " " + glyphStr + " " + dimSt.Render(r.stateLabel)
	}

	sp2 := "  "
	if isCursor {
		sp2 = lipgloss.NewStyle().Background(bg).Render("  ")
	}

	var rowStr string
	switch kind {
	case kindPolicy:
		rowStr = sp2 + padStr(glyphCell, 6) +
			padStr(labelSt.Render(trunc(r.label, labelW-1)), labelW) +
			padStr(textSt.Render(trunc(r.typeName, typeW-1)), typeW) +
			padStr(textSt.Render(trunc(r.stack, stackW-1)), stackW) +
			padStr(textSt.Render(r.operation), 12) +
			dimSt.Render(timeStr)
	case kindResource:
		rowStr = sp2 + padStr(glyphCell, 6) +
			padStr(labelSt.Render(trunc(r.label, labelW-1)), labelW) +
			padStr(textSt.Render(trunc(r.typeName, typeW-1)), typeW) +
			padStr(textSt.Render(r.operation), 12) +
			dimSt.Render(timeStr)
	default: // targets, stacks
		rowStr = sp2 + padStr(glyphCell, 6) +
			padStr(labelSt.Render(trunc(r.label, labelW-1)), labelW) +
			padStr(textSt.Render(r.operation), 12) +
			dimSt.Render(timeStr)
	}

	// Pad to full width for cursor
	if isCursor {
		rowWidth := lipgloss.Width(rowStr)
		if rowWidth < d.width {
			rowStr += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", d.width-rowWidth))
		}
	}

	result := rowStr + "\n"

	// Second line for failed: error message
	if r.state == components.StateFailed && r.errMsg != "" {
		errLine := "      " + lipgloss.NewStyle().Foreground(p.Error).Render(r.errMsg)
		result += errLine + "\n"
	}
	// Second line for cascade: depends on
	if r.state == components.StateSkipped && r.cascadeSrc != "" {
		depLine := "      " + lipgloss.NewStyle().Foreground(p.TextSubtle).Render("depends on "+r.cascadeSrc)
		result += depLine + "\n"
	}

	return result
}

// renderStateGlyph renders the status symbol for a row.
func (d detailModel) renderStateGlyph(r updateRow, bg lipgloss.Color, isCursor bool) string {
	p := d.th.Palette
	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isCursor {
			return s.Background(bg)
		}
		return s
	}
	switch r.state {
	case components.StateDone:
		return withBg(lipgloss.NewStyle().Foreground(p.Done)).Render("✓")
	case components.StateInProgress:
		spinFrame := d.spinView
		if spinFrame == "" {
			spinFrame = "◐"
		}
		return withBg(lipgloss.NewStyle().Foreground(p.PrimaryAccent)).Render(spinFrame)
	case components.StatePending:
		return withBg(lipgloss.NewStyle().Foreground(p.Pending)).Render("○")
	case components.StateFailed:
		return withBg(lipgloss.NewStyle().Foreground(p.Error).Bold(true)).Render("✗")
	case components.StateSkipped:
		return withBg(lipgloss.NewStyle().Foreground(p.TextSecondary)).Render("⊘")
	}
	return " "
}

// formatDetailDuration returns the time string for a summary row.
func formatDetailDuration(r updateRow) string {
	switch r.state {
	case components.StateDone, components.StateInProgress, components.StateFailed:
		return components.FormatDuration(r.duration)
	default:
		return "—"
	}
}

// renderCard renders a bordered detail card for a row.
func (d detailModel) renderCard(r updateRow, w int, isCursor bool) string {
	p := d.th.Palette

	// Card body: key/value pairs
	fieldSt := lipgloss.NewStyle().Foreground(p.TextSubtle)
	valueSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	errSt := lipgloss.NewStyle().Foreground(p.Error)
	inPrgSt := lipgloss.NewStyle().Foreground(p.InProgress)

	kv := func(k, v string) string {
		return fieldSt.Render(k) + " " + valueSt.Render(v)
	}

	var lines []string

	switch r.kind {
	case kindResource:
		lines = append(lines, kv("Type:    ", r.typeName))
		lines = append(lines, kv("Stack:   ", r.stack))
		switch r.state {
		case components.StateDone:
			lines = append(lines, kv("Duration:", components.FormatDuration(r.duration)))
		case components.StateInProgress:
			lines = append(lines, kv("Started: ", components.FormatDuration(r.duration)+" ago"))
			if r.maxAttempt > 0 {
				lines = append(lines, kv("Attempt: ", fmt.Sprintf("%d/%d", r.attempt, r.maxAttempt)))
			}
			if r.statusMsg != "" {
				lines = append(lines, fieldSt.Render("Status:  ")+" "+inPrgSt.Render(r.statusMsg))
			}
		case components.StateFailed:
			lines = append(lines, kv("Duration:", components.FormatDuration(r.duration)))
			if r.maxAttempt > 0 {
				lines = append(lines, kv("Attempt: ", fmt.Sprintf("%d/%d", r.attempt, r.maxAttempt)))
			}
			if r.errMsg != "" {
				lines = append(lines, fieldSt.Render("Error:   ")+" "+errSt.Render(r.errMsg))
			}
		case components.StateSkipped:
			cascade := "yes"
			if r.cascadeSrc != "" {
				cascade = "yes (depends on " + r.cascadeSrc + ")"
			}
			lines = append(lines, kv("Cascade: ", cascade))
		}

	case kindTarget:
		discStr := "no"
		if r.discoverable {
			discStr = "yes"
		}
		lines = append(lines, kv("Discoverable:", discStr))
		lines = append(lines, kv("Duration:    ", components.FormatDuration(r.duration)))
		if r.errMsg != "" {
			lines = append(lines, fieldSt.Render("Error:       ")+" "+errSt.Render(r.errMsg))
		}

	case kindStack:
		if r.description != "" {
			lines = append(lines, kv("Description:", r.description))
		}
		lines = append(lines, kv("Duration:   ", components.FormatDuration(r.duration)))
		if r.errMsg != "" {
			lines = append(lines, fieldSt.Render("Error:      ")+" "+errSt.Render(r.errMsg))
		}

	case kindPolicy:
		lines = append(lines, kv("Type:    ", r.typeName))
		lines = append(lines, kv("Stack:   ", r.stack))
		lines = append(lines, kv("Duration:", components.FormatDuration(r.duration)))
		if len(r.referencingStacks) > 0 {
			lines = append(lines, kv("Referencing stacks:", strings.Join(r.referencingStacks, ", ")))
		}
		if r.errMsg != "" {
			lines = append(lines, fieldSt.Render("Error:   ")+" "+errSt.Render(r.errMsg))
		}
	}

	content := strings.Join(lines, "\n")

	// Border color
	borderColor := p.Border
	if r.state == components.StateFailed {
		borderColor = p.Error
	}
	if isCursor {
		borderColor = p.PrimaryAccent
	}

	cardW := w - 8
	if cardW < 30 {
		cardW = 30
	}

	// Render card body with rounded border
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardW).
		Padding(0, 1).
		Render(content)

	// Build custom top border with title
	cardLines := strings.Split(card, "\n")
	actualWidth := 0
	if len(cardLines) > 0 {
		actualWidth = lipgloss.Width(cardLines[len(cardLines)-1])
	}
	if actualWidth == 0 {
		actualWidth = cardW + 2
	}

	borderSt := lipgloss.NewStyle().Foreground(borderColor)
	glyphStr := d.renderStateGlyph(r, lipgloss.Color(""), false)
	timeStr := formatDetailDuration(r)
	if r.state == components.StateInProgress {
		timeStr = components.FormatDuration(r.duration)
	}

	// Title: "operation label (type if resource/policy)"
	titleLabel := r.label
	if (r.kind == kindResource || r.kind == kindPolicy) && r.typeName != "" {
		titleLabel = r.label + " (" + r.typeName + ")"
	}
	if r.kind == kindPolicy && r.stack != "" {
		titleLabel = r.label + " (" + r.typeName + ", " + r.stack + ")"
	}
	titleStr := r.operation + " " + titleLabel

	var statusInTitle string
	if r.state == components.StateSkipped && r.kind == kindPolicy {
		statusInTitle = " Skipped "
	} else {
		statusInTitle = " " + glyphStr + " " + timeStr + " "
	}

	titleContent := " " + titleStr + " "
	titleW := lipgloss.Width(titleContent)
	statusW := lipgloss.Width(statusInTitle)
	// Total = ╭ + ─ + titleContent + dashes + statusInTitle + ╮ = actualWidth
	dashW := actualWidth - titleW - statusW - 2 // 2 = ╭ + ╮
	if dashW < 1 {
		dashW = 1
	}

	headerLine := borderSt.Render("╭─") +
		titleContent +
		borderSt.Render(strings.Repeat("─", dashW)) +
		statusInTitle +
		borderSt.Render("╮")

	if len(cardLines) > 0 {
		cardLines[0] = headerLine
	}

	return "    " + strings.Join(cardLines, "\n    ")
}

// sortColIndex returns the position of col in cols slice.
func sortColIndex(cols []int, col int) int {
	for i, c := range cols {
		if c == col {
			return i
		}
	}
	return 0
}
