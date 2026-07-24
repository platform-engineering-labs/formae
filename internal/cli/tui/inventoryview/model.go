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
	"github.com/charmbracelet/x/ansi"

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
	watcher   *theme.OmarchyWatcher
	nags      []string
	nagSeen   map[string]struct{}
	statsSent bool
	ready     bool
	// query is the shared editable query bar (bottom, always visible) that
	// drives the active tab's client-side filter — mirrors the status-command
	// TUI so the two views offer an identical "edit the query" interaction.
	query components.QueryBar
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
	// opts.Query is the initial query for the focus tab (D3). Seed both the tab's
	// applied query and the shared query bar with it so it is threaded to the
	// fetch and shown in the bar, mirroring the status-command TUI.
	tabs[opts.FocusTab].query = opts.Query
	model := Model{
		th:      th,
		keys:    tui.DefaultKeyMap(),
		client:  client,
		opts:    opts,
		specs:   specs,
		tabs:    tabs,
		active:  opts.FocusTab,
		spinner: components.NewSpinner(th),
		nagSeen: make(map[string]struct{}),
		query:   components.NewQueryBar(th, opts.Query),
	}
	if th.Name == "omarchy" {
		if w, err := theme.NewOmarchyWatcher(); err == nil {
			model.watcher = w
		}
	}
	return model
}

// ApplyTheme swaps the model (and its per-tab caches) onto a new theme: it
// threads the theme into every tab, rebuilds each tab's themed table styling
// (components.Table bakes header/cell/selected-row styles in at construction
// time — see Table.SetTheme) and cached per-cell styled replacements
// (tabModel.styledCells, rebuilt via sync — see tab.go), re-themes the query
// bar in place (WithTheme preserves any in-progress edit/focus state), and
// rebuilds the stateful spinner.
func (m Model) ApplyTheme(t *theme.Theme) Model {
	m.th = t
	for i := range m.tabs {
		m.tabs[i].th = t
		m.tabs[i].table = m.tabs[i].table.SetTheme(t)
		m.tabs[i] = m.tabs[i].sync(m.opts.MaxRows)
	}
	m.query = m.query.WithTheme(t)
	m.spinner = components.NewSpinner(t)
	return m
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
	// The focus tab's query was seeded from opts.Query in New (D3).
	cmds := []tea.Cmd{
		m.spinner.Tick,
		fetchCmd(m.client, m.specs, m.active, m.tabs[m.active].query, false),
	}
	if m.watcher != nil {
		cmds = append(cmds, m.watcher.WaitCmd())
	}
	return tea.Batch(cmds...)
}

// Update handles all incoming messages and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// setSize + sync ALL tabs so they render correctly when switched to.
		// The query bar is fixed chrome (always visible), so every tab gets the
		// same body budget.
		bodyH := m.bodyHeight()
		for i := range m.tabs {
			m.tabs[i] = m.tabs[i].setSize(m.width, bodyH)
			m.tabs[i] = m.tabs[i].sync(m.opts.MaxRows)
		}
		// Resize detail viewport if the detail screen is open.
		if m.detailOpen {
			vpH := m.height - detailChromeLines
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

	case theme.ApplyThemeMsg:
		m = m.ApplyTheme(msg.Theme)
		cmds := []tea.Cmd{m.spinner.Tick} // restart the tick at the new interval
		if m.watcher != nil {
			cmds = append(cmds, m.watcher.WaitCmd()) // re-arm the watch
		}
		return m, tea.Batch(cmds...)

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

	// When the query bar is focused, route all keys (except ctrl+c) to it.
	if m.query.Focused() {
		return m.handleQueryKey(msg)
	}

	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit

	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	// The inventory list is the top level, so esc quits (in the detail screen
	// esc goes back to the list). Consistent with the other TUIs.
	case msg.Type == tea.KeyEsc:
		return m, tea.Quit

	case msg.Type == tea.KeyTab:
		return m.switchTab(Tab((int(m.active) + 1) % 4))

	case msg.Type == tea.KeyShiftTab:
		return m.switchTab(Tab((int(m.active) + 3) % 4)) // +3 mod 4 = -1 mod 4

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '4':
		return m.switchTab(Tab(msg.Runes[0] - '1'))

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'r':
		return m.refreshActive()

	// →← move the sort-column highlight; s toggles sort on the highlighted
	// column — mirrors the status-command TUI (no modal selector).
	case key.Matches(msg, m.keys.Left):
		return m.moveSortHi(-1)

	case key.Matches(msg, m.keys.Right):
		return m.moveSortHi(1)

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 's':
		if m.tabs[m.active].state == tabLoaded {
			return m.toggleSort()
		}

	case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '/':
		m.query = m.query.Focus()
		return m, nil

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

// detailIndent is the left indent (spaces) applied to the detail-screen content
// so it aligns with the indented header, title and rule above it.
const detailIndent = 2

// colorizeDetailLine adds structured color to a plain detail-body line:
//   - a top-level "Section:" header (no indent, ends with ":") → accent
//   - a "Key: value" line → dim key (through the colon), bright value
//   - anything else → unchanged
//
// It operates on the already-truncated plain line so widths stay correct.
func colorizeDetailLine(th *theme.Theme, line string) string {
	if th == nil || strings.TrimSpace(line) == "" {
		return line
	}
	p := th.Palette
	rtrim := strings.TrimRight(line, " ")
	indented := strings.HasPrefix(line, " ")
	if !indented && strings.HasSuffix(rtrim, ":") {
		return lipgloss.NewStyle().Foreground(p.PrimaryAccent).Render(line)
	}
	if idx := strings.Index(line, ":"); idx >= 0 {
		key := line[:idx+1]
		val := line[idx+1:]
		return lipgloss.NewStyle().Foreground(p.TextSecondary).Render(key) +
			lipgloss.NewStyle().Foreground(p.TextPrimary).Render(val)
	}
	return line
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
	// Content is indented 2 spaces to align with the header/title/rule above it,
	// so build it for the reduced width.
	contentWidth := m.width - detailIndent
	if contentWidth < 1 {
		contentWidth = 1
	}
	if r.detail != nil {
		m.detailBody = r.detail(contentWidth)
	} else {
		m.detailBody = nil
	}

	vpH := m.height - detailChromeLines
	if vpH < 1 {
		vpH = 1
	}
	vp := viewport.New(m.width, vpH)
	// Build content: indent 2 spaces, truncate to the content width, then colorize.
	indent := strings.Repeat(" ", detailIndent)
	contentLines := make([]string, len(m.detailBody))
	for i, line := range m.detailBody {
		contentLines[i] = indent + colorizeDetailLine(m.th, components.Truncate(line, contentWidth))
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
		// q quits from the detail screen too (esc goes back to the list).
		return m, tea.Quit

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

// handleQueryKey handles key events while the query bar is focused. On enter
// the query is applied to the active tab; esc cancels the edit and keeps the
// previously-applied query. For serverQuery tabs (Resources, Targets) enter
// re-fetches from the server with the query; for the others it re-syncs the
// client-side substring filter. Mirrors the status-command TUI.
func (m Model) handleQueryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	var applied bool
	m.query, applied = m.query.Update(msg)
	if !applied {
		return m, nil
	}
	m.tabs[m.active].query = m.query.Query()
	if m.specs[m.active].serverQuery {
		// Re-fetch from the server with the new query.
		m.tabs[m.active].state = tabLoading
		m.statsSent = true
		return m, fetchCmd(m.client, m.specs, m.active, m.tabs[m.active].query, true)
	}
	m.tabs[m.active] = m.tabs[m.active].sync(m.opts.MaxRows)
	return m, nil
}

// moveSortHi moves the sort-column highlight on the active tab by delta,
// wrapping around the column set. It does not re-sort — s (toggleSort) commits.
func (m Model) moveSortHi(delta int) (tea.Model, tea.Cmd) {
	n := len(m.specs[m.active].columns)
	if n == 0 {
		return m, nil
	}
	m.tabs[m.active].sortHi = (m.tabs[m.active].sortHi + delta%n + n) % n
	return m, nil
}

// toggleSort sorts the active tab by the highlighted column. Pressing s on the
// already-active column flips its direction; on a new column it sorts ascending.
func (m Model) toggleSort() (tea.Model, tea.Cmd) {
	t := m.active
	hi := m.tabs[t].sortHi
	if hi == m.tabs[t].sortCol {
		if m.tabs[t].sortDir == components.SortAsc {
			m.tabs[t].sortDir = components.SortDesc
		} else {
			m.tabs[t].sortDir = components.SortAsc
		}
	} else {
		m.tabs[t].sortCol = hi
		m.tabs[t].sortDir = components.SortAsc
	}
	m.tabs[t] = m.tabs[t].sync(m.opts.MaxRows)
	return m, nil
}

// switchTab switches to tab t, triggering a fetch if the tab is not yet loaded.
func (m Model) switchTab(t Tab) (tea.Model, tea.Cmd) {
	m.active = t
	// Reseed the shared query bar with the new tab's applied filter so the bar
	// always reflects the active tab's query.
	m.query = components.NewQueryBar(m.th, m.tabs[t].query)
	// Re-size the newly-active tab for the body budget (the query bar is fixed
	// chrome, so the budget is the same for all tabs).
	m.tabs[t] = m.tabs[t].setSize(m.width, m.bodyHeight())
	m.tabs[t] = m.tabs[t].sync(m.opts.MaxRows)

	tab := &m.tabs[t]
	if tab.state == tabNotLoaded || tab.state == tabFailed {
		tab.state = tabLoading
		// Fetch with the tab's applied query (empty for a fresh tab). Init fires
		// the session's first fetch (fromTUI=false); Update fetches use true (R7).
		cmd := fetchCmd(m.client, m.specs, t, tab.query, true)
		m.statsSent = true
		return m, cmd
	}
	return m, nil
}

// refreshActive refetches the active tab, preserving filter/sortCol/sortDir.
func (m Model) refreshActive() (tea.Model, tea.Cmd) {
	t := m.active
	// Pipeline state (query/sortCol/sortDir) is preserved on the tabModel across
	// the reload; the tabLoadedMsg only replaces allRows.
	m.tabs[t].state = tabLoading
	// Re-fetch with the tab's applied query so a server-side query survives a
	// manual refresh (it is ignored by non-serverQuery tabs' fetch).
	cmd := fetchCmd(m.client, m.specs, t, m.tabs[t].query, true)
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
//	header (2) + tab bar (3) + blank (1) + status line (1) + query bar (2) + FooterBar (2) = 11.
const chromeLines = 11

// narrowChromeLines is the chrome line count at narrow width (< narrowFooterThreshold):
//
//	header (2) + tab bar (3) + blank (1) + query bar (2) + FooterBar (2) = 10.
//
// The standalone status line is dropped; its content is folded into the
// FooterBar hint line as a single combined dim string.
const narrowChromeLines = 10

// bodyHeight returns the number of lines available for the tab body region
// (the table). The query bar is fixed chrome, not part of the body budget.
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

// detailChromeLines is the number of non-viewport lines on the detail screen:
//
//	HeaderBar (2) + "  <title>" (1) + rule (1) + FooterBar (2) = 6.
const detailChromeLines = 6

// viewDetail renders the full-screen detail view: the branded header, the accent
// title, a rule, then the (colored) content. Back navigation (esc) lives in the
// footer, so there is no separate esc row.
func (m Model) viewDetail() string {
	p := m.th.Palette

	header := components.HeaderBarBranded(m.th, "inventory", "", m.width)

	pad := func(s string) string {
		if w := lipgloss.Width(s); w < m.width {
			return s + strings.Repeat(" ", m.width-w)
		}
		return s
	}
	titleLine := pad("  " + lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true).Render(m.detailTitle))
	rule := pad("  " + lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", m.width-2)))

	footer := components.FooterBar(m.th, m.width, []components.KeyHint{
		{Key: "esc", Desc: "back to list"},
		{Key: "q", Desc: "quit"},
	}, "")

	vpH := m.height - detailChromeLines
	if vpH < 1 {
		vpH = 1
	}
	// Ensure viewport dimensions are current.
	m.detailViewport.Width = m.width
	m.detailViewport.Height = vpH

	vpContent := m.detailViewport.View()

	parts := []string{
		header,
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

	header := components.HeaderBarBranded(m.th, "inventory", "", m.width)
	tabBar := m.renderTabBar()

	narrow := m.width < narrowFooterThreshold
	var footer string
	if narrow {
		// Narrow: combined dim line — "Showing X of Y · ↑↓ enter / s q".
		// No "?: help" at narrow widths (mockup View 9 shows none).
		combined := m.tabs[m.active].statusLineNarrow(m.opts.MaxRows)
		footer = components.FooterBarNarrow(m.th, m.width, combined)
	} else {
		footer = components.FooterBar(m.th, m.width, inventoryFooterHints(), "")
	}

	// The query bar is fixed chrome: always visible just above the footer,
	// mirroring the status-command TUI.
	queryBar := m.query.View(m.width)

	// Body region: the table fills the whole body budget.
	bodyH := m.bodyHeight()
	bodyLines := m.tabs[m.active].view(m.th, m.opts.MaxRows, m.spinner.View())
	for len(bodyLines) < bodyH {
		bodyLines = append(bodyLines, "")
	}
	if len(bodyLines) > bodyH {
		bodyLines = bodyLines[:bodyH]
	}

	// Assemble bottom chrome. Wide: status line + query bar + footer. Narrow:
	// query bar + footer (the count is folded into the narrow footer).
	var parts []string
	// Blank line between the tab bar and the table for breathing room.
	parts = append(parts, header, tabBar, "")
	parts = append(parts, bodyLines...)
	if !narrow {
		statusLine := components.Truncate(m.tabs[m.active].statusLine(m.opts.MaxRows), m.width)
		parts = append(parts, statusLine)
	}
	parts = append(parts, queryBar, footer)

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
	header := components.HeaderBarBranded(m.th, "inventory", "", m.width)
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

// renderTabBar renders the tab-selection bar as numbered tabs, e.g.
// " 1 Resources   2 Targets   3 Stacks   4 Policies". The active tab is drawn as
// an open-bottom box (top + side borders in the accent color); the third row is
// an accent baseline that spans the full width with a gap under the active box,
// so the line continues to the right edge beneath the inactive tabs. The leading
// numbers double as the 1-4 switch hints. Returns exactly three lines.
func (m Model) renderTabBar() string {
	titles := [4]string{"Resources", "Targets", "Stacks", "Policies"}

	borderSt := lipgloss.NewStyle()
	activeSt := lipgloss.NewStyle()
	dimSt := lipgloss.NewStyle()
	if m.th != nil {
		borderSt = lipgloss.NewStyle().Foreground(m.th.Palette.PrimaryAccent)
		activeSt = lipgloss.NewStyle().Foreground(m.th.Palette.PrimaryAccent).Bold(true)
		dimSt = lipgloss.NewStyle().Foreground(m.th.Palette.TextSecondary)
	}

	var top, mid strings.Builder
	col := 0
	aStart, aWidth := -1, 0 // active box span (incl. side borders), in visual columns

	top.WriteString(" ")
	mid.WriteString(" ")
	col++
	for i, title := range titles {
		if i > 0 {
			top.WriteString("  ")
			mid.WriteString("  ")
			col += 2
		}
		label := " " + string(rune('1'+i)) + " " + title + " "
		lw := len([]rune(label))
		if Tab(i) == m.active {
			aStart, aWidth = col, lw+2
			top.WriteString(borderSt.Render("╭" + strings.Repeat("─", lw) + "╮"))
			mid.WriteString(borderSt.Render("│") + activeSt.Render(label) + borderSt.Render("│"))
			col += aWidth
		} else {
			top.WriteString(strings.Repeat(" ", lw))
			mid.WriteString(dimSt.Render(label))
			col += lw
		}
	}

	// Baseline: accent rule across the full width. Under the active box it curves
	// up into the box's side borders (╯ … ╰) with an open gap between, so the tab
	// sits on the line and the rule continues to the right edge under the others.
	leftCol, rightCol := aStart, aStart+aWidth-1
	var base strings.Builder
	for c := 0; c < m.width; c++ {
		switch {
		case aStart < 0:
			base.WriteString("─")
		case c == leftCol:
			base.WriteString("╯")
		case c == rightCol:
			base.WriteString("╰")
		case c > leftCol && c < rightCol:
			base.WriteString(" ")
		default:
			base.WriteString("─")
		}
	}

	pad := func(s string) string {
		if w := lipgloss.Width(s); w < m.width {
			return s + strings.Repeat(" ", m.width-w)
		} else if w > m.width {
			return ansi.Truncate(s, m.width, "")
		}
		return s
	}
	return pad(top.String()) + "\n" + pad(mid.String()) + "\n" + borderSt.Render(base.String())
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
