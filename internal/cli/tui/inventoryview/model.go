// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package inventoryview

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
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
	th            *theme.Theme
	keys          tui.KeyMap
	client        Client
	opts          Options
	specs         [4]tabSpec
	tabs          [4]tabModel
	active        Tab
	width         int
	height        int
	spinner       spinner.Model
	nags          []string
	nagSeen       map[string]struct{}
	statsSent     bool
	ready         bool
	sortOpen      bool      // sort selector is visible (replaces footer)
	sortCursor    int       // index into specs[active].columns of selector cursor
	filterBar     filterBar // lightweight textinput for live client-side filter
	filterFocused bool      // true while the filter bar has keyboard focus
	// detail screen state
	detailOpen     bool
	detailTitle    string         // captured at open time
	detailBody     []string       // captured at open time (row.detail(width))
	detailViewport viewport.Model // scrollable content area for the detail screen
	// helpOpen tracks whether the help overlay is currently displayed.
	helpOpen bool
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
		th:        th,
		keys:      tui.DefaultKeyMap(),
		client:    client,
		opts:      opts,
		specs:     specs,
		tabs:      tabs,
		active:    opts.FocusTab,
		spinner:   components.NewSpinner(th),
		nagSeen:   make(map[string]struct{}),
		filterBar: newFilterBar(),
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
		// The active tab gets the reduced body budget when the filter bar is visible;
		// inactive tabs always get the full body budget (they don't show the bar).
		bodyH := m.bodyHeight()
		for i := range m.tabs {
			tabH := bodyH
			if Tab(i) == m.active && m.filterBarVisible() {
				tabH = bodyH - filterBarLines
			}
			if tabH < 1 {
				tabH = 1
			}
			m.tabs[i] = m.tabs[i].setSize(m.width, tabH)
			m.tabs[i] = m.tabs[i].sync(m.opts.MaxRows)
		}
		// Resize detail viewport if the detail screen is open.
		if m.detailOpen {
			vpH := m.height - 9
			if vpH < 1 {
				vpH = 1
			}
			m.detailViewport.Width = m.width
			m.detailViewport.Height = vpH
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
	// ctrl+c always quits, even over the help overlay.
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	// Help overlay: ? or esc closes; all other keys (except ctrl+c) are swallowed.
	if m.helpOpen {
		if msg.Type == tea.KeyEsc ||
			(msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '?') {
			m.helpOpen = false
		}
		return m, nil
	}

	// When detail screen is open, route to detail key handler.
	if m.detailOpen {
		return m.handleDetailKey(msg)
	}

	// When the sort selector is open, only a small set of keys are routed;
	// all others are silently swallowed.
	if m.sortOpen {
		return m.handleSortKey(msg)
	}

	// When the filter bar is focused, route all keys to it except ctrl+c.
	if m.filterFocused {
		return m.handleFilterKey(msg)
	}

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

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 's':
		if m.tabs[m.active].state == tabLoaded {
			return m.openSortSelector()
		}

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '/':
		if m.tabs[m.active].state == tabLoaded {
			return m.openFilterBar()
		}

	case msg.Type == tea.KeyEnter:
		if m.tabs[m.active].state == tabLoaded {
			return m.openDetail()
		}

	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
		key.Matches(msg, m.keys.PageUp), key.Matches(msg, m.keys.PageDown):
		return m.delegateNav(msg)

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '?':
		m.helpOpen = true
		return m, nil
	}

	return m, nil
}

// openDetail opens the full-screen detail view for the currently selected row.
func (m Model) openDetail() (tea.Model, tea.Cmd) {
	tab := m.tabs[m.active]
	vis, _ := tab.visible(m.opts.MaxRows)
	cursor := tab.table.Cursor()
	if len(vis) == 0 || cursor >= len(vis) {
		return m, nil
	}

	r := vis[cursor]
	m.detailTitle = r.title
	if r.detail != nil {
		m.detailBody = r.detail(m.width)
	} else {
		m.detailBody = nil
	}

	vpH := m.height - 9
	if vpH < 1 {
		vpH = 1
	}
	vp := viewport.New(m.width, vpH)
	// Build content: truncate each line to m.width.
	contentLines := make([]string, len(m.detailBody))
	for i, line := range m.detailBody {
		contentLines[i] = components.Truncate(line, m.width)
	}
	vp.SetContent(strings.Join(contentLines, "\n"))
	m.detailViewport = vp
	m.detailOpen = true
	return m, nil
}

// handleDetailKey handles key events while the detail screen is open.
func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		m.detailOpen = false
		return m, nil

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'q':
		// Swallowed — q does not quit from detail screen.
		return m, nil

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '?':
		m.helpOpen = true
		return m, nil

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'j',
		msg.Type == tea.KeyDown:
		m.detailViewport.ScrollDown(1)

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'k',
		msg.Type == tea.KeyUp:
		m.detailViewport.ScrollUp(1)

	case msg.Type == tea.KeyCtrlD:
		m.detailViewport.HalfPageDown()

	case msg.Type == tea.KeyCtrlU:
		m.detailViewport.HalfPageUp()
	}
	return m, nil
}

// openFilterBar focuses the filter bar on the active tab.
func (m Model) openFilterBar() (tea.Model, tea.Cmd) {
	m.filterFocused = true
	m.filterBar = m.filterBar.focus(m.tabs[m.active].filter)
	// Resize the active tab to the reduced body budget.
	m.tabs[m.active] = m.tabs[m.active].setSize(m.width, m.bodyHeight()-filterBarLines)
	m.tabs[m.active] = m.tabs[m.active].sync(m.opts.MaxRows)
	return m, nil
}

// handleFilterKey handles key events while the filter bar is focused.
// Only ctrl+c, enter, esc, and all textinput keys are processed here.
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEnter:
		// Unfocus but keep filter applied.
		m.filterFocused = false
		m.filterBar = m.filterBar.blur()
		// Size the active tab correctly: if the filter is non-empty the bar
		// remains visible (filterBarVisible depends on filter, not focus), so
		// we must use the reduced body budget; only an empty filter earns the
		// full budget.
		tabH := m.bodyHeight()
		if m.tabs[m.active].filter != "" {
			tabH -= filterBarLines
		}
		if tabH < 1 {
			tabH = 1
		}
		m.tabs[m.active] = m.tabs[m.active].setSize(m.width, tabH)
		m.tabs[m.active] = m.tabs[m.active].sync(m.opts.MaxRows)
		return m, nil

	case tea.KeyEsc:
		// Clear filter and unfocus.
		m.filterFocused = false
		m.filterBar = m.filterBar.blur()
		m.filterBar = m.filterBar.setValue("")
		m.tabs[m.active].filter = ""
		// Restore full body budget.
		m.tabs[m.active] = m.tabs[m.active].setSize(m.width, m.bodyHeight())
		m.tabs[m.active] = m.tabs[m.active].sync(m.opts.MaxRows)
		return m, nil

	default:
		// Route to textinput. The textinput.Update call returns a new model
		// and possibly a blink cmd. We update the filter value live.
		var cmd tea.Cmd
		newTI, tiCmd := m.filterBar.ti.Update(msg)
		m.filterBar.ti = newTI
		m.tabs[m.active].filter = m.filterBar.value()
		m.tabs[m.active] = m.tabs[m.active].sync(m.opts.MaxRows)
		cmd = tiCmd
		return m, cmd
	}
}

// openSortSelector opens the inline sort selector on the active tab.
// sortCursor starts on the currently-applied sortCol; if sortCol==-1, start on 0.
func (m Model) openSortSelector() (tea.Model, tea.Cmd) {
	m.sortOpen = true
	col := m.tabs[m.active].sortCol
	if col < 0 {
		col = 0
	}
	m.sortCursor = col
	return m, nil
}

// handleSortKey handles key events while the sort selector is open.
// Only ctrl+c, left/right/up/down (and h/l/j/k), enter and esc are routed;
// all other keys are silently swallowed.
func (m Model) handleSortKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	numCols := len(m.specs[m.active].columns)

	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit

	case msg.Type == tea.KeyLeft,
		msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'h'):
		m.sortCursor = (m.sortCursor - 1 + numCols) % numCols

	case msg.Type == tea.KeyRight,
		msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'l'):
		m.sortCursor = (m.sortCursor + 1) % numCols

	case msg.Type == tea.KeyUp,
		msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'k'):
		m.sortCursor = (m.sortCursor - 1 + numCols) % numCols

	case msg.Type == tea.KeyDown,
		msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'j'):
		m.sortCursor = (m.sortCursor + 1) % numCols

	case msg.Type == tea.KeyEnter:
		t := m.active
		if m.sortCursor == m.tabs[t].sortCol {
			// Toggle direction: Asc → Desc → Asc.
			if m.tabs[t].sortDir == components.SortAsc {
				m.tabs[t].sortDir = components.SortDesc
			} else {
				m.tabs[t].sortDir = components.SortAsc
			}
		} else {
			m.tabs[t].sortCol = m.sortCursor
			m.tabs[t].sortDir = components.SortAsc
		}
		m.tabs[t] = m.tabs[t].sync(m.opts.MaxRows)
		m.sortOpen = false

	case msg.Type == tea.KeyEsc:
		m.sortOpen = false

	default:
		// Swallow all other keys silently.
	}

	return m, nil
}

// switchTab switches to tab t, triggering a fetch if the tab is not yet loaded.
func (m Model) switchTab(t Tab) (tea.Model, tea.Cmd) {
	m.active = t
	// Re-size the newly-active tab for its correct body budget. Visibility of
	// the filter bar after the switch depends on the NEW active tab's filter
	// (focus is always false during a switch). We always re-size here so that
	// stale sizes from any previous active state are corrected in both
	// directions: filtered → full budget (bar gone) or unfiltered → full
	// budget, filtered → reduced budget (bar still visible).
	tabH := m.bodyHeight()
	if m.tabs[t].filter != "" {
		tabH -= filterBarLines
	}
	if tabH < 1 {
		tabH = 1
	}
	m.tabs[t] = m.tabs[t].setSize(m.width, tabH)
	m.tabs[t] = m.tabs[t].sync(m.opts.MaxRows)

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

// chromeLines is the number of lines consumed by fixed chrome at full width (≥80):
//
//	HeaderBar (2) + tab bar (1) + status line (1) + FooterBar (2) = 6.
const chromeLines = 6

// narrowChromeLines is the chrome line count at narrow width (< narrowFooterThreshold):
//
//	HeaderBar (2) + tab bar (1) + FooterBar (2) = 5.
//
// The standalone status line is dropped; its content is folded into the
// FooterBar hint line as a single combined dim string.
const narrowChromeLines = 5

// bodyHeight returns the number of lines available for the tab body region
// (excluding any filter bar lines — those are sliced from the body budget).
func (m Model) bodyHeight() int {
	budget := chromeLines
	if m.width < narrowFooterThreshold {
		budget = narrowChromeLines
	}
	h := m.height - budget
	if h < 1 {
		h = 1
	}
	return h
}

// filterBarVisible reports whether the filter bar should be rendered.
// The bar is visible when focused OR when the active tab has a non-empty filter.
func (m Model) filterBarVisible() bool {
	return m.filterFocused || m.tabs[m.active].filter != ""
}

// viewDetail renders the full-screen detail view.
//
// Layout (m.height lines total):
//
//	HeaderBar           (2 lines)
//	blank               (1 line)
//	"  ← esc"           (1 line)
//	blank               (1 line)
//	"  <title>"         (1 line)
//	rule                (1 line)
//	viewport            (vpHeight = m.height - 9 lines)
//	FooterBar           (2 lines)
func (m Model) viewDetail() string {
	p := m.th.Palette

	header := components.HeaderBar(m.th, "formae inventory", "", m.width)

	escLine := "  ← esc"
	escW := lipgloss.Width(escLine)
	if escW < m.width {
		escLine += strings.Repeat(" ", m.width-escW)
	}

	titleLine := "  " + m.detailTitle
	titleW := lipgloss.Width(titleLine)
	if titleW < m.width {
		titleLine += strings.Repeat(" ", m.width-titleW)
	}

	rule := "  " + lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", m.width-2))
	ruleW := lipgloss.Width(rule)
	if ruleW < m.width {
		rule += strings.Repeat(" ", m.width-ruleW)
	}

	footer := components.FooterBar(m.th, m.width, []components.KeyHint{{Key: "esc", Desc: "back to list"}}, "")

	vpH := m.height - 9
	if vpH < 1 {
		vpH = 1
	}
	// Ensure viewport dimensions are current.
	m.detailViewport.Width = m.width
	m.detailViewport.Height = vpH

	vpContent := m.detailViewport.View()

	parts := []string{
		header,
		"",
		escLine,
		"",
		titleLine,
		rule,
		vpContent,
		footer,
	}

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

// narrowFooterThreshold is the terminal width below which the footer hint line
// and status bar switch to abbreviated forms. Matches the PLA-282 statuswatch
// convention (no analogous constant exists in statuswatch — it uses full hints
// at all widths; the threshold here is spec-defined at 80).
const narrowFooterThreshold = 80

// View composes the full terminal screen. Returns "" until the first
// WindowSizeMsg is received (statuswatch convention).
func (m Model) View() string {
	if !m.ready {
		return ""
	}

	// Help overlay: render over whatever the current screen is.
	if m.helpOpen {
		return m.viewHelp()
	}

	if m.detailOpen {
		return m.viewDetail()
	}

	header := components.HeaderBar(m.th, "formae inventory", "", m.width)
	tabBar := m.renderTabBar()

	narrow := m.width < narrowFooterThreshold
	var footer string
	if m.sortOpen {
		footer = m.renderSortSelector()
	} else if m.filterFocused {
		footer = m.renderFilterFooter()
	} else if narrow {
		// Narrow: combined dim line — "Showing X of Y · ↑↓ enter / s q".
		// No "?: help" at narrow widths (mockup View 9 shows none).
		combined := m.tabs[m.active].statusLineNarrow(m.opts.MaxRows)
		footer = components.FooterBarNarrow(m.th, m.width, combined)
	} else {
		footer = components.FooterBar(m.th, m.width, inventoryFooterHints(), "")
	}

	// Body region: bodyH total lines = filter bar lines (if visible) + table lines.
	bodyH := m.bodyHeight()

	// Collect filter bar lines (2) when visible, then table lines filling the remainder.
	var bodyLines []string
	if m.filterBarVisible() {
		barLines := m.filterBar.renderLines(m.th, m.width)
		bodyLines = append(bodyLines, barLines...)
		// Table gets bodyH - filterBarLines.
		tableH := bodyH - filterBarLines
		if tableH < 1 {
			tableH = 1
		}
		tableLines := m.tabs[m.active].view(m.th, m.opts.MaxRows, m.spinner.View())
		// Pad/trim table to exactly tableH.
		for len(tableLines) < tableH {
			tableLines = append(tableLines, "")
		}
		if len(tableLines) > tableH {
			tableLines = tableLines[:tableH]
		}
		bodyLines = append(bodyLines, tableLines...)
	} else {
		// No filter bar: table fills all of bodyH.
		bodyLines = m.tabs[m.active].view(m.th, m.opts.MaxRows, m.spinner.View())
	}

	// Pad body to exactly bodyH lines (defensive).
	for len(bodyLines) < bodyH {
		bodyLines = append(bodyLines, "")
	}
	if len(bodyLines) > bodyH {
		bodyLines = bodyLines[:bodyH]
	}

	// Status line: omitted at narrow widths (content folded into footer).
	var parts []string
	if narrow {
		// Assemble: header (2 lines) + tab bar (1 line) + body (bodyH lines) + footer (2 lines)
		// = narrowChromeLines + bodyH = m.height total lines.
		parts = []string{header, tabBar}
		parts = append(parts, bodyLines...)
		parts = append(parts, footer)
	} else {
		statusLine := m.tabs[m.active].statusLine(m.opts.MaxRows)
		statusLine = components.Truncate(statusLine, m.width)
		// Assemble: header (2 lines) + tab bar (1 line) + body (bodyH lines) + status (1 line) + footer (2 lines)
		// = chromeLines + bodyH = m.height total lines.
		parts = []string{header, tabBar}
		parts = append(parts, bodyLines...)
		parts = append(parts, statusLine, footer)
	}

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

// viewHelp renders the help overlay centered on the screen, over the header
// and footer (statuswatch pattern: panel borders, key/desc columns, theme roles).
func (m Model) viewHelp() string {
	header := components.HeaderBar(m.th, "formae inventory", "", m.width)
	footer := components.FooterBar(m.th, m.width, inventoryFooterHints(), "")

	// Body area: total height − header (2) − footer (2).
	bodyH := m.height - 4
	if bodyH < 1 {
		bodyH = 1
	}

	helpPanel := components.HelpOverlay(m.th, m.width, bodyH, inventoryHelpGroups())

	parts := header + "\n" + helpPanel + "\n" + footer
	lines := strings.Split(parts, "\n")
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// renderFilterFooter renders the 2-line footer used while the filter bar is focused.
// Line 1 (hint): "  enter: confirm search  esc: clear search" + right-aligned "?: help"
// Line 2 (separator): same border line as FooterBar.
func (m Model) renderFilterFooter() string {
	hints := []components.KeyHint{
		{Key: "enter", Desc: "confirm search"},
		{Key: "esc", Desc: "clear search"},
	}
	return components.FooterBar(m.th, m.width, hints, "")
}

// renderSortSelector renders the 2-line sort selector that replaces the footer
// when sortOpen is true.
//
// Line 1 (selector): "  Sort by:  ●NativeID  ▸Stack  ▸Type  ▸Label"
//   - ● marks the column the sortCursor is on (accent-styled)
//   - ▸ marks all other columns (plain)
//   - The currently-applied sort column shows ▲ or ▼ suffix after its title
//
// Line 2 (hint): "  ↑↓/j/k or ←/→: select  enter: confirm  esc: cancel"
//
// Both lines together are joined with "\n" to produce a 2-line replacement for
// the FooterBar (which also renders as 2 lines: border + content).
func (m Model) renderSortSelector() string {
	cols := m.specs[m.active].columns
	applied := m.tabs[m.active].sortCol
	dir := m.tabs[m.active].sortDir

	var sb strings.Builder
	sb.WriteString("  Sort by:  ")

	for i, col := range cols {
		if i > 0 {
			sb.WriteString("  ")
		}
		// Build entry text: marker + title + optional direction suffix.
		title := col.Title
		if i == applied {
			if dir == components.SortAsc {
				title += " ▲"
			} else if dir == components.SortDesc {
				title += " ▼"
			}
		}

		if i == m.sortCursor {
			entry := "●" + title
			if m.th != nil {
				sb.WriteString(m.th.Styles.Accent.Render(entry))
			} else {
				sb.WriteString(entry)
			}
		} else {
			sb.WriteString("▸" + title)
		}
	}

	selectorLine := sb.String()
	// Pad to m.width.
	selectorVisW := lipgloss.Width(selectorLine)
	if selectorVisW < m.width {
		selectorLine += strings.Repeat(" ", m.width-selectorVisW)
	}

	hintLine := "  ↑↓/j/k or ←/→: select  enter: confirm  esc: cancel"
	hintVisW := lipgloss.Width(hintLine)
	if hintVisW < m.width {
		hintLine += strings.Repeat(" ", m.width-hintVisW)
	}

	return selectorLine + "\n" + hintLine
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

	// Pad or truncate the tab bar line to full width.
	line := sb.String()
	lineW := lipgloss.Width(line)
	if lineW < m.width {
		line += strings.Repeat(" ", m.width-lineW)
	} else if lineW > m.width {
		line = components.Truncate(line, m.width)
	}
	return line
}

// inventoryHelpGroups returns the grouped keybinding hints for the help overlay.
func inventoryHelpGroups() []components.HelpGroup {
	return []components.HelpGroup{
		{
			Title: "Navigate",
			Hints: []components.KeyHint{
				{Key: "↑↓ / j k", Desc: "navigate"},
				{Key: "ctrl-d / ctrl-u", Desc: "scroll detail"},
				{Key: "1-4 / tab", Desc: "switch tab"},
			},
		},
		{
			Title: "Actions",
			Hints: []components.KeyHint{
				{Key: "enter", Desc: "detail"},
				{Key: "/", Desc: "search"},
				{Key: "s", Desc: "sort"},
				{Key: "r", Desc: "refresh"},
			},
		},
		{
			Title: "General",
			Hints: []components.KeyHint{
				{Key: "esc", Desc: "back"},
				{Key: "q", Desc: "quit"},
				{Key: "?", Desc: "close help"},
			},
		},
	}
}

// inventoryFooterHints returns the key hints shown in the inventory footer (full width ≥80).
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
