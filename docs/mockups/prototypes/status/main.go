// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Prototype: Status/Watch TUI
// Run: go run ./docs/mockups/prototypes/status/
//
// Multi-command summary + single command drill-in views with hardcoded data.
// enter: drill in, esc: back, d: toggle detail/summary, s: cycle sort,
// ↑↓/j/k: scroll, q: quit
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// -- Data types --

type updateState string

const (
	stateDone       updateState = "Done"
	stateInProgress updateState = "InProgress"
	statePending    updateState = "Pending"
	stateFailed     updateState = "Failed"
	stateSkipped    updateState = "Skipped"
)

type updateKind string

const (
	kindTarget   updateKind = "target"
	kindStack    updateKind = "stack"
	kindPolicy   updateKind = "policy"
	kindResource updateKind = "resource"
)

type fakeUpdate struct {
	Kind         updateKind
	Operation    string
	Label        string
	TypeName     string
	Stack        string
	State        updateState
	Duration     time.Duration
	ErrorMessage string
	Attempt      int
	MaxAttempts  int
	StatusMsg    string
	CascadeSrc   string
}

type fakeCommand struct {
	ID       string
	CmdType  string
	Mode     string
	State    string
	Duration time.Duration
	Updates  []fakeUpdate
}

func isTerminalState(s updateState) bool {
	return s == stateDone || s == stateFailed || s == stateSkipped
}

// -- Sort --

type sortColumn int

const (
	sortByStatus sortColumn = iota
	sortByTime
	sortByProgress
	sortByID
	sortColumnCount // sentinel for cycling
)

func sortCommands(cmds []fakeCommand, col sortColumn) {
	sort.SliceStable(cmds, func(i, j int) bool {
		switch col {
		case sortByStatus:
			return statusPriority(cmds[i]) < statusPriority(cmds[j])
		case sortByTime:
			return cmds[i].Duration > cmds[j].Duration
		case sortByProgress:
			pi := progressPct(cmds[i])
			pj := progressPct(cmds[j])
			return pi < pj
		case sortByID:
			return cmds[i].ID < cmds[j].ID
		}
		return false
	})
}

func statusPriority(cmd fakeCommand) int {
	switch cmd.State {
	case "Failed":
		return 0
	case "InProgress":
		return 1
	case "Done":
		return 2
	}
	return 3
}

func progressPct(cmd fakeCommand) float64 {
	completed, total := countCompleted(cmd.Updates)
	if total == 0 {
		return 0
	}
	return float64(completed) / float64(total)
}

// -- Model --

type viewMode int

const (
	viewMultiCommand viewMode = iota
	viewSingleCommand
)

// Use pointer receiver throughout so viewport mutations persist
type model struct {
	theme      *theme.Theme
	commands   []fakeCommand
	view       viewMode
	cursor     int            // command list cursor
	selected   int            // which command is drilled into
	updateCur  int            // resource list cursor in single command view
	expanded   map[int]bool   // set of expanded update indices
	sortCol    sortColumn
	spinner    spinner.Model
	vp         viewport.Model
	width      int
	height     int
	ready      bool
	quitting   bool
}

func newModel() *model {
	th := theme.New("formae")
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(th.Palette.InProgress)

	cmds := fakeCommands()
	sortCommands(cmds, sortByStatus)

	return &model{
		theme:    th,
		commands: cmds,
		sortCol:  sortByStatus,
		expanded: make(map[int]bool),
		spinner:  s,
		width:      80,
		height:     24,
	}
}

func (m *model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := m.height - 7
		if vpH < 1 {
			vpH = 1
		}
		if !m.ready {
			m.vp = viewport.New(m.width, vpH)
			m.ready = true
		} else {
			m.vp.Width = m.width
			m.vp.Height = vpH
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("enter", " "))):
			if m.view == viewMultiCommand {
				m.view = viewSingleCommand
				m.selected = m.cursor
				m.updateCur = 0
				m.expanded = make(map[int]bool)
				m.vp.GotoTop()
			} else {
				// Toggle expand on current update
				if m.expanded[m.updateCur] {
					delete(m.expanded, m.updateCur)
				} else {
					m.expanded[m.updateCur] = true
				}
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "backspace"))):
			if m.view == viewSingleCommand {
				m.view = viewMultiCommand
				m.cursor = m.selected
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
			if m.view == viewMultiCommand {
				m.sortCol = (m.sortCol + 1) % sortColumnCount
				sortCommands(m.commands, m.sortCol)
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if m.view == viewMultiCommand {
				if m.cursor < len(m.commands)-1 {
					m.cursor++
				}
			} else {
				totalUpdates := len(m.commands[m.selected].Updates)
				if m.updateCur < totalUpdates-1 {
					m.updateCur++
				}
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.view == viewMultiCommand {
				if m.cursor > 0 {
					m.cursor--
				}
			} else {
				if m.updateCur > 0 {
					m.updateCur--
				}
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m *model) View() string {
	if m.quitting {
		return ""
	}
	switch m.view {
	case viewMultiCommand:
		return m.viewMultiCommand()
	case viewSingleCommand:
		return m.viewSingleCommand()
	}
	return ""
}

// -- Status counts for a command --

type statusCounts struct {
	done       int
	failed     int
	inProgress int
	pending    int
}

func countStatuses(updates []fakeUpdate) statusCounts {
	var c statusCounts
	for _, u := range updates {
		switch u.State {
		case stateDone:
			c.done++
		case stateFailed, stateSkipped:
			c.failed++
		case stateInProgress:
			c.inProgress++
		case statePending:
			c.pending++
		}
	}
	return c
}

// -- Multi-command view --

func (m *model) viewMultiCommand() string {
	p := m.theme.Palette
	w := m.width

	// Header
	header := renderHeaderBar("formae status command", "↻ live", p, w)

	// Column headers
	// Fixed columns: status(3) + ID(14) + Type(9) + Mode(11) + bar(elastic) + ✓(4) + ✗(4) + ◐(4) + ·(4) + Time(6)
	sortIndicator := ""
	if m.sortCol != sortByStatus {
		switch m.sortCol {
		case sortByTime:
			sortIndicator = " Time▼"
		case sortByProgress:
			sortIndicator = " Progress▼"
		case sortByID:
			sortIndicator = " ID▼"
		}
	}

	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	bw := barWidth(w)

	// Build column header with manual padding to avoid ANSI width issues
	colLine := "     " +
		padRight(dimStyle.Render("ID"), 14) +
		padRight(dimStyle.Render("Command"), 9) +
		padRight(dimStyle.Render("Mode"), 11) +
		padRight(dimStyle.Render("Progress"), bw) +
		" " +
		padRight(lipgloss.NewStyle().Foreground(p.Done).Render("✓"), 4) +
		padRight(lipgloss.NewStyle().Foreground(p.Error).Render("✗"), 4) +
		padRight(lipgloss.NewStyle().Foreground(p.InProgress).Render("◐"), 4) +
		padRight(lipgloss.NewStyle().Foreground(p.Pending).Render("○"), 4) +
		"  " + dimStyle.Render("Time")
	if sortIndicator != "" {
		colLine += lipgloss.NewStyle().Foreground(p.PrimaryAccent).Render(sortIndicator)
	}

	// Rows
	var rows []string
	for i, cmd := range m.commands {
		rows = append(rows, m.renderCommandRow(cmd, w, i == m.cursor))
	}

	// Footer
	footer := m.renderFooter(w, []keyHint{
		{"enter", "drill in"},
		{"s", "sort"},
		{"↑↓/j/k", "navigate"},
		{"q", "quit"},
	}, "")

	// Compose
	var b strings.Builder
	b.WriteString(header + "\n")
	b.WriteString(colLine + "\n")
	for _, row := range rows {
		b.WriteString(row + "\n")
	}
	usedLines := 3 + len(rows)
	for i := usedLines; i < m.height-2; i++ {
		b.WriteString("\n")
	}
	b.WriteString(footer)
	return b.String()
}

func barWidth(termWidth int) int {
	// Fixed columns take ~60 chars; bar gets the rest
	// status(3) + ID(14) + Type(9) + Mode(11) + counts(4*4=16) + Time(6) + spacing(~5)
	fixed := 64
	bw := termWidth - fixed
	if bw < 10 {
		bw = 10
	}
	return bw
}

func (m *model) renderCommandRow(cmd fakeCommand, w int, isCursor bool) string {
	p := m.theme.Palette

	sc := countStatuses(cmd.Updates)
	total := sc.done + sc.failed + sc.inProgress + sc.pending
	bw := barWidth(w)

	bg := lipgloss.Color("")
	if isCursor {
		bg = lipgloss.Color("239") // visible but not overpowering
	}

	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isCursor {
			return s.Background(bg)
		}
		return s
	}

	// Status symbol (far left) — fixed width column
	padSym := func(s string, w int) string {
		if isCursor {
			return padRightBg(s, w, bg)
		}
		return padRight(s, w)
	}
	var statusSym string
	switch cmd.State {
	case "Failed":
		statusSym = padSym(withBg(lipgloss.NewStyle().Foreground(p.Error).Bold(true)).Render("✗"), 2)
	case "Done":
		statusSym = padSym(withBg(lipgloss.NewStyle().Foreground(p.Done)).Render("✓"), 2)
	case "InProgress":
		// Get spinner view, then wrap it with background if cursor
		spinView := m.spinner.View()
		if isCursor {
			// Strip existing style and re-wrap with background
			spinView = lipgloss.NewStyle().Background(bg).Render(spinView)
		}
		statusSym = padSym(spinView, 2)
	default:
		statusSym = padSym(withBg(lipgloss.NewStyle().Foreground(p.Pending)).Render("○"), 2)
	}

	idStyle := withBg(lipgloss.NewStyle().Foreground(p.PrimaryAccent))
	plainStyle := withBg(lipgloss.NewStyle())
	dimStyle := withBg(lipgloss.NewStyle().Foreground(p.TextSecondary))
	subtleStyle := withBg(lipgloss.NewStyle().Foreground(p.TextSubtle))

	pad := func(s string, w int) string {
		if isCursor {
			return padRightBg(s, w, bg)
		}
		return padRight(s, w)
	}

	id := pad(idStyle.Render(cmd.ID), 14)
	cmdType := pad(plainStyle.Render(cmd.CmdType), 9)
	mode := pad(plainStyle.Render(cmd.Mode), 11)
	dur := pad(dimStyle.Render(formatDuration(cmd.Duration)), 6)

	// Progress area: bar + N/total for in-progress, "completed N/N" for terminal
	isTerminal := cmd.State == "Done" || cmd.State == "Failed"
	var progressArea string
	if isTerminal {
		completedText := fmt.Sprintf("completed %d/%d", total, total)
		progressArea = pad(subtleStyle.Render(completedText), bw+7)
	} else {
		bar := renderSegmentedBar(sc, total, bw, p, bg, isCursor)
		done := sc.done + sc.failed
		countStr := dimStyle.Render(fmt.Sprintf("%d/%d", done, total))
		progressArea = bar + pad("", 1) + pad(countStr, 6)
	}

	// Per-status count columns, colored
	renderCount := func(n int, color lipgloss.AdaptiveColor) string {
		s := withBg(lipgloss.NewStyle())
		if n == 0 {
			return pad(s.Foreground(p.TextSubtle).Render("0"), 4)
		}
		return pad(s.Foreground(color).Render(fmt.Sprintf("%d", n)), 4)
	}

	doneCol := renderCount(sc.done, p.Done)
	failCol := renderCount(sc.failed, p.Error)
	ipCol := renderCount(sc.inProgress, p.InProgress)
	pendCol := renderCount(sc.pending, p.Pending)

	// Build row — use bg-colored spaces for cursor rows
	sp := " "
	sp2 := "  "
	if isCursor {
		bgStyle := lipgloss.NewStyle().Background(bg)
		sp = bgStyle.Render(" ")
		sp2 = bgStyle.Render("  ")
	}

	row := sp2 + statusSym + sp2 + id + cmdType + mode + progressArea + sp + doneCol + failCol + ipCol + pendCol + sp2 + dur

	// Pad the rest to full width
	rowWidth := lipgloss.Width(row)
	if rowWidth < w {
		if isCursor {
			row += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", w-rowWidth))
		} else {
			row += strings.Repeat(" ", w-rowWidth)
		}
	}

	return row
}

// -- Segmented progress bar --

func renderSegmentedBar(sc statusCounts, total, width int, p theme.Palette, bg lipgloss.Color, hasBg bool) string {
	if total == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(p.Pending)
		if hasBg {
			emptyStyle = emptyStyle.Background(bg)
		}
		return emptyStyle.Render(strings.Repeat("░", width))
	}

	// Calculate segment widths proportionally
	doneW := width * sc.done / total
	failW := width * sc.failed / total
	ipW := width * sc.inProgress / total
	pendW := width - doneW - failW - ipW
	if pendW < 0 {
		pendW = 0
	}

	// Distinct characters per state — colorblind-safe by density:
	// █ (solid) > ▒ (hatched) > ░ (light) > ⋅ (dots)
	doneStyle := lipgloss.NewStyle().Foreground(p.Done)
	failStyle := lipgloss.NewStyle().Foreground(p.Error)
	ipStyle := lipgloss.NewStyle().Foreground(p.InProgress)
	pendStyle := lipgloss.NewStyle().Foreground(p.Pending)

	if hasBg {
		doneStyle = doneStyle.Background(bg)
		failStyle = failStyle.Background(bg)
		ipStyle = ipStyle.Background(bg)
		pendStyle = pendStyle.Background(bg)
	}

	return doneStyle.Render(strings.Repeat("█", doneW)) +
		failStyle.Render(strings.Repeat("▒", failW)) +
		ipStyle.Render(strings.Repeat("░", ipW)) +
		pendStyle.Render(strings.Repeat("⋅", pendW))
}

// -- Single command view --

func (m *model) viewSingleCommand() string {
	p := m.theme.Palette
	w := m.width

	cmd := m.commands[m.selected]
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)

	// Header
	header := renderHeaderBar("← esc/backspace", "↻ live", p, w)

	// Reuse the same command row from the multi-command view
	bw := barWidth(w)
	cmdColHeader := "     " +
		padRight(dimStyle.Render("ID"), 14) +
		padRight(dimStyle.Render("Command"), 9) +
		padRight(dimStyle.Render("Mode"), 11) +
		padRight(dimStyle.Render("Progress"), bw) +
		" " +
		padRight(lipgloss.NewStyle().Foreground(p.Done).Render("✓"), 4) +
		padRight(lipgloss.NewStyle().Foreground(p.Error).Render("✗"), 4) +
		padRight(lipgloss.NewStyle().Foreground(p.InProgress).Render("◐"), 4) +
		padRight(lipgloss.NewStyle().Foreground(p.Pending).Render("○"), 4) +
		"  " + dimStyle.Render("Time")

	cmdRow := m.renderCommandRow(cmd, w, false)

	// Separator
	sep := lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", w))

	// Build flat sorted list of all updates for cursor tracking
	allUpdates := sortedUpdates(cmd.Updates)

	// Section title style
	titleStyle := lipgloss.NewStyle().
		Foreground(p.SecondaryAccent).
		Bold(true)

	// Build scrollable body with per-row expand
	var body strings.Builder
	groups := []struct {
		kind    updateKind
		label   string
		hasType bool
	}{
		{kindTarget, "Targets", false},
		{kindStack, "Stacks", false},
		{kindPolicy, "Policies", true},
		{kindResource, "Resources", true},
	}

	idx := 0
	for _, g := range groups {
		var groupUpdates []fakeUpdate
		for _, u := range allUpdates {
			if u.Kind == g.kind {
				groupUpdates = append(groupUpdates, u)
			}
		}
		if len(groupUpdates) == 0 {
			continue
		}

		body.WriteString("\n  " + titleStyle.Render("▌ "+g.label) + "\n")
		if g.hasType {
			body.WriteString("       " +
				padRight(dimStyle.Render("Label"), 24) +
				padRight(dimStyle.Render("Type"), 30) +
				padRight(dimStyle.Render("Operation"), 10) +
				dimStyle.Render("Time") + "\n")
		} else {
			body.WriteString("       " +
				padRight(dimStyle.Render("Label"), 54) +
				padRight(dimStyle.Render("Operation"), 10) +
				dimStyle.Render("Time") + "\n")
		}

		for _, u := range groupUpdates {
			isCur := idx == m.updateCur
			isExpanded := m.expanded[idx]

			if isExpanded {
				body.WriteString(m.renderDetailEntryWithHighlight(u, w, isCur))
			} else {
				body.WriteString(m.renderUpdateRowHighlight(u, w, isCur))
			}
			idx++
		}
	}

	m.vp.SetContent(body.String())

	// Footer
	footer := m.renderFooter(w, []keyHint{
		{"enter/space", "expand"},
		{"↑↓/j/k", "navigate"},
		{"esc", "back"},
	}, "")

	return header + "\n" +
		cmdColHeader + "\n" +
		cmdRow + "\n" +
		sep + "\n" +
		m.vp.View() + "\n" +
		footer
}

// -- Rendering helpers --

func (m *model) renderUpdateRowHighlight(u fakeUpdate, w int, isCursor bool) string {
	p := m.theme.Palette

	bg := lipgloss.Color("")
	if isCursor {
		bg = lipgloss.Color("239")
	}

	withBg := func(s lipgloss.Style) lipgloss.Style {
		if isCursor {
			return s.Background(bg)
		}
		return s
	}

	pad := func(s string, w int) string {
		if isCursor {
			return padRightBg(s, w, bg)
		}
		return padRight(s, w)
	}

	// Status symbol
	stateStr := m.renderStateSymbol(u)
	if isCursor {
		if u.State == stateInProgress {
			stateStr = lipgloss.NewStyle().Background(bg).Render(m.spinner.View())
		}
		stateStr = padRightBg(stateStr, 2, bg)
	} else {
		stateStr = padRight(stateStr, 2)
	}

	labelStyle := withBg(lipgloss.NewStyle().Foreground(p.PrimaryAccent))
	typeStyle := withBg(lipgloss.NewStyle().Foreground(p.TextSecondary))
	opStyle := withBg(lipgloss.NewStyle().Foreground(p.TextSecondary))
	dimStyle := withBg(lipgloss.NewStyle().Foreground(p.TextSubtle))

	label := u.Label
	hasType := u.Kind == kindResource || u.Kind == kindPolicy
	typeName := u.TypeName
	if u.Kind == kindPolicy {
		typeName = u.TypeName + ", " + u.Stack
	}

	var timeStr string
	switch u.State {
	case stateDone, stateInProgress, stateFailed:
		timeStr = formatDuration(u.Duration)
	default:
		timeStr = "—"
	}

	sp2 := "  "
	if isCursor {
		sp2 = lipgloss.NewStyle().Background(bg).Render("  ")
	}

	var row string
	if hasType {
		row = sp2 + stateStr + sp2 +
			pad(labelStyle.Render(label), 24) +
			pad(typeStyle.Render(typeName), 30) +
			pad(opStyle.Render(u.Operation), 10) +
			dimStyle.Render(timeStr)
	} else {
		row = sp2 + stateStr + sp2 +
			pad(labelStyle.Render(label), 54) +
			pad(opStyle.Render(u.Operation), 10) +
			dimStyle.Render(timeStr)
	}

	// Pad to full width
	rowWidth := lipgloss.Width(row)
	if rowWidth < w && isCursor {
		row += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", w-rowWidth))
	}

	// If failed, show error on next line
	if u.State == stateFailed && u.ErrorMessage != "" {
		errLine := "       " + lipgloss.NewStyle().Foreground(p.Error).Render(u.ErrorMessage)
		return row + "\n" + errLine + "\n"
	}
	if u.State == stateSkipped && u.CascadeSrc != "" {
		depLine := "       " + withBg(lipgloss.NewStyle().Foreground(p.TextSubtle)).Render("Depends on: "+u.CascadeSrc+" (failed)")
		return row + "\n" + depLine + "\n"
	}

	return row + "\n"
}

func (m *model) renderUpdateRow(u fakeUpdate, w int) string {
	p := m.theme.Palette

	// Status symbol (far left) — fixed width, no duration bleed
	stateStr := m.renderStateSymbol(u)

	labelStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent)
	typeStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	opStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSubtle)

	label := u.Label
	hasType := u.Kind == kindResource || u.Kind == kindPolicy
	typeName := u.TypeName
	if u.Kind == kindPolicy {
		typeName = u.TypeName + ", " + u.Stack
	}

	// Time column
	var timeStr string
	switch u.State {
	case stateDone, stateInProgress, stateFailed:
		timeStr = formatDuration(u.Duration)
	default:
		timeStr = "—"
	}

	var row string
	if hasType {
		row = "  " + padRight(stateStr, 3) + "  " +
			padRight(labelStyle.Render(label), 24) +
			padRight(typeStyle.Render(typeName), 30) +
			padRight(opStyle.Render(u.Operation), 10) +
			dimStyle.Render(timeStr)
	} else {
		row = "  " + padRight(stateStr, 3) + "  " +
			padRight(labelStyle.Render(label), 54) +
			padRight(opStyle.Render(u.Operation), 10) +
			dimStyle.Render(timeStr)
	}

	// If failed, show error on next line
	if u.State == stateFailed && u.ErrorMessage != "" {
		errLine := "       " + lipgloss.NewStyle().Foreground(p.Error).Render(u.ErrorMessage)
		return row + "\n" + errLine + "\n"
	}
	// If skipped with cascade, show dependency
	if u.State == stateSkipped && u.CascadeSrc != "" {
		depLine := "       " + dimStyle.Render("Depends on: "+u.CascadeSrc+" (failed)")
		return row + "\n" + depLine + "\n"
	}

	return row + "\n"
}

func (m *model) renderSummaryLine(u fakeUpdate, w int) string {
	p := m.theme.Palette
	op := lipgloss.NewStyle().Foreground(p.TextSecondary).Width(10).Render(u.Operation)
	label := formatLabel(u, p)
	stateStr := m.renderStateIndicator(u)

	left := fmt.Sprintf("    %s %s", op, label)
	return left + padBetween(w, left, stateStr+"  ") + stateStr + "  \n"
}

func (m *model) renderDetailEntryHighlighted(u fakeUpdate, w int) string {
	return m.renderDetailEntryWithHighlight(u, w, true)
}

func (m *model) renderDetailEntry(u fakeUpdate, w int) string {
	return m.renderDetailEntryWithHighlight(u, w, false)
}

func (m *model) renderDetailEntryWithHighlight(u fakeUpdate, w int, highlighted bool) string {
	p := m.theme.Palette

	field := lipgloss.NewStyle().Foreground(p.TextSubtle)
	value := lipgloss.NewStyle().Foreground(p.TextSecondary)

	// Build card content as key-value pairs
	var contentLines []string

	// Always show operation
	contentLines = append(contentLines, field.Render("Operation:")+" "+value.Render(u.Operation))

	if u.Kind == kindResource {
		contentLines = append(contentLines, field.Render("Type:")+"      "+value.Render(u.TypeName))
		contentLines = append(contentLines, field.Render("Stack:")+"     "+value.Render(u.Stack))
	}
	if u.Kind == kindPolicy {
		contentLines = append(contentLines, field.Render("Policy:")+"    "+value.Render(u.TypeName))
		contentLines = append(contentLines, field.Render("Stack:")+"     "+value.Render(u.Stack))
	}

	switch u.State {
	case stateInProgress:
		contentLines = append(contentLines, field.Render("Time:")+"      "+value.Render(formatDuration(u.Duration)))
		if u.MaxAttempts > 0 {
			contentLines = append(contentLines, field.Render("Attempt:")+"   "+
				value.Render(fmt.Sprintf("%d/%d", u.Attempt, u.MaxAttempts)))
		}
		if u.StatusMsg != "" {
			contentLines = append(contentLines, field.Render("Status:")+"    "+
				lipgloss.NewStyle().Foreground(p.InProgress).Render(u.StatusMsg))
		}
	case stateDone:
		contentLines = append(contentLines, field.Render("Time:")+"      "+value.Render(formatDuration(u.Duration)))
	case stateFailed:
		contentLines = append(contentLines, field.Render("Time:")+"      "+value.Render(formatDuration(u.Duration)))
		if u.MaxAttempts > 0 {
			contentLines = append(contentLines, field.Render("Attempt:")+"   "+
				value.Render(fmt.Sprintf("%d/%d", u.Attempt, u.MaxAttempts)))
		}
		if u.ErrorMessage != "" {
			contentLines = append(contentLines, field.Render("Error:")+"     "+
				lipgloss.NewStyle().Foreground(p.Error).Render(u.ErrorMessage))
		}
	case stateSkipped:
		if u.CascadeSrc != "" {
			contentLines = append(contentLines, field.Render("Depends on:")+" "+
				value.Render(u.CascadeSrc+" (failed)"))
		}
	}

	content := strings.Join(contentLines, "\n")

	// Border color: highlighted = accent, failed = red, default = border
	borderColor := p.Border
	if u.State == stateFailed {
		borderColor = p.Error
	}
	if highlighted {
		borderColor = p.PrimaryAccent
	}

	// Card title: status symbol + label
	stateStr := m.renderStateSymbol(u)
	labelStr := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Render(u.Label)

	cardWidth := w - 8
	if cardWidth < 30 {
		cardWidth = 30
	}

	// Render bordered card body
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardWidth).
		Padding(0, 1).
		Render(content)

	// Replace the top border with custom header: ╭─ symbol label ─────╮
	// Measure the actual rendered bottom border to get the true card width
	cardLines := strings.Split(card, "\n")
	actualWidth := 0
	if len(cardLines) > 1 {
		actualWidth = lipgloss.Width(cardLines[len(cardLines)-1])
	}
	if actualWidth == 0 {
		actualWidth = cardWidth + 2
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	titleContent := " " + stateStr + " " + labelStr + " "
	titleW := lipgloss.Width(titleContent)
	// Total = ╭ + ─ + titleContent + dashes + ╮ = actualWidth
	dashW := actualWidth - titleW - 3 // 3 = ╭ + ─ + ╮
	if dashW < 1 {
		dashW = 1
	}
	headerLine := borderStyle.Render("╭─") +
		titleContent +
		borderStyle.Render(strings.Repeat("─", dashW) + "╮")

	if len(cardLines) > 0 {
		cardLines[0] = headerLine
	}

	return "    " + strings.Join(cardLines, "\n    ") + "\n"
}

// renderStateSymbol returns just the status symbol (fixed width, no duration).
func (m *model) renderStateSymbol(u fakeUpdate) string {
	p := m.theme.Palette
	switch u.State {
	case stateDone:
		return lipgloss.NewStyle().Foreground(p.Done).Render("✓")
	case stateInProgress:
		return lipgloss.NewStyle().Foreground(p.InProgress).Render(m.spinner.View())
	case statePending:
		return lipgloss.NewStyle().Foreground(p.Pending).Render("○")
	case stateFailed:
		return lipgloss.NewStyle().Foreground(p.Error).Bold(true).Render("✗")
	case stateSkipped:
		return lipgloss.NewStyle().Foreground(p.TextSecondary).Render("⊘")
	}
	return " "
}

// renderStateIndicator returns symbol + duration (for detail cards and old summary lines).
func (m *model) renderStateIndicator(u fakeUpdate) string {
	p := m.theme.Palette
	switch u.State {
	case stateDone:
		return lipgloss.NewStyle().Foreground(p.Done).Render("✓")
	case stateInProgress:
		return lipgloss.NewStyle().Foreground(p.InProgress).Render(
			m.spinner.View() + " " + formatDuration(u.Duration))
	case statePending:
		return lipgloss.NewStyle().Foreground(p.Pending).Render("○")
	case stateFailed:
		return lipgloss.NewStyle().Foreground(p.Error).Bold(true).Render("✗")
	case stateSkipped:
		return lipgloss.NewStyle().Foreground(p.TextSecondary).Render("⊘")
	}
	return ""
}

// -- Shared rendering --

func renderHeaderBar(left, right string, p theme.Palette, w int) string {
	leftStyled := "  " + left
	rightStyled := right + "  "
	content := leftStyled + padBetween(w, leftStyled, rightStyled) + rightStyled

	return lipgloss.NewStyle().
		Foreground(p.TextPrimary).Bold(true).
		Width(w).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(p.Border).
		Render(content)
}

func formatLabel(u fakeUpdate, p theme.Palette) string {
	labelStyle := lipgloss.NewStyle().Foreground(p.TextPrimary)
	typeStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	switch u.Kind {
	case kindResource:
		return labelStyle.Render(u.Label) + typeStyle.Render(" ("+u.TypeName+")")
	case kindPolicy:
		return labelStyle.Render(u.Label) + typeStyle.Render(" ("+u.TypeName+", "+u.Stack+")")
	default:
		return labelStyle.Render(u.Label)
	}
}

type keyHint struct {
	key  string
	desc string
}

func (m *model) renderFooter(w int, hints []keyHint, scrollInfo string) string {
	p := m.theme.Palette
	s := m.theme.Styles

	var parts []string
	for _, h := range hints {
		parts = append(parts,
			s.KeybindingKey.Render(h.key)+s.KeybindingDesc.Render(": "+h.desc))
	}
	left := "  " + strings.Join(parts, "  ")

	rightParts := []string{}
	if scrollInfo != "" {
		rightParts = append(rightParts, scrollInfo)
	}
	rightParts = append(rightParts, s.KeybindingKey.Render("?")+s.KeybindingDesc.Render(": help"))
	right := strings.Join(rightParts, "  ") + "  "

	content := left + padBetween(w, left, right) + right

	return lipgloss.NewStyle().
		Width(w).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(p.Border).
		Render(content)
}

// -- Helpers --

// padRight pads a (possibly styled) string to exactly w visible characters.
func padRight(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

// padRightBg pads with background-colored spaces.
func padRightBg(s string, w int, bg lipgloss.Color) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", w-visible))
}

func padBetween(totalWidth int, left, right string) string {
	space := totalWidth - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		space = 1
	}
	return strings.Repeat(" ", space)
}

func countCompleted(updates []fakeUpdate) (completed, total int) {
	for _, u := range updates {
		total++
		if isTerminalState(u.State) {
			completed++
		}
	}
	return
}

func filterByKind(updates []fakeUpdate, kind updateKind) []fakeUpdate {
	var result []fakeUpdate
	for _, u := range updates {
		if u.Kind == kind {
			result = append(result, u)
		}
	}
	// Sort by status urgency: failed → in-progress → pending → done/skipped
	sort.SliceStable(result, func(i, j int) bool {
		return updateStatePriority(result[i].State) < updateStatePriority(result[j].State)
	})
	return result
}

// sortedUpdates returns all updates sorted by status urgency within each kind.
func sortedUpdates(updates []fakeUpdate) []fakeUpdate {
	result := make([]fakeUpdate, len(updates))
	copy(result, updates)
	sort.SliceStable(result, func(i, j int) bool {
		return updateStatePriority(result[i].State) < updateStatePriority(result[j].State)
	})
	return result
}

func updateStatePriority(s updateState) int {
	switch s {
	case stateFailed:
		return 0
	case stateInProgress:
		return 1
	case statePending:
		return 2
	case stateDone:
		return 3
	case stateSkipped:
		return 4
	}
	return 5
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// -- Fake data --

func fakeCommands() []fakeCommand {
	return []fakeCommand{
		{
			ID: "cmd-abc123", CmdType: "apply", Mode: "reconcile",
			State: "InProgress", Duration: 42 * time.Second,
			Updates: []fakeUpdate{
				{Kind: kindTarget, Operation: "create", Label: "aws-us-east-1", State: stateDone, Duration: 3 * time.Second},
				{Kind: kindStack, Operation: "create", Label: "production", State: stateDone, Duration: 1 * time.Second},
				{Kind: kindPolicy, Operation: "create", Label: "auto-reconcile", TypeName: "ttl", Stack: "production", State: stateDone, Duration: 1 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "my-bucket", TypeName: "AWS::S3::Bucket", Stack: "production", State: stateInProgress, Duration: 5 * time.Second, Attempt: 1, MaxAttempts: 9, StatusMsg: "Creating resource..."},
				{Kind: kindResource, Operation: "create", Label: "web-1", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 8 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "web-2", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 7 * time.Second},
				{Kind: kindResource, Operation: "update", Label: "primary", TypeName: "AWS::RDS::DBInstance", Stack: "production", State: stateInProgress, Duration: 12 * time.Second, Attempt: 2, MaxAttempts: 9, StatusMsg: "Modifying database instance..."},
				{Kind: kindResource, Operation: "create", Label: "web-3", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 6 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "web-4", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 5 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "web-5", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 7 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "web-6", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 6 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "web-7", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 8 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "web-8", TypeName: "AWS::EC2::Instance", Stack: "production", State: stateDone, Duration: 5 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "cdn", TypeName: "AWS::CloudFront::Distribution", Stack: "production", State: statePending},
			},
		},
		{
			ID: "cmd-def456", CmdType: "destroy", Mode: "",
			State: "Done", Duration: 15 * time.Second,
			Updates: []fakeUpdate{
				{Kind: kindResource, Operation: "delete", Label: "old-bucket", TypeName: "AWS::S3::Bucket", Stack: "staging", State: stateDone, Duration: 3 * time.Second},
				{Kind: kindResource, Operation: "delete", Label: "legacy-app", TypeName: "AWS::Lambda::Function", Stack: "staging", State: stateDone, Duration: 2 * time.Second},
				{Kind: kindResource, Operation: "delete", Label: "old-db", TypeName: "AWS::RDS::DBInstance", Stack: "staging", State: stateDone, Duration: 8 * time.Second},
			},
		},
		{
			ID: "cmd-ghi789", CmdType: "apply", Mode: "patch",
			State: "Failed", Duration: 33 * time.Second,
			Updates: []fakeUpdate{
				{Kind: kindResource, Operation: "update", Label: "api-config", TypeName: "AWS::SSM::Parameter", Stack: "production", State: stateDone, Duration: 2 * time.Second},
				{Kind: kindResource, Operation: "delete", Label: "old-data", TypeName: "AWS::S3::Bucket", Stack: "production", State: stateFailed, Duration: 4 * time.Second, Attempt: 9, MaxAttempts: 9, ErrorMessage: "BucketNotEmpty: The bucket is not empty"},
				{Kind: kindResource, Operation: "delete", Label: "old-logs", TypeName: "AWS::S3::Bucket", Stack: "production", State: stateSkipped, CascadeSrc: "old-data"},
			},
		},
		{
			ID: "cmd-jkl012", CmdType: "apply", Mode: "reconcile",
			State: "InProgress", Duration: 118 * time.Second,
			Updates: []fakeUpdate{
				{Kind: kindTarget, Operation: "create", Label: "aws-eu-west-1", State: stateDone, Duration: 3 * time.Second},
				{Kind: kindStack, Operation: "create", Label: "staging", State: stateDone, Duration: 1 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "vpc", TypeName: "AWS::EC2::VPC", Stack: "staging", State: stateDone, Duration: 10 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "subnet-a", TypeName: "AWS::EC2::Subnet", Stack: "staging", State: stateDone, Duration: 8 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "subnet-b", TypeName: "AWS::EC2::Subnet", Stack: "staging", State: stateDone, Duration: 7 * time.Second},
				{Kind: kindResource, Operation: "create", Label: "rds-primary", TypeName: "AWS::RDS::DBInstance", Stack: "staging", State: stateFailed, Duration: 45 * time.Second, Attempt: 9, MaxAttempts: 9, ErrorMessage: "InsufficientDBInstanceCapacity"},
				{Kind: kindResource, Operation: "create", Label: "rds-replica", TypeName: "AWS::RDS::DBInstance", Stack: "staging", State: stateSkipped, CascadeSrc: "rds-primary"},
				{Kind: kindResource, Operation: "create", Label: "cache", TypeName: "AWS::ElastiCache::CacheCluster", Stack: "staging", State: stateInProgress, Duration: 30 * time.Second, Attempt: 1, MaxAttempts: 9, StatusMsg: "Creating cache cluster..."},
				{Kind: kindResource, Operation: "create", Label: "api-gw", TypeName: "AWS::ApiGateway::RestApi", Stack: "staging", State: stateInProgress, Duration: 15 * time.Second, Attempt: 1, MaxAttempts: 9, StatusMsg: "Creating API..."},
				{Kind: kindResource, Operation: "create", Label: "lambda-auth", TypeName: "AWS::Lambda::Function", Stack: "staging", State: statePending},
				{Kind: kindResource, Operation: "create", Label: "lambda-api", TypeName: "AWS::Lambda::Function", Stack: "staging", State: statePending},
				{Kind: kindResource, Operation: "create", Label: "dns", TypeName: "AWS::Route53::RecordSet", Stack: "staging", State: statePending},
			},
		},
	}
}

func main() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
