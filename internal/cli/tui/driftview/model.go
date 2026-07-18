// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package driftview implements the interactive reconcile-rejected drift view:
// a multi-select list of out-of-band changes grouped by stack, with an
// extract-as-code file prompt and a revert-all confirmation screen.
package driftview

import (
	"sort"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// ResourceRef identifies one drifted resource carried on a DecisionExtract.
type ResourceRef struct{ Stack, Type, Label, Operation string }

// Decision records the user's final choice when the program quits.
type Decision interface{ isDecision() }

// DecisionAbort means the user quit without acting on the drift.
type DecisionAbort struct{}

func (DecisionAbort) isDecision() {}

// DecisionRevertAll means the user confirmed reverting all out-of-band
// changes by re-applying with --force.
type DecisionRevertAll struct{}

func (DecisionRevertAll) isDecision() {}

// DecisionExtract means the user chose to extract the selected resources as
// code to Path.
type DecisionExtract struct {
	Path     string
	Selected []ResourceRef
}

func (DecisionExtract) isDecision() {}

// Options configures the drift view.
type Options struct {
	// Notice is an optional one-line warning rendered under the header
	// (e.g. "Drift changed while you were reviewing.").
	Notice string
	// SimulateOnly suppresses the revert-all action (a mutating cloud
	// operation) when the parent apply is running in --simulate mode.
	// Extract-as-code remains available because it only writes local files.
	SimulateOnly bool
}

// screenKind identifies which screen is currently displayed.
type screenKind int

const (
	screenList screenKind = iota
	screenFilePrompt
	screenRevertConfirm
)

// rowClass classifies a drifted resource by how it should be presented.
type rowClass int

const (
	rowClassUpdate rowClass = iota // out-of-band property change — selectable, expandable
	rowClassCreate rowClass = iota // resource appeared out-of-band — selectable
	rowClassDelete rowClass = iota // resource deleted out-of-band — informational only
)

// driftRow is one drifted resource in the list.
type driftRow struct {
	key   string // "stack/label"
	mod   apimodel.ResourceModification
	class rowClass
}

// selectable reports whether the row responds to selection keys.
func (r driftRow) selectable() bool { return r.class != rowClassDelete }

// classWord returns the operation word shown for the row.
func (r driftRow) classWord() string {
	switch r.class {
	case rowClassCreate:
		return "create"
	case rowClassDelete:
		return "delete"
	default:
		return "update"
	}
}

// driftStack groups rows under their stack name.
type driftStack struct {
	name string
	rows []driftRow
}

// Model is the bubbletea model for the drift view.
type Model struct {
	th        *theme.Theme
	rejected  *apimodel.FormaReconcileRejectedError
	opts      Options
	stacks    []driftStack
	cursor    int
	selected  map[string]bool
	expanded  map[string]bool
	screen    screenKind
	decision  Decision
	fileInput string
	showHelp  bool
	vp        viewport.Model
	width     int
	height    int
}

// New builds a drift view Model from a reconcile rejection. Stacks are
// ordered alphabetically; rows keep the order of ModifiedResources. All
// selectable rows (updates and creates) start selected.
func New(th *theme.Theme, rejected *apimodel.FormaReconcileRejectedError, opts Options) Model {
	names := make([]string, 0, len(rejected.ModifiedStacks))
	for name := range rejected.ModifiedStacks {
		names = append(names, name)
	}
	sort.Strings(names)

	stacks := make([]driftStack, 0, len(names))
	selected := map[string]bool{}
	for _, name := range names {
		st := driftStack{name: name}
		for _, mod := range rejected.ModifiedStacks[name].ModifiedResources {
			row := driftRow{
				key:   mod.Stack + "/" + mod.Label,
				mod:   mod,
				class: classifyRow(mod),
			}
			if row.selectable() {
				selected[row.key] = true
			}
			st.rows = append(st.rows, row)
		}
		stacks = append(stacks, st)
	}

	return Model{
		th:        th,
		rejected:  rejected,
		opts:      opts,
		stacks:    stacks,
		selected:  selected,
		expanded:  map[string]bool{},
		screen:    screenList,
		decision:  DecisionAbort{},
		fileInput: "./extracted-drift.pkl",
		vp:        viewport.New(80, 20),
	}
}

// classifyRow maps a ResourceModification to its presentation class.
// Deletes are explicit; a modification without previous properties means the
// resource appeared out-of-band (create semantic); everything else is an
// in-place update.
func classifyRow(mod apimodel.ResourceModification) rowClass {
	if mod.Operation == "delete" {
		return rowClassDelete
	}
	if len(mod.OldProperties) == 0 {
		return rowClassCreate
	}
	return rowClassUpdate
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Decision returns the user's final decision.
func (m Model) Decision() Decision { return m.decision }

// navigableRows returns the flat cursor-order list of all rows.
func (m Model) navigableRows() []driftRow {
	var rows []driftRow
	for _, s := range m.stacks {
		rows = append(rows, s.rows...)
	}
	return rows
}

// selectedRefs returns refs for all currently selected rows in list order.
func (m Model) selectedRefs() []ResourceRef {
	var refs []ResourceRef
	for _, r := range m.navigableRows() {
		if m.selected[r.key] {
			refs = append(refs, ResourceRef{
				Stack:     r.mod.Stack,
				Type:      r.mod.Type,
				Label:     r.mod.Label,
				Operation: r.mod.Operation,
			})
		}
	}
	return refs
}

// selectedCount returns the number of selected rows.
func (m Model) selectedCount() int {
	n := 0
	for _, r := range m.navigableRows() {
		if m.selected[r.key] {
			n++
		}
	}
	return n
}

// chromeLines is the fixed line count around the list viewport:
// header (2) + optional notice (1) + intro (4) + footer (3).
func (m Model) chromeLines() int {
	c := 9
	if m.opts.Notice != "" {
		c++
	}
	return c
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := max(m.height-m.chromeLines(), 1)
		m.vp = viewport.New(m.width, vpH)
		return m, nil

	case tea.KeyMsg:
		switch m.screen {
		case screenFilePrompt:
			return m.updateFilePrompt(msg)
		case screenRevertConfirm:
			return m.updateRevertConfirm(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

// updateList handles keys on the main list screen.
func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.navigableRows()

	// ctrl+c is always an escape hatch — quit even while help overlay is open.
	if msg.Type == tea.KeyCtrlC {
		m.decision = DecisionAbort{}
		return m, tea.Quit
	}

	// Help overlay is modal: only ? (toggle) and esc (close) act; all other keys
	// are swallowed so they do not act on the underlying list.
	if m.showHelp {
		switch {
		case msg.Type == tea.KeyEsc:
			m.showHelp = false
		case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == '?':
			m.showHelp = false
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		m.decision = DecisionAbort{}
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}

	case " ":
		if m.cursor >= 0 && m.cursor < len(rows) {
			r := rows[m.cursor]
			if r.selectable() {
				if m.selected[r.key] {
					delete(m.selected, r.key)
				} else {
					m.selected[r.key] = true
				}
			}
		}

	case "enter":
		if m.cursor >= 0 && m.cursor < len(rows) {
			r := rows[m.cursor]
			if r.class == rowClassUpdate {
				m.expanded[r.key] = !m.expanded[r.key]
			}
		}

	case "a":
		for _, r := range rows {
			if r.selectable() {
				m.selected[r.key] = true
			}
		}

	case "n":
		for _, r := range rows {
			delete(m.selected, r.key)
		}

	case "e":
		if m.selectedCount() > 0 {
			m.screen = screenFilePrompt
		}

	case "r":
		if !m.opts.SimulateOnly {
			m.screen = screenRevertConfirm
		}

	case "?":
		m.showHelp = !m.showHelp
	}

	return m, nil
}

// updateFilePrompt handles keys on the extract file prompt screen.
func (m Model) updateFilePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList

	case "ctrl+c":
		m.decision = DecisionAbort{}
		return m, tea.Quit

	case "enter":
		m.decision = DecisionExtract{Path: m.fileInput, Selected: m.selectedRefs()}
		return m, tea.Quit

	case "backspace":
		if runes := []rune(m.fileInput); len(runes) > 0 {
			m.fileInput = string(runes[:len(runes)-1])
		}

	default:
		if msg.Type == tea.KeyRunes {
			m.fileInput += string(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			m.fileInput += " "
		}
	}
	return m, nil
}

// updateRevertConfirm handles keys on the revert confirmation screen.
func (m Model) updateRevertConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.decision = DecisionRevertAll{}
		return m, tea.Quit

	case "n", "esc":
		m.screen = screenList

	case "ctrl+c":
		m.decision = DecisionAbort{}
		return m, tea.Quit
	}
	return m, nil
}
