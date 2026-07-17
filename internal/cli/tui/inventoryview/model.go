// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tui "github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// Model is the root bubbletea model for the inventory TUI.
// It wires together the four tab models (Resources, Targets, Stacks, Policies)
// with lazy per-tab fetching, a shared spinner, and exact-fill rendering.
type Model struct {
	th        *theme.Theme
	keys      tui.KeyMap
	client    Client
	opts      Options
	specs     [4]tabSpec
	tabs      [4]tabModel
	active    Tab
	width     int
	height    int
	spinner   spinner.Model
	nags      []string
	nagSeen   map[string]struct{}
	statsSent bool
	ready     bool
}

// New constructs a Model with the four tab specs and sane defaults.
func New(th *theme.Theme, client Client, opts Options) Model {
	specs := newSpecs(opts.Now)
	tabs := [4]tabModel{
		TabResources: newTabModel(th, specs[TabResources]),
		TabTargets:   newTabModel(th, specs[TabTargets]),
		TabStacks:    newTabModel(th, specs[TabStacks]),
		TabPolicies:  newTabModel(th, specs[TabPolicies]),
	}
	return Model{
		th:      th,
		keys:    tui.DefaultKeyMap(),
		client:  client,
		opts:    opts,
		specs:   specs,
		tabs:    tabs,
		active:  opts.FocusTab,
		spinner: components.NewSpinner(th),
		nagSeen: make(map[string]struct{}),
	}
}

// Nags returns the deduped, insertion-ordered nag messages collected across all
// tab fetches. Intended for post-exit stderr printing (design D9).
func (m Model) Nags() []string {
	if len(m.nags) == 0 {
		return nil
	}
	out := make([]string, len(m.nags))
	copy(out, m.nags)
	return out
}

// Init kicks off the spinner tick and the first fetch (for opts.FocusTab).
// The first fetch passes fromTUI=false to transmit usage stats once (R7).
// The active tab's state is already tabNotLoaded at construction; the
// tabLoading transition happens in Update when the first fetch fires.
func (m Model) Init() tea.Cmd {
	query := m.opts.Query // only for FocusTab (D3)
	return tea.Batch(
		m.spinner.Tick,
		fetchCmd(m.client, m.specs, m.active, query, false),
	)
}

// Update handles all incoming messages and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// setSize + sync ALL tabs so they render correctly when switched to.
		bodyH := m.bodyHeight()
		for i := range m.tabs {
			m.tabs[i] = m.tabs[i].setSize(m.width, bodyH)
			m.tabs[i] = m.tabs[i].sync(m.opts.MaxRows)
		}
		m.ready = true
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tabLoadedMsg:
		return m.handleTabLoaded(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleTabLoaded processes a tabLoadedMsg — routes to the right tab even when
// it is not the active one (no dropped responses).
func (m Model) handleTabLoaded(msg tabLoadedMsg) (tea.Model, tea.Cmd) {
	tab := msg.tab

	// Mark stats as sent once the first fetch response lands.
	// Init fires the focus-tab fetch with fromTUI=false; all subsequent fetches
	// (triggered via key presses or refresh) should use fromTUI=true.
	if !m.statsSent {
		m.statsSent = true
	}

	if msg.err != nil {
		m.tabs[tab].state = tabFailed
		m.tabs[tab].err = msg.err
	} else {
		m.tabs[tab].state = tabLoaded
		m.tabs[tab].allRows = msg.rows
		m.tabs[tab] = m.tabs[tab].sync(m.opts.MaxRows)
	}

	// Collect nags with deduplication.
	for _, nag := range msg.nags {
		if _, seen := m.nagSeen[nag]; !seen {
			m.nagSeen[nag] = struct{}{}
			m.nags = append(m.nags, nag)
		}
	}

	return m, nil
}

// handleKey routes keyboard input.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit

	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case msg.Type == tea.KeyTab:
		return m.switchTab(Tab((int(m.active) + 1) % 4))

	case msg.Type == tea.KeyShiftTab:
		return m.switchTab(Tab((int(m.active) + 3) % 4)) // +3 mod 4 = -1 mod 4

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '4':
		return m.switchTab(Tab(msg.Runes[0] - '1'))

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'r':
		return m.refreshActive()

	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
		key.Matches(msg, m.keys.PageUp), key.Matches(msg, m.keys.PageDown):
		return m.delegateNav(msg)
	}

	return m, nil
}

// switchTab switches to tab t, triggering a fetch if the tab is not yet loaded.
func (m Model) switchTab(t Tab) (tea.Model, tea.Cmd) {
	m.active = t
	tab := &m.tabs[t]
	if tab.state == tabNotLoaded || tab.state == tabFailed {
		tab.state = tabLoading
		query := "" // non-focus tabs always use empty query (D3)
		// Init always fires the session's first fetch (fromTUI=false), so any
		// fetch issued from Update passes fromTUI=true (R7).
		cmd := fetchCmd(m.client, m.specs, t, query, true)
		m.statsSent = true
		return m, cmd
	}
	return m, nil
}

// refreshActive refetches the active tab, preserving filter/sortCol/sortDir.
func (m Model) refreshActive() (tea.Model, tea.Cmd) {
	t := m.active
	// Preserve pipeline state.
	filter := m.tabs[t].filter
	sortCol := m.tabs[t].sortCol
	sortDir := m.tabs[t].sortDir

	m.tabs[t].state = tabLoading
	m.tabs[t].filter = filter
	m.tabs[t].sortCol = sortCol
	m.tabs[t].sortDir = sortDir

	query := ""
	if t == m.opts.FocusTab {
		query = m.opts.Query
	}
	// Refresh is never the session's first fetch — always fromTUI=true (R7).
	cmd := fetchCmd(m.client, m.specs, t, query, true)
	m.statsSent = true
	return m, cmd
}

// delegateNav passes navigation keys to the active tab's table and re-syncs.
func (m Model) delegateNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := m.active
	newTable, cmd := m.tabs[t].table.Update(msg)
	m.tabs[t].table = newTable
	m.tabs[t] = m.tabs[t].sync(m.opts.MaxRows)
	return m, cmd
}

// chromeLines is the number of lines consumed by fixed chrome:
//
//	HeaderBar (2) + tab bar (1) + status line (1) + FooterBar (2) = 6.
const chromeLines = 6

// bodyHeight returns the number of lines available for the tab body region.
func (m Model) bodyHeight() int {
	h := m.height - chromeLines
	if h < 1 {
		h = 1
	}
	return h
}

// View composes the full terminal screen. Returns "" until the first
// WindowSizeMsg is received (statuswatch convention).
func (m Model) View() string {
	if !m.ready {
		return ""
	}

	header := components.HeaderBar(m.th, "formae inventory", "", m.width)
	tabBar := m.renderTabBar()
	footer := components.FooterBar(m.th, m.width, inventoryFooterHints(), "")

	// Active tab body: view() returns exactly bodyHeight() lines.
	bodyH := m.bodyHeight()
	bodyLines := m.tabs[m.active].view(m.th, m.opts.MaxRows, m.spinner.View())

	// Pad body to exactly bodyH lines (view() should already guarantee this,
	// but we defend here for safety).
	for len(bodyLines) < bodyH {
		bodyLines = append(bodyLines, "")
	}
	if len(bodyLines) > bodyH {
		bodyLines = bodyLines[:bodyH]
	}

	statusLine := m.tabs[m.active].statusLine(m.opts.MaxRows)

	// Assemble: header (2 lines) + tab bar (1 line) + body (bodyH lines) + status (1 line) + footer (2 lines)
	// = chromeLines + bodyH = m.height total lines.
	parts := []string{header, tabBar}
	parts = append(parts, bodyLines...)
	parts = append(parts, statusLine, footer)

	result := strings.Join(parts, "\n")

	// Clamp to exactly m.height lines (defensive).
	lines := strings.Split(result, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// renderTabBar renders the tab-selection bar.
// Format: "  [Resources]  Targets  Stacks  Policies"
// Active tab is bracketed and accent-styled; inactive tabs are plain.
func (m Model) renderTabBar() string {
	titles := [4]string{"Resources", "Targets", "Stacks", "Policies"}

	var sb strings.Builder
	sb.WriteString("  ")
	for i, title := range titles {
		if i > 0 {
			sb.WriteString("  ")
		}
		if Tab(i) == m.active {
			bracketed := "[" + title + "]"
			if m.th != nil {
				sb.WriteString(m.th.Styles.Accent.Render(bracketed))
			} else {
				sb.WriteString(bracketed)
			}
		} else {
			// Pad to same visual width as bracketed version (title + 2 for brackets).
			padded := " " + title + " "
			sb.WriteString(padded)
		}
	}

	// Pad the tab bar line to full width.
	line := sb.String()
	lineW := lipgloss.Width(line)
	if lineW < m.width {
		line += strings.Repeat(" ", m.width-lineW)
	}
	return line
}

// inventoryFooterHints returns the key hints shown in the inventory footer.
func inventoryFooterHints() []components.KeyHint {
	return []components.KeyHint{
		{Key: "↑↓/j/k", Desc: "navigate"},
		{Key: "enter", Desc: "detail"},
		{Key: "/", Desc: "search"},
		{Key: "s", Desc: "sort"},
		{Key: "r", Desc: "refresh"},
		{Key: "1-4", Desc: "tab"},
		{Key: "q", Desc: "quit"},
	}
}
