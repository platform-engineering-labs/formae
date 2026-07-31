// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package inventoryview implements the data layer for the inventory TUI
// browser. It exposes the Client seam, tab specs, and fetch commands that the
// tab engine (later tasks) drives to populate tabbed views over resources,
// targets, stacks and policies.
package inventoryview

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
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
	// ListResourceSummaries returns lightweight resource summaries; detail is
	// fetched lazily by ksuid via ResourceDetailByKsuid.
	ListResourceSummaries(query string, fromTUI bool) ([]pkgmodel.ResourceSummary, []string, error)
	ResourceDetailByKsuid(ksuid string, fromTUI bool) (*pkgmodel.Resource, []string, error)
}

// Options configures an inventory TUI session.
type Options struct {
	FocusTab Tab
	Query    string // server-side; applies to FocusTab only (D3)
	Version  string // formae version shown in the header banner
	MaxRows  int    // display cap after filtering (D5/D8); 0 = unlimited
	Now      func() time.Time
}

// row is a render-ready table row with an optional detail expander.
// When detailKsuid is non-empty, the detail is fetched lazily by ksuid rather
// than computed synchronously via the detail closure. The two are mutually
// exclusive: summary rows (resources tab) set detailKsuid and leave detail nil;
// other tabs set detail and leave detailKsuid empty.
type row struct {
	cells       []string
	title       string // display title used in the detail screen header
	detail      func(width int) []string
	detailKsuid string // non-empty → lazy async detail fetch; empty → use detail closure
}

// tabSpec describes one inventory tab declaratively.
type tabSpec struct {
	title   string // "Resources"
	entity  string // "resources" — status bar noun
	columns []components.Column
	fetch   func(c Client, query string, fromTUI bool) ([]row, []string, error)
	// serverQuery is true when the tab's fetch honours a server-side query
	// (Resources, Targets). For those tabs the query bar drives a real server
	// query (re-fetched on enter, full syntax with wildcards) and no client-side
	// filter is applied. For serverQuery=false tabs (Stacks, Policies — whose
	// fetch takes no query) the query bar drives a client-side substring filter.
	serverQuery bool
	styleCell   func(th *theme.Theme, col int, cell string) string // nil = plain
}

// tabLoadedMsg is the bubbletea message delivered when a tab fetch completes.
type tabLoadedMsg struct {
	tab  Tab
	rows []row
	nags []string
	err  error
}

// resourceDetailLoadedMsg is the bubbletea message delivered when a lazy
// resource detail fetch completes. ksuid carries the resource the fetch was
// dispatched for, and seq carries the monotonic request generation so the
// stale-response guard can drop responses that are no longer the latest
// dispatch — including a second in-flight fetch for the same ksuid after an
// open -> close -> reopen of the same row.
type resourceDetailLoadedMsg struct {
	ksuid    string
	seq      uint64
	resource *pkgmodel.Resource
	err      error
}

// newSpecs returns the four tab specifications.
func newSpecs(now func() time.Time) [4]tabSpec {
	return [4]tabSpec{
		TabResources: {
			title:       "Resources",
			entity:      "resources",
			serverQuery: true,
			columns: []components.Column{
				{Title: "Label", Width: 20, Priority: 0},
				{Title: "Stack", Width: 14, Priority: 2},
				{Title: "Type", Width: 24, Priority: 1},
				{Title: "NativeID", Width: 28, Priority: 3},
			},
			// col 1 = Stack: cells starting with "⚠ " are rendered with the
			// themeable Unmanaged role (default red) to highlight unmanaged
			// resources, independent of the failure color.
			styleCell: func(th *theme.Theme, col int, cell string) string {
				if col == 1 && th != nil && strings.HasPrefix(cell, "⚠ ") {
					return th.Styles.Unmanaged.Render(cell)
				}
				return cell
			},
			fetch: func(c Client, query string, fromTUI bool) ([]row, []string, error) {
				summaries, nags, err := c.ListResourceSummaries(query, fromTUI)
				if err != nil {
					return nil, nags, err
				}
				rows := make([]row, 0, len(summaries))
				for _, s := range summaries {
					rows = append(rows, resourceSummaryRow(s))
				}
				return rows, nags, nil
			},
		},
		TabTargets: {
			title:       "Targets",
			entity:      "targets",
			serverQuery: true,
			columns: []components.Column{
				{Title: "Label", Width: 20, Priority: 0},
				{Title: "Namespace", Width: 20, Priority: 1},
				{Title: "Discoverable", Width: 14, Priority: 2},
				{Title: "Config", Width: 40, Priority: 3},
			},
			// col 2 = Discoverable: "yes" gets the blue accent role.
			styleCell: func(th *theme.Theme, col int, cell string) string {
				if col == 2 && th != nil && cell == "yes" {
					return th.Styles.Accent.Render(cell)
				}
				return cell
			},
			fetch: func(c Client, query string, fromTUI bool) ([]row, []string, error) {
				targets, nags, err := c.ExtractTargets(query, fromTUI)
				if err != nil {
					return nil, nags, err
				}
				rows := make([]row, 0, len(targets))
				for _, t := range targets {
					rows = append(rows, targetRow(t))
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
				clockNow := time.Now()
				if now != nil {
					clockNow = now()
				}
				rows := make([]row, 0, len(stacks))
				for _, s := range stacks {
					rows = append(rows, stackRow(s, clockNow))
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
					rows = append(rows, policyRow(p))
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

// fetchResourceDetailCmd returns a bubbletea Cmd that fetches the full resource
// detail for the given ksuid and delivers a resourceDetailLoadedMsg. The ksuid
// and seq are carried back in the msg so the stale-response guard can accept
// only the latest dispatch for the currently-open row.
func fetchResourceDetailCmd(c Client, ksuid string, seq uint64) tea.Cmd {
	return func() tea.Msg {
		resource, _, err := c.ResourceDetailByKsuid(ksuid, true)
		return resourceDetailLoadedMsg{ksuid: ksuid, seq: seq, resource: resource, err: err}
	}
}
