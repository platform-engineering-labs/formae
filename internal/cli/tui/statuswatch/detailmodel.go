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

// detailPageSize is how many rows per group are shown before a "show N more"
// row, and how many each activation reveals.
const detailPageSize = 20

// detailChromeLines is the detail view's non-viewport height: a plain two-line
// header + the pinned command row + separator + the two-line footer. It is
// deliberately independent of the multi view's chromeLines (which includes the
// taller header banner).
const detailChromeLines = 7

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
	cmdState   string // command state, used for the abandoned footer reminder
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
	// abandonedSet is the set of resource IDs that were force-canceled; rows in
	// this set with Canceled state render "Abandoned" in Warning color.
	abandonedSet   map[string]bool
	abandonedCount int // count of rows with stateLabel "Abandoned"
}

func newDetailModel(th *theme.Theme, width, height int) detailModel {
	vp := viewport.New(width, max(height-detailChromeLines, 1)) // placeholder; resized on SetCommand/View
	return detailModel{
		th:      th,
		visible: map[updateKind]int{kindTarget: detailPageSize, kindStack: detailPageSize, kindPolicy: detailPageSize, kindResource: detailPageSize},
		// Default the sort to the Label column (not the empty status column) so the
		// ▲/▼ arrow appears on a real, labeled header instead of a lone glyph.
		sortHi:  map[updateKind]int{kindTarget: detailColLabel, kindStack: detailColLabel, kindPolicy: detailColLabel, kindResource: detailColLabel},
		sortCol: map[updateKind]int{kindTarget: detailColLabel, kindStack: detailColLabel, kindPolicy: detailColLabel, kindResource: detailColLabel},
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
// abandonedIDs is the set of resource IDs (bare ksuids) that were force-canceled; may be nil.
func (d detailModel) SetCommand(c apimodel.Command, r row, spinView string, now time.Time, abandonedIDs map[string]bool) detailModel {
	d.cmdID = c.CommandID
	d.cmdState = c.State
	d.spinView = spinView
	d.abandonedSet = abandonedIDs

	// Rebuild groups, preserving sort state
	newGroups := buildGroups(c, abandonedIDs)
	for i := range newGroups {
		k := newGroups[i].kind
		sortGroup(newGroups[i].rows, d.sortCol[k], d.sortDir[k])
	}
	d.groups = newGroups

	// Count rows with "Abandoned" label for the footer reminder.
	count := 0
	for _, g := range d.groups {
		for _, row := range g.rows {
			if row.stateLabel == "Abandoned" {
				count++
			}
		}
	}
	d.abandonedCount = count

	// Clamp cursor against the new nav list so a shrinking command never leaves
	// the cursor pointing past the end of the navigable lines.
	nav := d.navLines()
	if len(nav) == 0 {
		d.cursor = 0
	} else if d.cursor < 0 {
		d.cursor = 0
	} else if d.cursor >= len(nav) {
		d.cursor = len(nav) - 1
	}

	// Pinned header + row reuse the multi view's renderers so they share the
	// responsive column drops and stay aligned; Age is omitted per mockup
	// VIEW 2, and sortHi -1 suppresses the sort-navigation highlight.
	mv := multiView{th: d.th, rows: []row{r}, cursor: -1, sortHi: -1, width: d.width, spinView: spinView, now: now, hideAge: true, pinned: true}
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
		// Capture the focused row so we can keep focus on it after the re-sort
		// instead of jumping back to the top.
		anchorKey, anchored := "", false
		if d.cursor >= 0 && d.cursor < len(nav) && nav[d.cursor].kind == navRow {
			anchorKey, anchored = nav[d.cursor].rowKey, true
		}
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
		// Keep focus on the same row after the re-sort; fall back to clamping if
		// it is no longer present.
		if anchored {
			newNav := d.navLines()
			found := -1
			for i, n := range newNav {
				if n.kind == navRow && n.groupKind == grpKind && n.rowKey == anchorKey {
					found = i
					break
				}
			}
			switch {
			case found >= 0:
				d.cursor = found
			case d.cursor >= len(newNav):
				d.cursor = len(newNav) - 1
			}
		}

	case key.Matches(msg, keys.Enter) || msg.Type == tea.KeySpace:
		if d.cursor >= 0 && d.cursor < total {
			line := nav[d.cursor]
			if line.kind == navShowMore {
				d.visible[line.groupKind] += detailPageSize
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
// Fixed budget: indent 2 + status 6 + operation 12 + time 5 = 25; we subtract
// 26 to keep one spare and ensure rows never overflow the viewport by 1.
func groupLayout(kind updateKind, w int) (labelW, typeW, stackW int) {
	rem := w - 26
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
func (d detailModel) View(height int, showQueryBar bool) string {
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

		labelW, typeW, stackW := groupLayout(g.kind, w)

		// Section header
		headerStr := "\n  " + components.SectionHeader(d.th, g.title) + "\n"
		body.WriteString(headerStr)
		lineCount += 2 // blank + header line

		// Column header for this group
		colHeader := d.renderGroupColHeader(g.kind, labelW, typeW, stackW)
		body.WriteString(colHeader + "\n")
		lineCount++

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
			moreText := fmt.Sprintf("      ↓ show %d more (%d remaining)", min(detailPageSize, remaining), remaining)
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

	// Footer reminder: show when command is terminal and there are abandoned rows.
	if d.abandonedCount > 0 && isTerminalCommand(d.cmdState) {
		reminderText := fmt.Sprintf("\n  ⚠ %d in-progress updates were abandoned. Check the synchronizer or the cloud console for orphaned resources.", d.abandonedCount)
		body.WriteString(lipgloss.NewStyle().Foreground(p.Warning).Render(reminderText) + "\n")
	}

	// Viewport height from the detail view's own chrome (a plain two-line header,
	// the pinned command row + separator, and the footer) — independent of the
	// multi view's chromeLines, which now includes a taller header banner.
	vpHeight := height - detailChromeLines
	if showQueryBar {
		vpHeight -= 2
	}
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

	return d.pinnedHeader + "\n" +
		d.pinnedRow + "\n" +
		sep + "\n" +
		d.vp.View()
}

func detailFooterHints(singleCommand bool) []components.KeyHint {
	hints := []components.KeyHint{
		{Key: "→←", Desc: "column"},
		{Key: "s", Desc: "toggle sort"},
		{Key: "space", Desc: "expand"},
		{Key: "d", Desc: "details"},
	}
	// In single-command mode (apply/destroy --watch) there is no command list to
	// go back to, so omit the "esc back" hint; esc quits.
	if !singleCommand {
		hints = append(hints, components.KeyHint{Key: "esc", Desc: "back"})
	}
	return append(hints, components.KeyHint{Key: "q", Desc: "quit"})
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
// It uses the same sp2 indent and identical cell widths as renderSummaryRow so
// headers and data rows are always in sync. Padding is applied to the PLAIN
// label text before the lipgloss style is wrapped around it — never the other
// way around — to prevent escape-sequence fragments from being sliced off.
func (d detailModel) renderGroupColHeader(kind updateKind, labelW, typeW, stackW int) string {
	p := d.th.Palette
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	hiStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Background(p.Selection).Bold(true)
	accentStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true)

	grpHi := d.sortHi[kind]
	grpAct := d.sortCol[kind]
	grpDir := d.sortDir[kind]

	// renderColHdrPadded pads the PLAIN text to w columns first, then wraps
	// the padded plain string in the appropriate lipgloss style. This ensures
	// that pad() (which uses utf8.RuneCountInString) never slices through an
	// escape sequence — a bug that produced "[1;38Label" fragments.
	renderColHdrPadded := func(name string, col int, w int) string {
		isHL := col == grpHi
		isAct := col == grpAct
		arrow := ""
		// Never draw the sort arrow on the empty status column — a lone ▲/▼ there
		// reads as a collapse/expand toggle rather than a column sort indicator.
		if isAct && col != detailColStatus {
			if grpDir == components.SortDesc {
				arrow = " ▼"
			} else {
				arrow = " ▲"
			}
		}
		text := name + arrow
		// Pad the plain text to the desired width, then apply the style.
		paddedText := pad(text, w)
		switch {
		case isHL:
			return hiStyle.Render(paddedText)
		case isAct:
			return accentStyle.Render(paddedText)
		default:
			return dimStyle.Render(paddedText)
		}
	}
	// renderColHdrLast renders the last (un-padded) column header cell.
	renderColHdrLast := func(name string, col int) string {
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
	// 2-space indent mirrors the sp2 in renderSummaryRow so header columns
	// are pixel-aligned with data cell content.
	sb.WriteString("  ")
	// Status glyph cell: 6 wide, no label.
	sb.WriteString(renderColHdrPadded("", detailColStatus, 6))

	switch kind {
	case kindPolicy:
		sb.WriteString(renderColHdrPadded("Label", detailColLabel, labelW))
		sb.WriteString(renderColHdrPadded("Type", detailColType, typeW))
		sb.WriteString(renderColHdrPadded("Stack", detailColStack, stackW))
		sb.WriteString(renderColHdrPadded("Operation", detailColOperation, 12))
		sb.WriteString(renderColHdrLast("Time", detailColTime))
	case kindResource:
		sb.WriteString(renderColHdrPadded("Label", detailColLabel, labelW))
		sb.WriteString(renderColHdrPadded("Type", detailColType, typeW))
		sb.WriteString(renderColHdrPadded("Operation", detailColOperation, 12))
		sb.WriteString(renderColHdrLast("Time", detailColTime))
	default: // targets, stacks
		sb.WriteString(renderColHdrPadded("Label", detailColLabel, labelW))
		sb.WriteString(renderColHdrPadded("Operation", detailColOperation, 12))
		sb.WriteString(renderColHdrLast("Time", detailColTime))
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
		// Delegate to components.Truncate. The local convention is "give maxW-1
		// visible runes when cut"; Truncate(s, w) gives exactly w, so we pass maxW-1.
		return components.Truncate(s, maxW-1)
	}

	timeStr := formatDetailDuration(r)

	// State label ("finishing"/"Abandoned") placed just after the glyph. These are
	// rare, additive annotations — canceled/done/etc rows carry NO label (the
	// glyph conveys the state), so they keep the fixed 6-wide glyph column and
	// stay aligned with the header; only the rare labeled rows widen this prefix.
	// A trailing space guarantees a gap before the label (no "Abandonedlabel").
	glyphCell := " " + glyphStr + " "
	if r.stateLabel == "Abandoned" {
		warnSt := withBg(lipgloss.NewStyle().Foreground(p.Warning))
		glyphCell = " " + glyphStr + " " + warnSt.Render(r.stateLabel) + " "
	} else if r.stateLabel != "" {
		glyphCell = " " + glyphStr + " " + dimSt.Render(r.stateLabel) + " "
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
			spinFrame = d.th.Spinner.StaticFrame
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
