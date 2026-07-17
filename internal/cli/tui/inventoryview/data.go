// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package inventoryview implements the data layer for the inventory TUI browser
// (PLA-281). It exposes the Client seam, tab specs, and fetch commands that the
// tab engine (later tasks) drives to populate tabbed views over resources,
// targets, stacks and policies.
package inventoryview

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Tab identifies one of the four inventory entity tabs.
type Tab int

const (
	TabResources Tab = iota
	TabTargets
	TabStacks
	TabPolicies
)

// Client is the data-fetching seam consumed by inventoryview. *app.App satisfies
// this interface directly — no adapter required.
type Client interface {
	ExtractResources(query string, fromTUI bool) (*pkgmodel.Forma, []string, error)
	ExtractTargets(query string, fromTUI bool) ([]*pkgmodel.Target, []string, error)
	ExtractStacks(fromTUI bool) ([]*pkgmodel.Stack, []string, error)
	ExtractPolicies(fromTUI bool) ([]apimodel.PolicyInventoryItem, []string, error)
}

// Options configures an inventory TUI session.
type Options struct {
	FocusTab Tab
	Query    string // server-side; applies to FocusTab only (D3)
	MaxRows  int    // display cap after filtering (D5/D8); 0 = unlimited
	Now      func() time.Time
}

// row is a render-ready table row with an optional detail expander.
type row struct {
	cells  []string
	detail func(width int) []string
}

// tabSpec describes one inventory tab declaratively.
type tabSpec struct {
	title   string // "Resources"
	entity  string // "resources" — status bar noun
	columns []components.Column
	fetch   func(c Client, query string, fromTUI bool) ([]row, []string, error)
}

// tabLoadedMsg is the bubbletea message delivered when a tab fetch completes.
type tabLoadedMsg struct {
	tab  Tab
	rows []row
	nags []string
	err  error
}

// newSpecs returns the four tab specifications. Specs for tabs not yet fully
// built return placeholder cell sets; Tasks 5-6 replace the cell builders.
func newSpecs(_ func() time.Time) [4]tabSpec {
	return [4]tabSpec{
		TabResources: {
			title:  "Resources",
			entity: "resources",
			columns: []components.Column{
				{Title: "NativeID", Width: 28, Priority: 3},
				{Title: "Stack", Width: 14, Priority: 2},
				{Title: "Type", Width: 24, Priority: 1},
				{Title: "Label", Width: 20, Priority: 0},
			},
			fetch: func(c Client, query string, fromTUI bool) ([]row, []string, error) {
				forma, nags, err := c.ExtractResources(query, fromTUI)
				if err != nil {
					return nil, nags, err
				}
				if forma == nil {
					return nil, nags, nil
				}
				rows := make([]row, 0, len(forma.Resources))
				for i := range forma.Resources {
					r := &forma.Resources[i]
					rows = append(rows, row{
						cells: []string{r.NativeID, r.Stack, r.Type, r.Label},
					})
				}
				return rows, nags, nil
			},
		},
		TabTargets: {
			title:  "Targets",
			entity: "targets",
			columns: []components.Column{
				{Title: "Label", Width: 20, Priority: 0},
				{Title: "Namespace", Width: 20, Priority: 1},
				{Title: "Discoverable", Width: 14, Priority: 2},
				{Title: "Config", Width: 40, Priority: 3},
			},
			fetch: func(c Client, query string, fromTUI bool) ([]row, []string, error) {
				targets, nags, err := c.ExtractTargets(query, fromTUI)
				if err != nil {
					return nil, nags, err
				}
				rows := make([]row, 0, len(targets))
				for _, t := range targets {
					rows = append(rows, row{
						cells: []string{t.Label, t.Namespace, "", ""},
					})
				}
				return rows, nags, nil
			},
		},
		TabStacks: {
			title:  "Stacks",
			entity: "stacks",
			columns: []components.Column{
				{Title: "Label", Width: 20, Priority: 0},
				{Title: "Description", Width: 40, Priority: 1},
				{Title: "Policies", Width: 30, Priority: 2},
			},
			fetch: func(c Client, _ string, fromTUI bool) ([]row, []string, error) {
				stacks, nags, err := c.ExtractStacks(fromTUI)
				if err != nil {
					return nil, nags, err
				}
				rows := make([]row, 0, len(stacks))
				for _, s := range stacks {
					rows = append(rows, row{
						cells: []string{s.Label, s.Description, ""},
					})
				}
				return rows, nags, nil
			},
		},
		TabPolicies: {
			title:  "Policies",
			entity: "policies",
			columns: []components.Column{
				{Title: "Label", Width: 20, Priority: 0},
				{Title: "Type", Width: 20, Priority: 1},
				{Title: "Config", Width: 40, Priority: 3},
				{Title: "AttachedStacks", Width: 30, Priority: 2},
			},
			fetch: func(c Client, _ string, fromTUI bool) ([]row, []string, error) {
				policies, nags, err := c.ExtractPolicies(fromTUI)
				if err != nil {
					return nil, nags, err
				}
				rows := make([]row, 0, len(policies))
				for _, p := range policies {
					rows = append(rows, row{
						cells: []string{p.Label, p.Type, "", ""},
					})
				}
				return rows, nags, nil
			},
		},
	}
}

// fetchCmd returns a bubbletea Cmd that calls the spec's fetch function and
// delivers a tabLoadedMsg.
func fetchCmd(c Client, specs [4]tabSpec, tab Tab, query string, fromTUI bool) tea.Cmd {
	return func() tea.Msg {
		spec := specs[tab]
		rows, nags, err := spec.fetch(c, query, fromTUI)
		return tabLoadedMsg{tab: tab, rows: rows, nags: nags, err: err}
	}
}
