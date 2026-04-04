// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Prototype: Simulation preview TUI
// Run: go run ./docs/mockups/prototypes/simulation/
//
// Shows what apply/destroy will do before confirmation.
// Reuses the same table/expand/sort/pagination patterns as status/watch.
//
// NOTE TO IMPLEMENTERS: This view and the status command detail view share
// the same underlying table component, rendering logic, expand/collapse,
// sort, and pagination. The only differences are:
//   - Simulation has no "state" column (everything is pending)
//   - Simulation uses operation symbols (+, ~, -, ↻, ⊘, =) instead of status symbols
//   - Simulation shows property change diffs in expanded cards
//   - Simulation has a confirmation prompt instead of live updates
//   - Status detail has live state updates and spinners
//
// The shared table component should be extracted into internal/cli/tui/components/
// and parameterized for both use cases.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// -- Data types --

type opKind string

const (
	opCreate  opKind = "create"
	opUpdate  opKind = "update"
	opDelete  opKind = "delete"
	opReplace opKind = "replace"
	opDetach  opKind = "detach"
	opKeep    opKind = "keep"
)

type updateKind string

const (
	kindTarget   updateKind = "target"
	kindStack    updateKind = "stack"
	kindPolicy   updateKind = "policy"
	kindResource updateKind = "resource"
)

type patchEntry struct {
	Action   string // "set", "add", "remove"
	Path     string // property path
	OldValue string // old value (for set)
	NewValue string // new value (for set/add)
}

type simUpdate struct {
	Kind       updateKind
	Operation  opKind
	Label      string
	TypeName   string
	Stack      string
	Patches    []patchEntry  // property changes for updates
	CascadeSrc string        // for cascade deletes
	RefStacks  string        // for "keep" — still referenced by
}

// -- Operation symbols and colors (colorblind-safe) --

func opSymbol(op opKind) string {
	switch op {
	case opCreate:
		return "+"
	case opUpdate:
		return "~"
	case opDelete:
		return "-"
	case opReplace:
		return "↻"
	case opDetach:
		return "⊘"
	case opKeep:
		return "="
	}
	return "?"
}

func opColor(op opKind, p theme.Palette) lipgloss.TerminalColor {
	switch op {
	case opCreate:
		return p.Done // green
	case opUpdate:
		return p.PrimaryAccent // blue
	case opDelete:
		return p.Warning // yellow — destructive but intentional, not an error
	case opReplace:
		return p.SecondaryAccent // orange
	case opDetach:
		return p.TextSecondary // gray
	case opKeep:
		return p.TextSubtle // dim gray
	}
	return p.TextSecondary
}

// opText returns "symbol word" combined for the Operation column
func opText(op opKind) string {
	return opSymbol(op) + " " + string(op)
}

// -- Sort --

type sortDir int

const (
	sortAsc sortDir = iota
	sortDesc
)

type sortableColumn struct {
	name   string
	sortFn func(a, b simUpdate) bool
}

var sortableColumns = []sortableColumn{
	{"Operation", func(a, b simUpdate) bool { return opPriority(a.Operation) < opPriority(b.Operation) }}, // 0
	{"Label", func(a, b simUpdate) bool { return a.Label < b.Label }},                                     // 1
	{"Type", func(a, b simUpdate) bool { return a.TypeName < b.TypeName }},                                 // 2
	{"Stack", func(a, b simUpdate) bool { return a.Stack < b.Stack }},                                      // 3
}

func opPriority(op opKind) int {
	switch op {
	case opDelete:
		return 0
	case opReplace:
		return 1
	case opUpdate:
		return 2
	case opCreate:
		return 3
	case opDetach:
		return 4
	case opKeep:
		return 5
	}
	return 6
}

func sortSimUpdates(updates []simUpdate, colIdx int, dir sortDir) {
	if colIdx < 0 || colIdx >= len(sortableColumns) {
		return
	}
	asc := sortableColumns[colIdx].sortFn
	sort.SliceStable(updates, func(i, j int) bool {
		if dir == sortAsc {
			return asc(updates[i], updates[j])
		}
		return asc(updates[j], updates[i])
	})
}

func validSortIndices(kind updateKind) []int {
	switch kind {
	case kindPolicy:
		return []int{0, 1, 2, 3}
	case kindResource:
		return []int{0, 1, 2}
	default:
		return []int{0, 1}
	}
}

func nextSortHighlight(kind updateKind, current int, delta int) int {
	valid := validSortIndices(kind)
	pos := 0
	for i, v := range valid {
		if v == current {
			pos = i
			break
		}
	}
	pos = (pos + delta + len(valid)) % len(valid)
	return valid[pos]
}

type colLayout int

const (
	layoutSimple    colLayout = iota
	layoutType
	layoutTypeStack
)

// -- Model --

type model struct {
	theme      *theme.Theme
	updates    []simUpdate
	cmdType    string // "apply" or "destroy"
	mode       string // "reconcile" or "patch"
	cursor     int
	expanded   map[int]bool
	confirmed  bool
	width      int
	height     int
	quitting   bool
	// Per-group sort + pagination
	groupSortHighlight map[updateKind]int
	groupSortActiveCol map[updateKind]int
	groupSortDir       map[updateKind]sortDir
	groupVisibleCount  map[updateKind]int
}

func newModel() *model {
	return &model{
		theme:              theme.New("formae"),
		updates:            fakeSimulation(),
		cmdType:            "apply",
		mode:               "reconcile",
		expanded:           make(map[int]bool),
		width:              80,
		height:             24,
		groupSortHighlight: map[updateKind]int{kindTarget: 0, kindStack: 0, kindPolicy: 0, kindResource: 0},
		groupSortActiveCol: map[updateKind]int{kindTarget: 0, kindStack: 0, kindPolicy: 0, kindResource: 0},
		groupSortDir:       map[updateKind]sortDir{kindTarget: sortAsc, kindStack: sortAsc, kindPolicy: sortAsc, kindResource: sortAsc},
		groupVisibleCount:  map[updateKind]int{kindTarget: 10, kindStack: 10, kindPolicy: 10, kindResource: 10},
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.confirmed {
			return m, tea.Quit
		}

		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.isShowMoreRow(m.cursor) {
				grp := m.currentGroup()
				m.groupVisibleCount[grp] += 10
			} else if m.expanded[m.cursor] {
				delete(m.expanded, m.cursor)
			} else {
				m.expanded[m.cursor] = true
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys(" "))):
			if m.isShowMoreRow(m.cursor) {
				grp := m.currentGroup()
				m.groupVisibleCount[grp] += 10
			} else if m.expanded[m.cursor] {
				delete(m.expanded, m.cursor)
			} else {
				m.expanded[m.cursor] = true
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("y"))):
			m.confirmed = true
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
			grp := m.currentGroup()
			h := m.groupSortHighlight[grp]
			a := m.groupSortActiveCol[grp]
			d := m.groupSortDir[grp]
			if a == h {
				if d == sortDesc {
					m.groupSortDir[grp] = sortAsc
				} else {
					m.groupSortDir[grp] = sortDesc
				}
			} else {
				m.groupSortActiveCol[grp] = h
				m.groupSortDir[grp] = sortDesc
			}
			m.groupVisibleCount[grp] = 10
			m.expanded = make(map[int]bool)
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("right", "l"))):
			grp := m.currentGroup()
			m.groupSortHighlight[grp] = nextSortHighlight(grp, m.groupSortHighlight[grp], 1)
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("left", "h"))):
			grp := m.currentGroup()
			m.groupSortHighlight[grp] = nextSortHighlight(grp, m.groupSortHighlight[grp], -1)
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			max := m.totalFlatRows() - 1
			if m.cursor < max {
				m.cursor++
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("pgdown"))):
			pageSize := m.height - 7
			if pageSize < 1 {
				pageSize = 1
			}
			m.cursor += pageSize
			max := m.totalFlatRows() - 1
			if m.cursor > max {
				m.cursor = max
			}
			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("pgup"))):
			pageSize := m.height - 7
			if pageSize < 1 {
				pageSize = 1
			}
			m.cursor -= pageSize
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) View() string {
	if m.quitting {
		return ""
	}
	if m.confirmed {
		return lipgloss.NewStyle().Foreground(m.theme.Palette.Done).Render("  Command submitted.\n")
	}

	p := m.theme.Palette
	w := m.width

	// Header
	headerLeft := fmt.Sprintf("  formae %s — simulation", m.cmdType)
	headerRight := fmt.Sprintf("mode: %s  ", m.mode)
	header := lipgloss.NewStyle().
		Foreground(p.TextPrimary).Bold(true).
		Width(w).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(p.Border).
		Render(headerLeft + padBetween(w, headerLeft, headerRight) + headerRight)

	// Summary counts
	counts := m.summaryCounts()
	summaryStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	summary := "  " + summaryStyle.Render(counts)

	// Build body
	dimStyle := lipgloss.NewStyle().Foreground(p.TextSecondary)
	highlightStyle := lipgloss.NewStyle().
		Foreground(p.TextPrimary).
		Background(lipgloss.Color("237")).
		Bold(true)
	accentStyle := lipgloss.NewStyle().Foreground(p.PrimaryAccent).Bold(true)
	titleStyle := lipgloss.NewStyle().Foreground(p.SecondaryAccent).Bold(true)

	groups := []struct {
		kind   updateKind
		label  string
		layout colLayout
	}{
		{kindTarget, "Targets", layoutSimple},
		{kindStack, "Stacks", layoutSimple},
		{kindPolicy, "Policies", layoutTypeStack},
		{kindResource, "Resources", layoutType},
	}

	var bodyLines []string
	cursorToLine := map[int]int{} // maps cursor index → line index in bodyLines

	idx := 0
	for _, g := range groups {
		var groupUpdates []simUpdate
		for _, u := range m.updates {
			if u.Kind == g.kind {
				groupUpdates = append(groupUpdates, u)
			}
		}
		if len(groupUpdates) == 0 {
			continue
		}
		sortSimUpdates(groupUpdates, m.groupSortActiveCol[g.kind], m.groupSortDir[g.kind])

		curGrp := m.currentGroup()
		isActiveGroup := g.kind == curGrp

		grpHighlight := m.groupSortHighlight[g.kind]
		grpActive := m.groupSortActiveCol[g.kind]
		grpDir := m.groupSortDir[g.kind]

		renderColName := func(name string, sortIdx int) string {
			isHL := isActiveGroup && sortIdx == grpHighlight
			isAct := sortIdx == grpActive
			arrow := ""
			if isAct {
				if grpDir == sortDesc {
					arrow = "▼"
				} else {
					arrow = "▲"
				}
			}
			text := name + arrow
			if isHL {
				return highlightStyle.Render(text)
			}
			if isAct {
				return accentStyle.Render(text)
			}
			return dimStyle.Render(text)
		}

		// No separate symbol column — Operation column is the first column

		// Elastic column widths — Operation(12) is fixed, rest elastic
		opW := 12
		var labelW, typeW, stackW int
		switch g.layout {
		case layoutTypeStack:
			remainder := w - opW
			labelW = remainder * 2 / 5
			typeW = remainder * 1 / 5
			stackW = remainder - labelW - typeW
			if labelW < 12 { labelW = 12 }
			if typeW < 10 { typeW = 10 }
			if stackW < 10 { stackW = 10 }
		case layoutType:
			remainder := w - opW
			typeW = remainder * 2 / 5
			if typeW < 16 { typeW = 16 }
			labelW = remainder - typeW
			if labelW < 12 { labelW = 12 }
		default:
			labelW = w - opW
			if labelW < 20 { labelW = 20 }
		}
		_ = opW

		bodyLines = append(bodyLines, "")
		bodyLines = append(bodyLines, "  "+titleStyle.Render("▌ "+g.label))

		// Column headers
		switch g.layout {
		case layoutTypeStack:
			bodyLines = append(bodyLines,
				"  "+padRight(renderColName("Operation", 0), 12)+
					padRight(renderColName("Label", 1), labelW)+
					padRight(renderColName("Type", 2), typeW)+
					renderColName("Stack", 3))
		case layoutType:
			bodyLines = append(bodyLines,
				"  "+padRight(renderColName("Operation", 0), 12)+
					padRight(renderColName("Label", 1), labelW)+
					renderColName("Type", 2))
		default:
			bodyLines = append(bodyLines,
				"  "+padRight(renderColName("Operation", 0), 12)+
					renderColName("Label", 1))
		}

		// Rows with pagination
		visCount := m.groupVisibleCount[g.kind]
		showCount := len(groupUpdates)
		hasMore := false
		if showCount > visCount {
			showCount = visCount
			hasMore = true
		}
		remaining := len(groupUpdates) - showCount

		for i := 0; i < showCount; i++ {
			u := groupUpdates[i]
			isCur := idx == m.cursor
			isExpanded := m.expanded[idx]

			cursorToLine[idx] = len(bodyLines)
			if isExpanded {
				bodyLines = append(bodyLines, m.renderExpandedCard(u, w, isCur)...)
			} else {
				bodyLines = append(bodyLines, m.renderRow(u, w, isCur, g.layout, labelW, typeW, stackW))
			}
			idx++
		}

		if hasMore {
			isCur := idx == m.cursor
			cursorToLine[idx] = len(bodyLines)
			moreText := fmt.Sprintf("      ↓ show 10 more (%d remaining)", remaining)
			if isCur {
				bodyLines = append(bodyLines, lipgloss.NewStyle().
					Background(lipgloss.Color("239")).
					Foreground(p.PrimaryAccent).
					Bold(true).
					Width(w).
					Render(moreText))
			} else {
				bodyLines = append(bodyLines, lipgloss.NewStyle().Foreground(p.PrimaryAccent).Render(moreText))
			}
			idx++
		}
	}

	// Scroll the body — keep cursor line in view
	visibleRows := m.height - 6
	if visibleRows < 1 {
		visibleRows = 1
	}

	cursorLine := cursorToLine[m.cursor]
	scrollOffset := 0
	if cursorLine >= visibleRows {
		scrollOffset = cursorLine - visibleRows + 3
	}
	if scrollOffset > len(bodyLines)-visibleRows {
		scrollOffset = len(bodyLines) - visibleRows
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	end := scrollOffset + visibleRows
	if end > len(bodyLines) {
		end = len(bodyLines)
	}

	// Footer
	s := m.theme.Styles
	var footerParts []string
	footerParts = append(footerParts, s.KeybindingKey.Render("↑↓")+s.KeybindingDesc.Render(": select"))
	footerParts = append(footerParts, s.KeybindingKey.Render("space")+s.KeybindingDesc.Render(": expand"))
	footerParts = append(footerParts, s.KeybindingKey.Render("→←")+s.KeybindingDesc.Render(": column"))
	footerParts = append(footerParts, s.KeybindingKey.Render("s")+s.KeybindingDesc.Render(": sort"))
	footerParts = append(footerParts, s.KeybindingKey.Render("y")+s.KeybindingDesc.Render(": confirm"))
	footerParts = append(footerParts, s.KeybindingKey.Render("q")+s.KeybindingDesc.Render(": abort"))
	left := "  " + strings.Join(footerParts, "  ")
	right := s.KeybindingKey.Render("?") + s.KeybindingDesc.Render(": help  ")
	footerContent := left + padBetween(w, left, right) + right
	footer := lipgloss.NewStyle().
		Width(w).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(p.Border).
		Render(footerContent)

	var b strings.Builder
	b.WriteString(header + "\n")
	b.WriteString(summary + "\n")
	for i := scrollOffset; i < end; i++ {
		b.WriteString(bodyLines[i] + "\n")
	}
	rendered := end - scrollOffset
	for i := rendered; i < visibleRows; i++ {
		b.WriteString("\n")
	}
	b.WriteString(footer)
	return b.String()
}

// -- Row rendering --

func (m *model) renderRow(u simUpdate, w int, isCursor bool, layout colLayout, labelW, typeW, stackW int) string {
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

	trunc := func(s string, maxW int) string {
		if maxW < 4 { maxW = 4 }
		if len(s) > maxW-1 {
			return s[:maxW-2] + "…"
		}
		return s
	}

	// Combined Operation column: "symbol word" colored
	color := opColor(u.Operation, p)
	opStr := opText(u.Operation)
	opRendered := pad(withBg(lipgloss.NewStyle().Foreground(color).Bold(true)).Render(opStr), 12)

	labelStyle := withBg(lipgloss.NewStyle().Foreground(p.PrimaryAccent))
	typeStyle := withBg(lipgloss.NewStyle().Foreground(p.TextSecondary))

	// Brighten on cursor
	if isCursor {
		labelStyle = withBg(lipgloss.NewStyle().Foreground(lipgloss.Color("#93C5FD")))
		typeStyle = withBg(lipgloss.NewStyle().Foreground(p.TextPrimary))
	}

	// Delete rows: all yellow (destructive but intentional)
	if u.Operation == opDelete {
		if isCursor {
			labelStyle = withBg(lipgloss.NewStyle().Foreground(lipgloss.Color("#FDE68A"))) // bright yellow
			typeStyle = withBg(lipgloss.NewStyle().Foreground(lipgloss.Color("#FDE68A")))
		} else {
			labelStyle = withBg(lipgloss.NewStyle().Foreground(p.Warning))
			typeStyle = withBg(lipgloss.NewStyle().Foreground(p.Warning))
		}
	}

	sp2 := "  "
	if isCursor {
		sp2 = lipgloss.NewStyle().Background(bg).Render("  ")
	}

	var row string
	switch u.Kind {
	case kindPolicy:
		row = sp2 + opRendered +
			pad(labelStyle.Render(trunc(u.Label, labelW-1)), labelW) +
			pad(typeStyle.Render(trunc(u.TypeName, typeW-1)), typeW) +
			typeStyle.Render(trunc(u.Stack, stackW-1))
	case kindResource:
		row = sp2 + opRendered +
			pad(labelStyle.Render(trunc(u.Label, labelW-1)), labelW) +
			typeStyle.Render(trunc(u.TypeName, typeW-1))
	default:
		row = sp2 + opRendered +
			labelStyle.Render(trunc(u.Label, labelW-1))
	}

	rowWidth := lipgloss.Width(row)
	if rowWidth < w && isCursor {
		row += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", w-rowWidth))
	}

	// Cascade info on second line
	if u.CascadeSrc != "" {
		depLine := "              " + lipgloss.NewStyle().Foreground(p.TextSubtle).Render("depends on: "+u.CascadeSrc)
		return row + "\n" + depLine
	}
	if u.RefStacks != "" {
		refLine := "              " + lipgloss.NewStyle().Foreground(p.TextSubtle).Render("still referenced by: "+u.RefStacks)
		return row + "\n" + refLine
	}

	return row
}

// -- Expanded card --

func (m *model) renderExpandedCard(u simUpdate, w int, highlighted bool) []string {
	p := m.theme.Palette

	field := lipgloss.NewStyle().Foreground(p.TextSubtle)
	value := lipgloss.NewStyle().Foreground(p.TextSecondary)

	var contentLines []string

	contentLines = append(contentLines, field.Render("Operation:")+" "+
		lipgloss.NewStyle().Foreground(opColor(u.Operation, p)).Render(string(u.Operation)))

	if u.Kind == kindResource {
		contentLines = append(contentLines, field.Render("Type:")+"      "+value.Render(u.TypeName))
		contentLines = append(contentLines, field.Render("Stack:")+"     "+value.Render(u.Stack))
	}
	if u.Kind == kindPolicy {
		contentLines = append(contentLines, field.Render("Policy:")+"    "+value.Render(u.TypeName))
		contentLines = append(contentLines, field.Render("Stack:")+"     "+value.Render(u.Stack))
	}

	// Property changes
	if len(u.Patches) > 0 {
		contentLines = append(contentLines, "")
		contentLines = append(contentLines, field.Render("Changes:"))
		for i, patch := range u.Patches {
			connector := "├"
			if i == len(u.Patches)-1 {
				connector = "└"
			}
			var patchLine string
			switch patch.Action {
			case "set":
				patchLine = fmt.Sprintf(" %s %s  %s: %s → %s",
					connector,
					lipgloss.NewStyle().Foreground(p.PrimaryAccent).Render(patch.Action),
					value.Render(patch.Path),
					lipgloss.NewStyle().Foreground(p.Error).Render(patch.OldValue),
					lipgloss.NewStyle().Foreground(p.Done).Render(patch.NewValue))
			case "add":
				patchLine = fmt.Sprintf(" %s %s  %s: %s",
					connector,
					lipgloss.NewStyle().Foreground(p.Done).Render(patch.Action),
					value.Render(patch.Path),
					lipgloss.NewStyle().Foreground(p.Done).Render(patch.NewValue))
			case "remove":
				patchLine = fmt.Sprintf(" %s %s  %s",
					connector,
					lipgloss.NewStyle().Foreground(p.Error).Render(patch.Action),
					lipgloss.NewStyle().Foreground(p.Error).Render(patch.Path))
			}
			contentLines = append(contentLines, patchLine)
		}
	}

	if u.CascadeSrc != "" {
		contentLines = append(contentLines, field.Render("Cascade:")+"   "+value.Render("depends on "+u.CascadeSrc))
	}
	if u.RefStacks != "" {
		contentLines = append(contentLines, field.Render("Referenced:")+" "+value.Render(u.RefStacks))
	}

	content := strings.Join(contentLines, "\n")

	// Border color by operation
	borderColor := p.Border
	if u.Operation == opDelete {
		borderColor = p.Warning // yellow for destructive
	}
	if highlighted {
		borderColor = p.PrimaryAccent
	}

	// Card title
	color := opColor(u.Operation, p)
	symStr := lipgloss.NewStyle().Foreground(color).Bold(true).Render(opSymbol(u.Operation))
	var labelColor lipgloss.TerminalColor
	if u.Operation == opDelete {
		labelColor = p.Warning
	} else {
		labelColor = p.PrimaryAccent
	}
	labelStr := lipgloss.NewStyle().Foreground(labelColor).Render(u.Label)

	cardWidth := w - 8
	if cardWidth < 30 {
		cardWidth = 30
	}

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardWidth).
		Padding(0, 1).
		Render(content)

	cardLines := strings.Split(card, "\n")
	actualWidth := 0
	if len(cardLines) > 1 {
		actualWidth = lipgloss.Width(cardLines[len(cardLines)-1])
	}
	if actualWidth == 0 {
		actualWidth = cardWidth + 2
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	titleContent := " " + symStr + " " + labelStr + " "
	titleW := lipgloss.Width(titleContent)
	dashW := actualWidth - titleW - 3
	if dashW < 1 {
		dashW = 1
	}
	headerLine := borderStyle.Render("╭─") + titleContent +
		borderStyle.Render(strings.Repeat("─", dashW)+"╮")

	if len(cardLines) > 0 {
		cardLines[0] = headerLine
	}

	var result []string
	for _, line := range cardLines {
		result = append(result, "    "+line)
	}
	return result
}

// -- Navigation helpers --

func (m *model) currentGroup() updateKind {
	kinds := []updateKind{kindTarget, kindStack, kindPolicy, kindResource}
	idx := 0
	for _, k := range kinds {
		var count int
		for _, u := range m.updates {
			if u.Kind == k {
				count++
			}
		}
		if count == 0 {
			continue
		}
		vis := m.groupVisibleCount[k]
		shown := count
		hasMore := false
		if shown > vis {
			shown = vis
			hasMore = true
		}
		endIdx := idx + shown
		if hasMore {
			endIdx++
		}
		if m.cursor >= idx && m.cursor < endIdx {
			return k
		}
		idx = endIdx
	}
	return kindResource
}

func (m *model) isShowMoreRow(flatIdx int) bool {
	kinds := []updateKind{kindTarget, kindStack, kindPolicy, kindResource}
	idx := 0
	for _, k := range kinds {
		var count int
		for _, u := range m.updates {
			if u.Kind == k {
				count++
			}
		}
		if count == 0 {
			continue
		}
		vis := m.groupVisibleCount[k]
		shown := count
		hasMore := false
		if shown > vis {
			shown = vis
			hasMore = true
		}
		idx += shown
		if hasMore {
			if flatIdx == idx {
				return true
			}
			idx++
		}
	}
	return false
}

func (m *model) totalFlatRows() int {
	kinds := []updateKind{kindTarget, kindStack, kindPolicy, kindResource}
	idx := 0
	for _, k := range kinds {
		var count int
		for _, u := range m.updates {
			if u.Kind == k {
				count++
			}
		}
		if count == 0 {
			continue
		}
		vis := m.groupVisibleCount[k]
		shown := count
		if shown > vis {
			shown = vis
			idx++
		}
		idx += shown
	}
	return idx
}

func (m *model) summaryCounts() string {
	counts := map[opKind]int{}
	for _, u := range m.updates {
		counts[u.Operation]++
	}
	var parts []string
	for _, op := range []opKind{opCreate, opUpdate, opDelete, opReplace, opDetach, opKeep} {
		if n := counts[op]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s",
				lipgloss.NewStyle().Foreground(opColor(op, m.theme.Palette)).Bold(true).Render(opSymbol(op)),
				n, string(op)))
		}
	}
	return strings.Join(parts, "  ")
}

// -- Helpers --

func padRight(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

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

// -- Fake data --

func fakeSimulation() []simUpdate {
	updates := []simUpdate{
		// Targets
		{Kind: kindTarget, Operation: opCreate, Label: "aws-us-east-1"},
		{Kind: kindTarget, Operation: opUpdate, Label: "aws-eu-west-1"},
		{Kind: kindTarget, Operation: opReplace, Label: "aws-ap-south-1"},

		// Stacks
		{Kind: kindStack, Operation: opCreate, Label: "production"},

		// Policies
		{Kind: kindPolicy, Operation: opCreate, Label: "auto-reconcile", TypeName: "ttl", Stack: "production"},
		{Kind: kindPolicy, Operation: opUpdate, Label: "staging-reconcile", TypeName: "auto-reconcile", Stack: "staging",
			Patches: []patchEntry{
				{Action: "set", Path: "Interval", OldValue: `"5m"`, NewValue: `"10m"`},
				{Action: "set", Path: "Label", OldValue: `""`, NewValue: `"staging-reconcile"`},
			}},
		{Kind: kindPolicy, Operation: opDetach, Label: "old-policy", TypeName: "ttl", Stack: "staging"},
		{Kind: kindPolicy, Operation: opKeep, Label: "shared-policy", TypeName: "ttl", Stack: "shared",
			RefStacks: "production, development"},

		// Resources — hand-crafted
		{Kind: kindResource, Operation: opCreate, Label: "my-bucket", TypeName: "AWS::S3::Bucket", Stack: "production"},
		{Kind: kindResource, Operation: opCreate, Label: "web-1", TypeName: "AWS::EC2::Instance", Stack: "production"},
		{Kind: kindResource, Operation: opUpdate, Label: "primary", TypeName: "AWS::RDS::DBInstance", Stack: "production",
			Patches: []patchEntry{
				{Action: "set", Path: "InstanceClass", OldValue: `"db.t3.medium"`, NewValue: `"db.t3.large"`},
				{Action: "set", Path: "MultiAZ", OldValue: "false", NewValue: "true"},
				{Action: "add", Path: "Tags[backup]", NewValue: `"daily"`},
				{Action: "remove", Path: "Tags[temporary]"},
			}},
		{Kind: kindResource, Operation: opUpdate, Label: "web-config", TypeName: "AWS::SSM::Parameter", Stack: "production",
			Patches: []patchEntry{
				{Action: "set", Path: "Value", OldValue: `"v1.2.3"`, NewValue: `"v1.3.0"`},
			}},
		{Kind: kindResource, Operation: opReplace, Label: "web-server", TypeName: "AWS::EC2::Instance", Stack: "production"},
		{Kind: kindResource, Operation: opCreate, Label: "cdn", TypeName: "AWS::CloudFront::Distribution", Stack: "production"},
		{Kind: kindResource, Operation: opDelete, Label: "old-cache", TypeName: "AWS::ElastiCache::CacheCluster", Stack: "production"},
		{Kind: kindResource, Operation: opDelete, Label: "legacy-queue", TypeName: "AWS::SQS::Queue", Stack: "production",
			CascadeSrc: "old-cache"},
	}

	// Add more resources to test pagination
	rng := rand.New(rand.NewSource(42))
	types := []string{"AWS::EC2::Instance", "AWS::S3::Bucket", "AWS::Lambda::Function",
		"AWS::DynamoDB::Table", "AWS::SQS::Queue", "AWS::SNS::Topic"}
	ops := []opKind{opCreate, opCreate, opCreate, opUpdate, opDelete}
	for i := 0; i < 30; i++ {
		op := ops[rng.Intn(len(ops))]
		u := simUpdate{
			Kind:      kindResource,
			Operation: op,
			Label:     fmt.Sprintf("res-%d", i+10),
			TypeName:  types[rng.Intn(len(types))],
			Stack:     "production",
		}
		if op == opUpdate {
			u.Patches = []patchEntry{
				{Action: "set", Path: "Config", OldValue: `"old"`, NewValue: `"new"`},
			}
		}
		updates = append(updates, u)
	}

	return updates
}

func main() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
