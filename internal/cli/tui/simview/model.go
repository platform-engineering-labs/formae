// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// ansiEscape matches ANSI SGR escape sequences for stripping display colors.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Kind identifies the command kind for the simulation preview.
type Kind int

const (
	KindApply   Kind = iota
	KindDestroy Kind = iota
)

// Decision records the user's final choice.
type Decision int

const (
	DecisionAborted   Decision = iota
	DecisionConfirmed Decision = iota
)

// Options configures the simulation preview model.
type Options struct {
	Kind         Kind
	Mode         string
	Source       string
	SimulateOnly bool
	Description  apimodel.Description
}

// screenKind identifies which screen is currently displayed.
type screenKind int

const (
	screenAck     screenKind = iota // description acknowledgment screen
	screenPreview screenKind = iota // main simulation preview
)

// navLineKind classifies a navigable line.
type navLineKind int

const (
	navRow      navLineKind = iota
	navShowMore navLineKind = iota
)

// navLine is one cursor position in the flat navigable list.
type navLine struct {
	kind    navLineKind
	rowKind rowKind
	rowKey  string
	rowIdx  int
}

// simviewChromeLines is the total fixed lines consumed by header + summary + footer.
//
//	HeaderBar: 2 lines (content + bottom border)
//	Summary line: 1 line
//	FooterBar: 2 lines (top border + content)
const simviewChromeLines = 5

// Column index constants for simview sort highlighting.
const (
	colOp    = 0
	colLabel = 1
	colType  = 2
	colStack = 3
)

// Model is the bubbletea model for the interactive simulation preview table.
type Model struct {
	th       *theme.Theme
	opts     Options
	cmd      apimodel.Command // stored for footer delegation to components.PromptForOperations
	groups   []simGroup
	cursor   int
	visible  map[rowKind]int
	sortHi   map[rowKind]int
	sortCol  map[rowKind]int
	sortDir  map[rowKind]components.SortDirection
	keys     tui.KeyMap
	expanded map[string]bool // keyed by simRow.key

	screen   screenKind
	decision Decision
	vp       viewport.Model
	width    int
	height   int
}

// New builds a simview Model from a Simulation and Options.
func New(th *theme.Theme, sim *apimodel.Simulation, opts Options) Model {
	groups := buildSimGroups(&sim.Command)
	vp := viewport.New(80, 20)

	// Start on ack screen if the description requires confirmation.
	initialScreen := screenPreview
	if opts.Description.Confirm && opts.Description.Text != "" {
		initialScreen = screenAck
	}

	return Model{
		th:     th,
		opts:   opts,
		cmd:    sim.Command,
		groups: groups,
		cursor: 0,
		visible: map[rowKind]int{
			kindTarget:   10,
			kindStack:    10,
			kindPolicy:   10,
			kindResource: 10,
		},
		sortHi: map[rowKind]int{
			kindTarget:   colOp,
			kindStack:    colOp,
			kindPolicy:   colOp,
			kindResource: colOp,
		},
		sortCol: map[rowKind]int{
			kindTarget:   colOp,
			kindStack:    colOp,
			kindPolicy:   colOp,
			kindResource: colOp,
		},
		sortDir: map[rowKind]components.SortDirection{
			kindTarget:   components.SortAsc,
			kindStack:    components.SortAsc,
			kindPolicy:   components.SortAsc,
			kindResource: components.SortAsc,
		},
		keys:     tui.DefaultKeyMap(),
		screen:   initialScreen,
		decision: DecisionAborted,
		vp:       vp,
		expanded: make(map[string]bool),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Decision returns the user's final decision.
func (m Model) Decision() Decision {
	return m.decision
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := max(m.height-simviewChromeLines, 1)
		m.vp = viewport.New(m.width, vpH)
		return m, nil

	case tea.KeyMsg:
		// Ack screen: only enter (proceed) and q/esc/ctrl-c (abort).
		if m.screen == screenAck {
			if msg.Type == tea.KeyEnter {
				m.screen = screenPreview
				return m, nil
			}
			if msg.String() == "q" || msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
				m.decision = DecisionAborted
				return m, tea.Quit
			}
			// All other keys ignored on ack screen.
			return m, nil
		}

		// Preview screen quit keys.
		if msg.String() == "q" || msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
			m.decision = DecisionAborted
			return m, tea.Quit
		}
		// y confirms — unless SimulateOnly (then y is a no-op).
		if msg.String() == "y" {
			if !m.opts.SimulateOnly {
				m.decision = DecisionConfirmed
				return m, tea.Quit
			}
			// SimulateOnly: y is silently ignored.
			return m, nil
		}

		nav := m.navLines()
		total := len(nav)

		switch {
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}

		case key.Matches(msg, m.keys.Down):
			if m.cursor < total-1 {
				m.cursor++
			}

		case key.Matches(msg, m.keys.PageUp):
			m.cursor -= m.vp.Height
			if m.cursor < 0 {
				m.cursor = 0
			}

		case key.Matches(msg, m.keys.PageDown):
			m.cursor += m.vp.Height
			if m.cursor >= total {
				m.cursor = total - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}

		case msg.Type == tea.KeyLeft || key.Matches(msg, m.keys.Left):
			rk := m.cursorRowKind()
			cols := validSimSortCols(rk)
			hi := m.sortHi[rk]
			idx := simSortColIndex(cols, hi)
			idx = (idx - 1 + len(cols)) % len(cols)
			m.sortHi[rk] = cols[idx]

		case msg.Type == tea.KeyRight || key.Matches(msg, m.keys.Right):
			rk := m.cursorRowKind()
			cols := validSimSortCols(rk)
			hi := m.sortHi[rk]
			idx := simSortColIndex(cols, hi)
			idx = (idx + 1) % len(cols)
			m.sortHi[rk] = cols[idx]

		case key.Matches(msg, m.keys.Sort):
			rk := m.cursorRowKind()
			hi := m.sortHi[rk]
			act := m.sortCol[rk]
			dir := m.sortDir[rk]
			if act == hi {
				if dir == components.SortAsc {
					m.sortDir[rk] = components.SortDesc
				} else {
					m.sortDir[rk] = components.SortAsc
				}
			} else {
				m.sortCol[rk] = hi
				m.sortDir[rk] = components.SortAsc
			}
			g := m.groupForKind(rk)
			if g != nil {
				sortRows(g.rows, modelColToDataCol(m.sortCol[rk]), m.sortDir[rk])
			}
			m.visible[rk] = 10
			m.cursor = 0

		case key.Matches(msg, m.keys.Enter) || msg.Type == tea.KeySpace:
			if m.cursor >= 0 && m.cursor < total {
				line := nav[m.cursor]
				if line.kind == navShowMore {
					m.visible[line.rowKind] += 10
				} else if line.kind == navRow {
					// Toggle card expansion by row key.
					if m.expanded[line.rowKey] {
						delete(m.expanded, line.rowKey)
					} else {
						m.expanded[line.rowKey] = true
					}
				}
			}
		}
	}

	return m, nil
}

// View implements tea.Model and produces exactly m.height lines.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	if m.screen == screenAck {
		return m.viewAck()
	}
	return m.viewPreview()
}

// viewAck renders the description acknowledgment screen (exactly m.height lines).
func (m Model) viewAck() string {
	header := components.HeaderBar(m.th, m.headerLeft(), m.headerRight(), m.width)
	footer := components.FooterBar(m.th, m.width, ackFooterHints(), "")

	// The viewport body contains the description panel + enter prompt.
	vpH := max(m.height-simviewChromeLines, 1)
	m.vp.Width = m.width
	m.vp.Height = vpH

	p := m.th.Palette
	enterStyle := lipgloss.NewStyle().Foreground(p.TextPrimary)
	subtleStyle := lipgloss.NewStyle().Foreground(p.TextSubtle)

	// Word-wrap description text to panel inner width.
	// Panel: 2 outer indent + NormalBorder(1 left +1 right) + 1 padding each side = 6 total.
	innerW := m.width - 6
	if innerW < 20 {
		innerW = 20
	}
	wrapped := wrapText(m.opts.Description.Text, innerW)
	panelContent := wrapped
	panelW := m.width - 4 // total width minus 2 indent on each side
	if panelW < 24 {
		panelW = 24
	}
	panel := lipgloss.NewStyle().
		Foreground(p.Border).
		Border(lipgloss.NormalBorder()).
		BorderForeground(p.Border).
		Padding(0, 1).
		Width(panelW - 2). // -2 for borders
		Render(panelContent)

	var bodyBuf strings.Builder
	bodyBuf.WriteString("\n")
	// Indent panel by 2 spaces
	for _, pl := range strings.Split(panel, "\n") {
		bodyBuf.WriteString("  " + pl + "\n")
	}
	bodyBuf.WriteString("\n")
	bodyBuf.WriteString("  " + enterStyle.Render("Press enter to continue...") + "\n")
	bodyBuf.WriteString("\n")
	bodyBuf.WriteString("  " + subtleStyle.Render("(simulation output follows after acknowledgment)") + "\n")

	m.vp.SetContent(bodyBuf.String())

	// Use an empty string for the summary placeholder line to match chrome.
	summaryLine := ""

	parts := header + "\n" + summaryLine + "\n" + m.vp.View() + "\n" + footer
	lines := strings.Split(parts, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// viewPreview renders the main simulation preview (exactly m.height lines).
func (m Model) viewPreview() string {
	header := components.HeaderBar(m.th, m.headerLeft(), m.headerRight(), m.width)
	summaryLine := "  " + m.renderSummaryCounts()
	footer := m.renderFooter()

	// Compute chrome line count dynamically: header(2) + summary(1) + footer(variable).
	footerLines := strings.Count(footer, "\n") + 1
	chromeLines := 3 + footerLines // header(2) + summary(1) + footer
	vpH := max(m.height-chromeLines, 1)
	m.vp.Width = m.width
	m.vp.Height = vpH

	body, cursorLine := m.renderBody()
	m.vp.SetContent(body)

	// Scroll to keep cursor in view.
	if cursorLine < m.vp.YOffset {
		m.vp.YOffset = cursorLine
	}
	if cursorLine >= m.vp.YOffset+vpH {
		m.vp.YOffset = cursorLine - vpH + 1
	}

	parts := header + "\n" + summaryLine + "\n" + m.vp.View() + "\n" + footer
	lines := strings.Split(parts, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// headerLeft returns the header's left-side text.
func (m Model) headerLeft() string {
	verb := "apply"
	if m.opts.Kind == KindDestroy {
		verb = "destroy"
	}
	return "formae " + verb + " — simulation"
}

// headerRight returns the header's right-side text.
// Shows Source if set, otherwise mode.
func (m Model) headerRight() string {
	if m.opts.Source != "" {
		return m.opts.Source
	}
	if m.opts.Mode != "" {
		return "mode: " + m.opts.Mode
	}
	return ""
}

// navLines computes the flat navigable list given current pagination state.
func (m Model) navLines() []navLine {
	var lines []navLine
	for _, g := range m.groups {
		lim := m.visible[g.kind]
		shown, remaining := simVisibleRows(g, lim)
		for i, r := range shown {
			lines = append(lines, navLine{
				kind:    navRow,
				rowKind: g.kind,
				rowKey:  r.key,
				rowIdx:  i,
			})
		}
		if remaining > 0 {
			lines = append(lines, navLine{
				kind:    navShowMore,
				rowKind: g.kind,
			})
		}
	}
	return lines
}

// groupForKind returns a pointer to the group with the given rowKind.
func (m Model) groupForKind(k rowKind) *simGroup {
	for i := range m.groups {
		if m.groups[i].kind == k {
			return &m.groups[i]
		}
	}
	return nil
}

// cursorRowKind returns the rowKind of the group the cursor is in.
func (m Model) cursorRowKind() rowKind {
	nav := m.navLines()
	if m.cursor >= 0 && m.cursor < len(nav) {
		return nav[m.cursor].rowKind
	}
	return kindResource
}

// footerHints returns the key hints for the preview footer bar (with y confirm).
func footerHints() []components.KeyHint {
	return []components.KeyHint{
		{Key: "↑↓", Desc: "select"},
		{Key: "space", Desc: "expand"},
		{Key: "→←", Desc: "column"},
		{Key: "s", Desc: "sort"},
		{Key: "y", Desc: "confirm"},
		{Key: "q", Desc: "abort"},
	}
}

// ackFooterHints returns key hints for the ack screen.
func ackFooterHints() []components.KeyHint {
	return []components.KeyHint{
		{Key: "enter", Desc: "continue"},
		{Key: "q", Desc: "abort"},
	}
}

// renderFooter renders the footer with variant text based on Options.
// SimulateOnly → single-line footer: "Command will not continue — simulation only"
// KindDestroy with cascades → multi-line footer with cascade counts
// Otherwise → single-line footer with PromptForOperations-style confirm
func (m Model) renderFooter() string {
	p := m.th.Palette

	if m.opts.SimulateOnly {
		msg := lipgloss.NewStyle().Foreground(p.TextSubtle).Render("  Command will not continue — simulation only")
		return lipgloss.NewStyle().
			Width(m.width).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(p.Border).
			Render(msg)
	}

	// Destroy with cascades: multi-line footer.
	if m.opts.Kind == KindDestroy {
		totalDeletes, cascadeDeletes := countDestroyResources(m.groups)
		if cascadeDeletes > 0 {
			line1 := fmt.Sprintf("  This operation will delete %d resource(s), of which %d will be deleted", totalDeletes, cascadeDeletes)
			line2 := "  because they depend on other targeted resources."
			line3 := ""
			line4 := "  Do you want to continue? (y/N)"
			content := line1 + "\n" + line2 + "\n" + line3 + "\n" + line4
			return lipgloss.NewStyle().
				Width(m.width).
				BorderStyle(lipgloss.NormalBorder()).
				BorderTop(true).
				BorderForeground(p.Border).
				Render(content)
		}
	}

	// Non-cascade confirm: delegate to components.PromptForOperations so that
	// targets, stacks, and policies are labelled correctly (not all "resource(s)"),
	// and the final separator is "and" instead of comma-only.
	raw := components.PromptForOperations(&m.cmd)
	var confirmMsg string
	if raw != "" {
		// PromptForOperations returns ANSI-colored text with a trailing
		// "\n\nDo you want to continue?" framing.  Strip ANSI codes (the simview
		// uses theme roles, not display-package colors) then extract just the
		// one-line summary.
		stripped := ansiEscape.ReplaceAllString(raw, "")
		// Split on the double-newline separator and take the first part.
		summary := strings.SplitN(stripped, "\n\n", 2)[0]
		// Defensively collapse any remaining newlines (e.g. very long commands).
		summary = strings.ReplaceAll(summary, "\n", " ")
		confirmMsg = summary + "  Do you want to continue? (y/N)"
	}
	return components.FooterBar(m.th, m.width, footerHints(), confirmMsg)
}

// countDestroyResources returns total delete count and cascade delete count
// across all resource groups.
func countDestroyResources(groups []simGroup) (total, cascades int) {
	for _, g := range groups {
		if g.kind != kindResource {
			continue
		}
		for _, r := range g.rows {
			if r.op == opDelete {
				total++
				if r.cascade {
					cascades++
				}
			}
		}
	}
	return
}

// wrapText wraps plain text to maxWidth runes per line, breaking at spaces.
func wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var lines []string
	line := ""
	for _, w := range words {
		if line == "" {
			line = w
			continue
		}
		candidate := line + " " + w
		if len([]rune(candidate)) <= maxWidth {
			line = candidate
		} else {
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// simVisibleRows returns the visible slice and remaining count for a group.
func simVisibleRows(g simGroup, limit int) ([]simRow, int) {
	n := len(g.rows)
	if limit > n {
		limit = n
	}
	rows := make([]simRow, limit)
	copy(rows, g.rows[:limit])
	return rows, n - limit
}

// validSimSortCols returns the valid sort column indexes for a rowKind.
func validSimSortCols(k rowKind) []int {
	switch k {
	case kindPolicy:
		return []int{colOp, colLabel, colType, colStack}
	case kindResource:
		return []int{colOp, colLabel, colType}
	default:
		return []int{colOp, colLabel}
	}
}

// simSortColIndex returns the index of col in cols, or 0 if not found.
func simSortColIndex(cols []int, col int) int {
	for i, c := range cols {
		if c == col {
			return i
		}
	}
	return 0
}

// modelColToDataCol maps model column constants to data.go sortRows column indexes.
// Model: colOp=0, colLabel=1, colType=2, colStack=3
// Data:  0=label, 1=op(word), 2=type, 3=stack
func modelColToDataCol(col int) int {
	switch col {
	case colOp:
		return 1
	case colLabel:
		return 0
	case colType:
		return 2
	case colStack:
		return 3
	}
	return 0
}
