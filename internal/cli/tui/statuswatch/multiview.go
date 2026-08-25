// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// row is a multi-command table row: command + health + resource counts.
type row struct {
	cmd    apimodel.Command
	counts map[components.State]int
	health health
}

// Column indexes for the multi-command table.
const (
	colStatus = iota
	colID
	colCommand
	colMode
	colUser
	colProgress
	colDone
	colFailed
	colInProg
	colPending
	colTime
	colAge
	colCount
)

// colSpec describes a column: title, width, drop priority, sortability.
type colSpec struct {
	title    string
	width    int // fixed rendered width incl. separating gap; colProgress is elastic
	priority int // 0 always visible; higher drops first as the terminal narrows
	sortable bool
}

// multiCols defines the column specifications.
// Widths follow the prototype's fixed layout (sym 4 + gap, Command 9,
// Mode 11, counts 4 each, Time 7, Age 5); Progress is elastic with a floor
// of 10 cells. ID is wide enough to show a full 27-char command ksuid plus a
// trailing gap so the identifier is never truncated (users copy it for
// `status command --query 'id:…'` and `cancel`). User is sized for a short
// name/handle; a bare Subject (typically a UUID) is truncated to fit it,
// same overflow idiom ID uses for an over-long ksuid. User's priority (3) is
// its own tier, shed before Mode/◐/○/Age (2): it is supplementary
// attribution, not operational state, so a terminal width that already
// showed the operational columns must keep showing them — User only appears
// once there is genuinely room left over.
var multiCols = [colCount]colSpec{
	colStatus:   {"", 5, 0, true},
	colID:       {"ID", 28, 0, true},
	colCommand:  {"Command", 10, 1, true},
	colMode:     {"Mode", 12, 2, true},
	colUser:     {"User", 12, 3, true},
	colProgress: {"Progress", 0, 0, true},
	colDone:     {"✓", 5, 0, false},
	colFailed:   {"✗", 5, 0, false},
	colInProg:   {"◐", 5, 2, false},
	colPending:  {"○", 5, 2, false},
	colTime:     {"Time", 8, 0, true},
	colAge:      {"Age", 6, 2, true},
}

// headerGlyph returns the themed glyph for the four status-count column
// headers (✓ ✗ ◐ ○ under the quiet default), falling back to the column's
// static title for everything else. multiCols carries those four glyphs as
// static defaults because widths/priority are fixed layout, but the glyphs
// themselves must follow the active theme like every other glyph site
// (the leading status column, Indicator, renderStateGlyph).
func headerGlyph(c int, g theme.Glyphs, fallback string) string {
	switch c {
	case colDone:
		return g.StatusDone
	case colFailed:
		return g.StatusFailed
	case colInProg:
		return g.StatusInProgress
	case colPending:
		return g.StatusPending
	default:
		return fallback
	}
}

const minBarWidth = 10

// buildRows constructs a row for each command, computing counts and health.
func buildRows(cmds []apimodel.Command) []row {
	rows := make([]row, 0, len(cmds))
	for _, c := range cmds {
		counts := commandCounts(c)
		rows = append(rows, row{cmd: c, counts: counts, health: commandHealth(c, counts)})
	}
	return rows
}

// visibleColumns drops priority-3 columns first (User — supplementary
// attribution, never at the cost of operational columns a narrower terminal
// already showed), then priority-2 (Mode, ◐, ○, Age), then priority-1
// (Command), until the fixed columns plus the bar floor fit.
func visibleColumns(termWidth int) map[int]bool {
	vis := make(map[int]bool, colCount)
	for c := 0; c < colCount; c++ {
		vis[c] = true
	}
	for _, dropTier := range []int{3, 2, 1} {
		if fixedWidth(vis)+minBarWidth > termWidth {
			for c := 0; c < colCount; c++ {
				if multiCols[c].priority == dropTier {
					vis[c] = false
				}
			}
		}
	}
	return vis
}

// fixedWidth computes the total width of non-elastic columns.
func fixedWidth(vis map[int]bool) int {
	w := 0
	for c := 0; c < colCount; c++ {
		if vis[c] && c != colProgress {
			w += multiCols[c].width
		}
	}
	return w
}

// barWidth returns the width available for the progress bar, with a floor of minBarWidth.
func barWidth(termWidth int, vis map[int]bool) int {
	bw := termWidth - fixedWidth(vis)
	if bw < minBarWidth {
		return minBarWidth
	}
	return bw
}

// sortableColumns returns the column indexes that support sorting.
// Consumed by the root model for sort-column navigation (Left/Right keys).
func sortableColumns() []int {
	cols := make([]int, 0, colCount)
	for c := 0; c < colCount; c++ {
		if multiCols[c].sortable {
			cols = append(cols, c)
		}
	}
	return cols
}

// modeLabel returns the mode string to display. Destroy commands have no mode
// (the server defaults them to "patch"), so they render as "-".
func modeLabel(c apimodel.Command) string {
	if c.Command == "destroy" {
		return "-"
	}
	return c.Mode
}

// userLabel returns the display value for the User column: SubjectName when
// present, otherwise the raw Subject (the caller truncates it to the column
// width, which yields a short prefix), and blank when a command carries
// neither — scheduler-originated commands (sync, discovery, auto-reconcile,
// stack expiry) never populate either field.
func userLabel(c apimodel.Command) string {
	if c.SubjectName != "" {
		return c.SubjectName
	}
	return c.Subject
}

// lessRows compares two rows for a given column. Returns true if row a
// should sort before row b.
func lessRows(a, b row, col int, now time.Time) bool {
	switch col {
	case colStatus:
		return a.health < b.health
	case colID:
		return a.cmd.CommandID < b.cmd.CommandID
	case colCommand:
		return a.cmd.Command < b.cmd.Command
	case colMode:
		return a.cmd.Mode < b.cmd.Mode
	case colUser:
		return userLabel(a.cmd) < userLabel(b.cmd)
	case colProgress:
		return progressFraction(a.counts) < progressFraction(b.counts)
	case colTime:
		return commandDuration(a.cmd, now) < commandDuration(b.cmd, now)
	case colAge:
		return a.cmd.StartTs.Before(b.cmd.StartTs)
	}
	return false
}

// sortRows sorts the rows in-place by the given column and direction.
func sortRows(rows []row, col int, dir components.SortDirection, now time.Time) {
	sort.SliceStable(rows, func(i, j int) bool {
		if dir == components.SortDesc && col != colStatus {
			return lessRows(rows[j], rows[i], col, now)
		}
		return lessRows(rows[i], rows[j], col, now)
	})
}

// multiView is the rendering model for the multi-command summary table (VIEW 1).
// It is a pure value type — no pointer receivers — so callers can copy it freely.
type multiView struct {
	th       *theme.Theme
	rows     []row
	cursor   int
	sortCol  int
	sortDir  components.SortDirection
	sortHi   int // column highlighted by →← navigation
	width    int
	spinView string // current spinner frame, injected by the root model
	now      time.Time
	hideAge  bool // detail view's pinned row omits Age (mockup VIEW 2)
	pinned   bool // detail view's pinned command row: render full-brightness (it is the subject, not a de-emphasized finished list row)
}

// visibleCols returns the responsive column set, additionally dropping Age
// when hideAge is set.
func (v multiView) visibleCols() map[int]bool {
	vis := visibleColumns(v.width)
	if v.hideAge {
		vis[colAge] = false
	}
	return vis
}

// summaryOutcome classifies the visible commands for the top-right summary
// indicator: failed when any command has a failure (running or settled), success
// when every command has settled without failure, and running otherwise (work
// still in flight and healthy, or an empty list).
type summaryOutcome int

const (
	outcomeRunning summaryOutcome = iota
	outcomeSuccess
	outcomeFailed
)

func (v multiView) summaryOutcome() summaryOutcome {
	if len(v.rows) == 0 {
		return outcomeRunning
	}
	hasFailed, hasRunning := false, false
	for _, r := range v.rows {
		if healthFailed(r.health) {
			hasFailed = true
		}
		if r.health == healthRunningFailing || r.health == healthRunning {
			hasRunning = true
		}
	}
	switch {
	case hasFailed:
		return outcomeFailed
	case hasRunning:
		return outcomeRunning
	default:
		return outcomeSuccess
	}
}

// rowStyles returns the id and text lipgloss styles for a row based on its
// health and cursor state. Implements the mockup's row-coloring table:
// problems get color, done gets brightness, finished rows fade, the cursor
// brightens everything one tier.
func (v multiView) rowStyles(h health, cursor bool) (id, text lipgloss.Style) {
	p := v.th.Palette
	// The detail view's pinned command row is the subject being viewed, so it
	// renders at full brightness regardless of terminal state — the state is
	// already conveyed by the status glyph (✓/✗/⊘/spinner).
	if v.pinned {
		idc := p.TextPrimary
		if v.th.Rows.LabelAccent {
			idc = p.PrimaryAccent
		}
		return lipgloss.NewStyle().Foreground(idc),
			lipgloss.NewStyle().Foreground(p.TextPrimary)
	}
	var idc, txt lipgloss.AdaptiveColor
	switch h {
	case healthRunningFailing:
		idc, txt = p.Error, p.Error
	case healthFinishedFailed:
		// Match the detail screen's failed red (Error), not a dimmed variant,
		// so a canceled/failed command reads the same in the list and the drill-in.
		idc, txt = p.Error, p.Error
		if cursor {
			idc, txt = p.ErrorBright, p.ErrorBright
		}
	case healthFinishedOK:
		// The command id (the row's label) is the accent color, matching simview's
		// blue label and the running state; the rest of the row stays bright so a
		// settled command doesn't read as bland. Failed stays red for contrast.
		idc, txt = p.PrimaryAccent, p.TextPrimary
	default: // healthRunning
		idc, txt = p.PrimaryAccent, p.TextPrimary
	}
	// Themes that don't make the label special (quiet) render the command id in
	// the same color as the rest of the row — nothing special about a label.
	if !v.th.Rows.LabelAccent {
		idc = txt
	}
	id = lipgloss.NewStyle().Foreground(idc)
	text = lipgloss.NewStyle().Foreground(txt)
	if cursor {
		id = id.Background(p.Selection)
		text = text.Background(p.Selection)
	}
	return id, text
}

// pad right-pads or truncates s to exactly w visible rune columns.
// Uses utf8.RuneCountInString for measurement on plain (ANSI-free) strings,
// which is appropriate for all cell content we build before styling.
func pad(s string, w int) string {
	return components.Pad(s, w)
}

// padCenter centers s within w visible rune columns. Used for the status-glyph
// column, whose single glyph reads as centered under its (also centered) header
// sort arrow rather than hugging the left edge.
func padCenter(s string, w int) string {
	return components.PadCenter(s, w)
}

// headerRow renders the column header line with sort indicators. Emphasis of
// the navigated (sort-hi) and active-sort columns is theme-driven — mirrors
// simview's renderGroupColHeader (internal/cli/tui/simview/render.go):
//   - "background": the navigated column gets a background highlight (like
//     the row cursor, using Selection.Dark); the active-sort column gets the
//     accent color. Navigated takes precedence over active-sort.
//   - "brighten" (default for unknown values): navigated OR active-sort both
//     render bright white, no background — today's quiet/colorblind behavior.
//
// All layout derives from multiCols[c].width.
func (v multiView) headerRow() string {
	p := v.th.Palette
	vis := v.visibleCols()
	bw := barWidth(v.width, vis)

	dimStyle := lipgloss.NewStyle().
		Foreground(p.TextSecondary).
		Bold(true)

	background := v.th.Header.Highlight == "background"
	accentStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true)
	hiStyle := lipgloss.NewStyle().Foreground(p.TextPrimary).Bold(true)
	// hlStyle colours the pending sort-target column (moved by ←/→). It uses the
	// SecondaryAccent foreground in every header mode so it reads as a distinct
	// "cursor" colour — never an underline, and never the Selection background
	// used by the row cursor (which would make the two indistinguishable).
	hlStyle := lipgloss.NewStyle().Foreground(p.SecondaryAccent).Bold(true)

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

	var sb strings.Builder
	for c := 0; c < colCount; c++ {
		if !vis[c] {
			continue
		}
		spec := multiCols[c]
		w := spec.width
		if c == colProgress {
			w = bw
		}

		title := headerGlyph(c, v.th.Glyphs, spec.title)
		if v.sortCol == c {
			switch v.sortDir {
			case components.SortAsc:
				title += " ▲"
			case components.SortDesc:
				title += " ▼"
			}
		}

		cell := pad(title, w)
		if c == colStatus {
			// The status column has no title text, only an optional sort arrow.
			// Center the bare arrow (dropping the separator space that would
			// otherwise offset it) so it sits over the centered row glyph.
			cell = padCenter(strings.TrimSpace(title), w)
		}
		st := styleFor(v.sortHi == c, v.sortCol == c)
		sb.WriteString(st.Render(cell))
	}
	return sb.String()
}

// scrollOffset computes the scroll offset so the cursor row stays visible in
// a window of visible rows.
func scrollOffset(cursor, total, visible int) int {
	off := 0
	if cursor >= visible {
		off = cursor - visible + 1
	}
	if max := total - visible; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	return off
}

// renderRows renders up to maxRows data rows windowed around the cursor.
// Each rendered string has exactly fixedWidth(vis)+barWidth(width,vis) visible
// rune columns so the table stays aligned.
func (v multiView) renderRows(maxRows int) []string {
	if len(v.rows) == 0 {
		return nil
	}
	vis := v.visibleCols()
	bw := barWidth(v.width, vis)
	p := v.th.Palette

	// Fixed-width count field for the Progress column: reserve the widest
	// " done/total " across all in-progress rows so the bar (which fills the
	// rest) is the same width on every row. Otherwise barW would shrink with
	// the digit count of done/total, making bars look like different widths.
	progCountW := 0
	for _, r := range v.rows {
		if isTerminalCommand(r.cmd.State) {
			continue
		}
		d, t := doneOf(r.counts)
		if l := utf8.RuneCountInString(fmt.Sprintf(" %d/%d ", d, t)); l > progCountW {
			progCountW = l
		}
	}

	off := scrollOffset(v.cursor, len(v.rows), maxRows)
	end := off + maxRows
	if end > len(v.rows) {
		end = len(v.rows)
	}

	result := make([]string, 0, end-off)
	for ri := off; ri < end; ri++ {
		r := v.rows[ri]
		isCursor := ri == v.cursor
		idStyle, textStyle := v.rowStyles(r.health, isCursor)
		terminal := isTerminalCommand(r.cmd.State)

		// status glyph
		var glyphStr string
		switch {
		case r.cmd.State == "Canceled":
			glyphStr = textStyle.Render(padCenter(v.th.Glyphs.StatusCanceled, multiCols[colStatus].width))
		case terminal && r.health == healthFinishedOK:
			glyphStr = textStyle.Render(padCenter(v.th.Glyphs.StatusDone, multiCols[colStatus].width))
		case terminal && (r.health == healthFinishedFailed):
			errSt := lipgloss.NewStyle().Foreground(p.Error)
			if isCursor {
				errSt = errSt.Background(p.Selection).Foreground(p.ErrorBright)
			}
			glyphStr = errSt.Render(padCenter(v.th.Glyphs.StatusFailed, multiCols[colStatus].width))
		default:
			// running — static in-progress glyph. Not animated: the row already
			// shows live progress (counts/bar), so an animated spinner here would
			// be a redundant second spinner next to the command ID.
			glyphSt := lipgloss.NewStyle().Foreground(p.InProgress)
			if r.health == healthRunningFailing {
				glyphSt = lipgloss.NewStyle().Foreground(p.Error)
			}
			if isCursor {
				glyphSt = glyphSt.Background(p.Selection)
			}
			glyphStr = glyphSt.Render(padCenter(v.th.Glyphs.StatusInProgress, multiCols[colStatus].width))
		}

		done, total := doneOf(r.counts)
		dur := commandDuration(r.cmd, v.now)

		// build cells per column
		var sb strings.Builder
		for c := 0; c < colCount; c++ {
			if !vis[c] {
				continue
			}
			spec := multiCols[c]
			w := spec.width
			if c == colProgress {
				w = bw
			}

			switch c {
			case colStatus:
				sb.WriteString(glyphStr)
			case colID:
				// Truncate to w-1 so an over-long ksuid always leaves a trailing
				// gap before the next column (pad alone would fill the full width).
				sb.WriteString(idStyle.Render(pad(components.Truncate(r.cmd.CommandID, w-1), w)))
			case colCommand:
				sb.WriteString(textStyle.Render(pad(r.cmd.Command, w)))
			case colMode:
				sb.WriteString(textStyle.Render(pad(modeLabel(r.cmd), w)))
			case colUser:
				// Truncate to w-1 so a bare Subject (typically a UUID, longer
				// than the column) renders as a short prefix with a trailing
				// gap before the next column, same idiom as colID.
				sb.WriteString(textStyle.Render(pad(components.Truncate(userLabel(r.cmd), w-1), w)))
			case colProgress:
				if terminal {
					verb := "completed"
					switch {
					case r.cmd.State == "Canceled":
						verb = "canceled"
					case r.health == healthFinishedFailed:
						verb = "failed"
					}
					// Truncate to w-1 so a squeezed progress column always leaves a
					// trailing gap before the ✓ column (pad alone fills the full
					// width and the count would abut — e.g. "completed 1" + "1").
					cell := pad(components.Truncate(fmt.Sprintf("%s %d/%d", verb, done, total), w-1), w)
					sb.WriteString(textStyle.Render(cell))
				} else {
					// segmented bar + count; bar is bw-8 wide, count is right-aligned remainder.
					// The trailing space is REQUIRED: the count fills the column exactly,
					// so without it the next (✓) column abuts the total — e.g. total 15
					// + ✓ 4 rendered as "4/154". The gap keeps them separate.
					countStr := fmt.Sprintf(" %d/%d ", done, total)
					barW := w - progCountW
					if barW < 1 {
						barW = 1
					}
					bar := components.ProgressBar(v.th, barW, r.counts, healthFailed(r.health))
					cell := bar + textStyle.Render(pad(countStr, w-barW))
					sb.WriteString(cell)
				}
			case colDone:
				n := r.counts[components.StateDone]
				st := lipgloss.NewStyle().Foreground(p.Done)
				if n == 0 || terminal {
					st = textStyle
				}
				if isCursor {
					st = st.Background(p.Selection)
				}
				sb.WriteString(st.Render(pad(fmt.Sprintf("%d", n), w)))
			case colFailed:
				n := r.counts[components.StateFailed]
				st := lipgloss.NewStyle().Foreground(p.Error)
				if n == 0 || terminal {
					st = textStyle
				}
				if isCursor {
					st = st.Background(p.Selection)
				}
				sb.WriteString(st.Render(pad(fmt.Sprintf("%d", n), w)))
			case colInProg:
				n := r.counts[components.StateInProgress]
				st := lipgloss.NewStyle().Foreground(p.InProgress)
				if n == 0 || terminal {
					st = textStyle
				}
				if isCursor {
					st = st.Background(p.Selection)
				}
				sb.WriteString(st.Render(pad(fmt.Sprintf("%d", n), w)))
			case colPending:
				n := r.counts[components.StatePending]
				st := lipgloss.NewStyle().Foreground(p.Pending)
				if n == 0 || terminal {
					st = textStyle
				}
				if isCursor {
					st = st.Background(p.Selection)
				}
				sb.WriteString(st.Render(pad(fmt.Sprintf("%d", n), w)))
			case colTime:
				sb.WriteString(textStyle.Render(pad(components.FormatDuration(dur), w)))
			case colAge:
				sb.WriteString(textStyle.Render(pad(components.FormatAge(r.cmd.StartTs, v.now), w)))
			}
		}
		result = append(result, sb.String())
	}
	return result
}
