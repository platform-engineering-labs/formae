// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package simview

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

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
	th      *theme.Theme
	opts    Options
	groups  []simGroup
	cursor  int
	visible map[rowKind]int
	sortHi  map[rowKind]int
	sortCol map[rowKind]int
	sortDir map[rowKind]components.SortDirection
	keys    tui.KeyMap

	decision Decision
	vp       viewport.Model
	width    int
	height   int
}

// New builds a simview Model from a Simulation and Options.
func New(th *theme.Theme, sim *apimodel.Simulation, opts Options) Model {
	groups := buildSimGroups(&sim.Command)
	vp := viewport.New(80, 20)
	return Model{
		th:     th,
		opts:   opts,
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
		decision: DecisionAborted,
		vp:       vp,
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
		// Quit keys
		if msg.String() == "y" {
			m.decision = DecisionConfirmed
			return m, tea.Quit
		}
		if msg.String() == "q" || msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
			m.decision = DecisionAborted
			return m, tea.Quit
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
				}
				// Card expansion is Task 10 — no-op for row rows.
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

	header := components.HeaderBar(m.th, m.headerLeft(), m.headerRight(), m.width)
	summaryLine := "  " + m.renderSummaryCounts()
	footer := components.FooterBar(m.th, m.width, footerHints(), "")

	vpH := max(m.height-simviewChromeLines, 1)
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
func (m Model) headerRight() string {
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

// footerHints returns the key hints for the footer bar.
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
