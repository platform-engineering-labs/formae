// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// Options configures the status/watch TUI. The public surface here is consumed
// by Task 13 (status command wiring) and PLA-283 (apply/destroy handoff).
type Options struct {
	// Query is the initial server-side filter string (--query flag).
	Query string
	// MaxResults is the page size passed to the API.
	MaxResults int
	// PollInterval controls how often the API is polled; default 2s.
	PollInterval time.Duration
	// Now is an injectable clock used for duration/age rendering; default time.Now.
	Now func() time.Time
	// FocusCommandID is the command ID to start drilled into — wired in Task 11
	// (detail view). Stored here to preserve the public Options contract.
	FocusCommandID string
	// ExitWhenDone causes the TUI to quit automatically when all visible
	// commands reach a terminal state.
	ExitWhenDone bool
}

// viewMode controls which TUI panel is active.
type viewMode int

const (
	viewMulti  viewMode = iota // multi-command summary list
	viewDetail                 // single-command drill-in
)

// Model is the root bubbletea model for the status/watch TUI.
// It wires together polling (poll.go), the query bar (querybar.go), and the
// multi-command table view (multiview.go).
type Model struct {
	th      *theme.Theme
	client  Client
	opts    Options
	keys    tui.KeyMap
	multi   multiView
	detail  detailModel
	view    viewMode
	query   queryBar
	spinner spinner.Model
	err     error
	width   int
	height  int
	ready   bool
	// focusHandled tracks whether FocusCommandID has been used to drill in yet.
	focusHandled bool
	// helpOpen tracks whether the help overlay is currently displayed.
	helpOpen bool
}

// New constructs a Model with sensible defaults applied to opts.
func New(th *theme.Theme, client Client, opts Options) Model {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = 10
	}
	return Model{
		th:      th,
		client:  client,
		opts:    opts,
		keys:    tui.DefaultKeyMap(),
		multi:   multiView{th: th, sortCol: colStatus, sortDir: components.SortAsc},
		detail:  newDetailModel(th, 80, 24), // placeholder; resized on WindowSizeMsg
		query:   newQueryBar(th, opts.Query),
		spinner: components.NewSpinner(th),
	}
}

// Init kicks off the first fetch, the poll ticker, and the spinner animation.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchCommands(m.client, m.query.Query(), m.opts.MaxResults),
		tick(m.opts.PollInterval),
		m.spinner.Tick,
	)
}

// Update handles all incoming messages and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.multi.width = msg.Width
		m.detail.width = msg.Width
		vpH := max(msg.Height-chromeLines-2, 1)
		m.detail.vp.Width = msg.Width
		m.detail.vp.Height = vpH
		m.ready = true
		return m, nil

	case commandsMsg:
		if msg.err != nil {
			// Keep stale rows; show error in header until next successful poll.
			m.err = msg.err
			return m, nil
		}
		m.err = nil

		// Remember which command the cursor is on before rebuilding rows.
		anchorID := ""
		if m.multi.cursor >= 0 && m.multi.cursor < len(m.multi.rows) {
			anchorID = m.multi.rows[m.multi.cursor].cmd.CommandID
		}

		m.multi.now = m.opts.Now()
		m.multi.rows = buildRows(msg.commands)
		sortRows(m.multi.rows, m.multi.sortCol, m.multi.sortDir, m.multi.now)

		// Re-anchor cursor to the same command ID (fall back to clamped index).
		m.multi.cursor = reanchorCursor(m.multi.rows, anchorID, m.multi.cursor)

		// FocusCommandID: on first successful poll, if the command is present, drill in.
		if m.opts.FocusCommandID != "" && !m.focusHandled {
			for _, r := range m.multi.rows {
				if r.cmd.CommandID == m.opts.FocusCommandID {
					m.focusHandled = true
					m.detail = m.detail.SetCommand(r.cmd, r, m.spinner.View(), m.opts.Now())
					m.view = viewDetail
					break
				}
			}
		}

		// If we are in detail view, refresh the detail model with the fresh data.
		if m.view == viewDetail {
			found := false
			for _, r := range m.multi.rows {
				if r.cmd.CommandID == m.detail.cmdID {
					found = true
					m.detail = m.detail.SetCommand(r.cmd, r, m.spinner.View(), m.opts.Now())
					break
				}
			}
			if !found {
				// Command vanished from results — fall back to multi view.
				m.view = viewMulti
			}
		}

		// ExitWhenDone: quit when all visible commands are terminal (and there
		// is at least one row — avoid quitting immediately on empty responses).
		if m.opts.ExitWhenDone && len(m.multi.rows) > 0 && allTerminal(m.multi.rows) {
			return m, tea.Quit
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(
			fetchCommands(m.client, m.query.Query(), m.opts.MaxResults),
			tick(m.opts.PollInterval),
		)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.multi.spinView = m.spinner.View()
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey routes keyboard input.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle ctrl+c before query bar routing: quit on ctrl+c even while editing query.
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	// Help overlay: any key closes it, except q/ctrl+c which also quit.
	if m.helpOpen {
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		default:
			m.helpOpen = false
			return m, nil
		}
	}

	// Query bar has priority when it is focused.
	if m.query.Focused() {
		var applied bool
		m.query, applied = m.query.Update(msg)
		if applied {
			// User pressed enter: refetch immediately with the new query.
			return m, fetchCommands(m.client, m.query.Query(), m.opts.MaxResults)
		}
		return m, nil
	}

	// In detail view, route keys to detail model (except '/', 'q', '?').
	if m.view == viewDetail {
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Search):
			m.query = m.query.Focus()
			return m, nil
		case key.Matches(msg, m.keys.Help):
			m.helpOpen = true
			return m, nil
		default:
			var back bool
			m.detail, back = m.detail.Update(msg, m.keys)
			if back {
				m.view = viewMulti
			}
			return m, nil
		}
	}

	visible := m.height - chromeLines
	if visible < 1 {
		visible = 1
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Search):
		m.query = m.query.Focus()
		return m, nil

	case key.Matches(msg, m.keys.Up):
		if m.multi.cursor > 0 {
			m.multi.cursor--
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.multi.cursor < len(m.multi.rows)-1 {
			m.multi.cursor++
		}
		return m, nil

	case key.Matches(msg, m.keys.PageUp):
		m.multi.cursor -= visible
		if m.multi.cursor < 0 {
			m.multi.cursor = 0
		}
		return m, nil

	case key.Matches(msg, m.keys.PageDown):
		m.multi.cursor += visible
		if m.multi.cursor >= len(m.multi.rows) {
			m.multi.cursor = len(m.multi.rows) - 1
		}
		if m.multi.cursor < 0 {
			m.multi.cursor = 0
		}
		return m, nil

	case key.Matches(msg, m.keys.Left):
		cols := sortableColumns()
		if len(cols) > 0 {
			// Find current sortHi index in sortable cols and move left.
			idx := sortHiIndex(cols, m.multi.sortHi)
			idx = (idx - 1 + len(cols)) % len(cols)
			m.multi.sortHi = cols[idx]
		}
		return m, nil

	case key.Matches(msg, m.keys.Right):
		cols := sortableColumns()
		if len(cols) > 0 {
			idx := sortHiIndex(cols, m.multi.sortHi)
			idx = (idx + 1) % len(cols)
			m.multi.sortHi = cols[idx]
		}
		return m, nil

	case key.Matches(msg, m.keys.Sort):
		anchorID := ""
		if m.multi.cursor >= 0 && m.multi.cursor < len(m.multi.rows) {
			anchorID = m.multi.rows[m.multi.cursor].cmd.CommandID
		}
		if m.multi.sortCol == m.multi.sortHi {
			// Flip direction on the active column.
			if m.multi.sortDir == components.SortAsc {
				m.multi.sortDir = components.SortDesc
			} else {
				m.multi.sortDir = components.SortAsc
			}
		} else {
			// Activate the highlighted column, start descending.
			m.multi.sortCol = m.multi.sortHi
			m.multi.sortDir = components.SortDesc
		}
		sortRows(m.multi.rows, m.multi.sortCol, m.multi.sortDir, m.multi.now)
		m.multi.cursor = reanchorCursor(m.multi.rows, anchorID, m.multi.cursor)
		return m, nil

	case key.Matches(msg, m.keys.Enter):
		if m.multi.cursor >= 0 && m.multi.cursor < len(m.multi.rows) {
			r := m.multi.rows[m.multi.cursor]
			m.detail = newDetailModel(m.th, m.width, m.height)
			m.detail = m.detail.SetCommand(r.cmd, r, m.spinner.View(), m.opts.Now())
			m.view = viewDetail
		}
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.helpOpen = true
		return m, nil
	}

	return m, nil
}

// chromeLines is the number of lines consumed by fixed UI chrome:
//
//	HeaderBar (2) + column header (1) + query bar (2) + footer bar (2) = 7.
//
// The data rows area receives height - chromeLines lines.
const chromeLines = 7

// View composes the full terminal screen.
func (m Model) View() string {
	if !m.ready {
		return ""
	}

	// Header: error banner replaces the "↻ live" status when last poll failed.
	right := "↻ live"
	if m.err != nil {
		right = lipgloss.NewStyle().
			Foreground(m.th.Palette.Error).
			Render("⚠ " + m.err.Error())
	}

	// Help overlay: render the help panel instead of the body between header and footer
	if m.helpOpen {
		header := components.HeaderBar(m.th, "formae status command", right, m.width)
		footer := components.FooterBar(m.th, m.width, multiFooterHints(), "")

		// Calculate body height: total height - header (2) - footer (2)
		bodyHeight := m.height - 4
		if bodyHeight < 1 {
			bodyHeight = 1
		}

		// Render help panel centered in the body area
		helpPanel := renderHelpOverlay(m.th, m.width, bodyHeight)

		// Assemble exactly: header + body (with centered panel) + footer
		// No interstitial blank lines, no post-padding needed
		parts := header + "\n" + helpPanel + "\n" + footer
		lines := strings.Split(parts, "\n")

		// Trim or pad to exact height
		if len(lines) > m.height {
			lines = lines[:m.height]
		} else {
			for len(lines) < m.height {
				lines = append(lines, "")
			}
		}
		return strings.Join(lines, "\n")
	}

	if m.view == viewDetail {
		header := components.HeaderBar(m.th, "← esc/backspace", right, m.width)
		detailContent := m.detail.View(m.height)
		queryView := m.query.View(m.width)
		// Detail view: header + detail (incl. pinned cmd row + sep + vp + footer)
		// We need to count lines to match height exactly
		parts := header + "\n" + detailContent
		lines := strings.Split(parts, "\n")
		// Pad to height
		for len(lines) < m.height {
			lines = append(lines, "")
		}
		if len(lines) > m.height {
			lines = lines[:m.height]
		}
		_ = queryView
		return strings.Join(lines, "\n")
	}

	header := components.HeaderBar(m.th, "formae status command", right, m.width)

	visible := m.height - chromeLines
	if visible < 0 {
		visible = 0
	}

	dataRows := m.multi.renderRows(visible)
	// Pad short row slices with blank lines so the footer is always anchored.
	for len(dataRows) < visible {
		dataRows = append(dataRows, "")
	}

	body := m.multi.headerRow() + "\n" + strings.Join(dataRows, "\n")
	footer := components.FooterBar(m.th, m.width, multiFooterHints(), "")

	return header + "\n" + body + "\n" + m.query.View(m.width) + "\n" + footer
}

// multiFooterHints returns the key hints shown in the footer bar.
func multiFooterHints() []components.KeyHint {
	return []components.KeyHint{
		{Key: "↑↓", Desc: "select"},
		{Key: "enter", Desc: "details"},
		{Key: "→←", Desc: "column"},
		{Key: "s", Desc: "toggle sort"},
		{Key: "q", Desc: "quit"},
	}
}

// reanchorCursor finds anchorID in rows and returns its index. If the ID is
// not found, it clamps oldIdx to [0, len(rows)-1].
func reanchorCursor(rows []row, anchorID string, oldIdx int) int {
	if anchorID != "" {
		for i, r := range rows {
			if r.cmd.CommandID == anchorID {
				return i
			}
		}
	}
	// Fall back: clamp old index.
	if len(rows) == 0 {
		return 0
	}
	if oldIdx >= len(rows) {
		return len(rows) - 1
	}
	if oldIdx < 0 {
		return 0
	}
	return oldIdx
}

// allTerminal reports whether every row is in a terminal command state.
func allTerminal(rows []row) bool {
	for _, r := range rows {
		if !isTerminalCommand(r.cmd.State) {
			return false
		}
	}
	return true
}

// sortHiIndex returns the index of col in cols, or 0 if not found.
func sortHiIndex(cols []int, col int) int {
	for i, c := range cols {
		if c == col {
			return i
		}
	}
	return 0
}
