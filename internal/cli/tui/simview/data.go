// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package simview provides the data layer for the simulation-preview TUI
// shown before apply/destroy confirmation. It maps an apimodel.Command into
// render-ready grouped rows — no bubbletea or rendering concerns here.
package simview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// opKind encodes the operation type for a row. The iota values define the
// default destructive-first sort order: lower values sort first.
type opKind int

const (
	opDelete  opKind = iota // "-"
	opReplace               // "↻"
	opUpdate                // "~"
	opDetach                // "⊘"
	opCreate                // "+"
	opKeep                  // "="
)

// opGlyph returns the themed glyph for an operation.
func opGlyph(g theme.Glyphs, o opKind) string {
	switch o {
	case opDelete:
		return g.OpDelete
	case opReplace:
		return g.OpReplace
	case opUpdate:
		return g.OpUpdate
	case opDetach:
		return g.OpDetach
	case opCreate:
		return g.OpCreate
	case opKeep:
		return g.OpKeep
	}
	return "?"
}

// opColor returns the themed color for an operation.
func opColor(p theme.Palette, o opKind) lipgloss.AdaptiveColor {
	switch o {
	case opDelete:
		return p.OpDelete
	case opReplace:
		return p.OpReplace
	case opUpdate:
		return p.OpUpdate
	case opDetach:
		return p.OpDetach
	case opCreate:
		return p.OpCreate
	case opKeep:
		return p.OpKeep
	}
	return p.TextPrimary
}

// word returns the human-readable operation name.
func (o opKind) word() string {
	switch o {
	case opDelete:
		return "delete"
	case opReplace:
		return "replace"
	case opUpdate:
		return "update"
	case opDetach:
		return "detach"
	case opCreate:
		return "create"
	case opKeep:
		return "keep"
	}
	return "unknown"
}

// rowKind identifies the kind of entity a row represents.
type rowKind int

const (
	kindTarget rowKind = iota
	kindStack
	kindPolicy
	kindResource
)

// simRow is a render-ready row for the simulation preview table.
type simRow struct {
	key               string // "<kind>/<stack>/<label>" — stable identity (PLA-282)
	op                opKind
	label, typ, stack string
	detail            string // target "to discoverable" / replace reason / policy referencing-stacks note
	cascade           bool
	cascadeSrc        string
	res               *apimodel.ResourceUpdate // for resource rows: card data (replace: CREATE half)
	delRes            *apimodel.ResourceUpdate // for replace rows: the DELETE half of the GroupID pair
	policy            *apimodel.PolicyUpdate
}

// simGroup is a named group of rows sharing the same rowKind.
type simGroup struct {
	kind  rowKind
	title string // "Targets", "Stacks", "Policies", "Resources"
	rows  []simRow
}

// buildSimGroups maps an apimodel.Command into render-ready grouped rows.
// Groups are ordered: targets, stacks, policies, resources. Empty groups are
// omitted. Within each group rows are sorted destructive-first (opKind order).
func buildSimGroups(cmd *apimodel.Command) []simGroup {
	targetRows := buildTargetRows(cmd.TargetUpdates)
	stackRows := buildStackRows(cmd.StackUpdates)
	policyRows := buildPolicyRows(cmd.PolicyUpdates)
	resourceRows := buildResourceRows(cmd.ResourceUpdates)

	// Sort each group destructive-first.
	sortByOpKind(targetRows)
	sortByOpKind(stackRows)
	sortByOpKind(policyRows)
	sortByOpKind(resourceRows)

	// Build groups in fixed order, omitting empty ones.
	var groups []simGroup
	if len(targetRows) > 0 {
		groups = append(groups, simGroup{kind: kindTarget, title: "Targets", rows: targetRows})
	}
	if len(stackRows) > 0 {
		groups = append(groups, simGroup{kind: kindStack, title: "Stacks", rows: stackRows})
	}
	if len(policyRows) > 0 {
		groups = append(groups, simGroup{kind: kindPolicy, title: "Policies", rows: policyRows})
	}
	if len(resourceRows) > 0 {
		groups = append(groups, simGroup{kind: kindResource, title: "Resources", rows: resourceRows})
	}
	return groups
}

// buildTargetRows converts TargetUpdates into simRows.
// Operation vocabulary (mirrored from formatSimulatedTargetUpdate, renderer.go:673-712):
//
//	"create"  → opCreate
//	"update"  → opUpdate  (detail: "to discoverable" / "to not discoverable" when no config diff)
//	"replace" → opReplace
//	"delete"  → opDelete
//	anything else → opCreate (fallback; renderer uses coloredOperation which just colors the string)
func buildTargetRows(updates []apimodel.TargetUpdate) []simRow {
	rows := make([]simRow, 0, len(updates))
	for i := range updates {
		tu := &updates[i]
		op := targetOpKind(tu)
		detail := targetDetail(tu)
		row := simRow{
			key:    fmt.Sprintf("target/%s", tu.TargetLabel),
			op:     op,
			label:  tu.TargetLabel,
			stack:  "", // targets have no stack
			detail: detail,
		}
		rows = append(rows, row)
	}
	return rows
}

// targetOpKind maps a TargetUpdate operation string to opKind.
func targetOpKind(tu *apimodel.TargetUpdate) opKind {
	switch tu.Operation {
	case "create":
		return opCreate
	case "update":
		return opUpdate
	case "replace":
		return opReplace
	case "delete":
		return opDelete
	default:
		return opCreate
	}
}

// targetDetail returns the detail string for a target update.
// Mirrors the renderer: for "update" with no config diffs, show discoverable transition.
func targetDetail(tu *apimodel.TargetUpdate) string {
	if tu.Operation != "update" && tu.Operation != "replace" {
		return ""
	}
	// Check whether there are config diffs (same logic as diffConfigs in renderer).
	if hasConfigDiffs(tu.ExistingConfig, tu.DesiredConfig) {
		return ""
	}
	if tu.Operation == "update" {
		if tu.Discoverable {
			return "to discoverable"
		}
		return "to not discoverable"
	}
	return ""
}

// hasConfigDiffs returns true when existing and desired configs differ at the
// JSON key level. Mirrors the renderer's diffConfigs behaviour: if either
// side is absent, or if the JSON objects share all keys with identical values,
// there are no diffs.
func hasConfigDiffs(existing, desired json.RawMessage) bool {
	if len(existing) == 0 || len(desired) == 0 {
		return false
	}
	var e, d map[string]json.RawMessage
	if json.Unmarshal(existing, &e) != nil || json.Unmarshal(desired, &d) != nil {
		return false
	}
	// Any key that exists in one but not the other, or has differing values.
	allKeys := map[string]struct{}{}
	for k := range e {
		allKeys[k] = struct{}{}
	}
	for k := range d {
		allKeys[k] = struct{}{}
	}
	for k := range allKeys {
		ev, eok := e[k]
		dv, dok := d[k]
		if eok != dok {
			return true
		}
		if string(ev) != string(dv) {
			return true
		}
	}
	return false
}

// buildStackRows converts StackUpdates into simRows.
// Operation vocabulary (mirrored from formatSimulatedStackUpdate, renderer.go:754-761):
//
//	"create"  → opCreate
//	"update"  → opUpdate
//	"replace" → opReplace
//	"delete"  → opDelete
func buildStackRows(updates []apimodel.StackUpdate) []simRow {
	rows := make([]simRow, 0, len(updates))
	for i := range updates {
		su := &updates[i]
		op := genericOpKind(su.Operation)
		row := simRow{
			key:   fmt.Sprintf("stack/%s", su.StackLabel),
			op:    op,
			label: su.StackLabel,
		}
		rows = append(rows, row)
	}
	return rows
}

// buildPolicyRows converts PolicyUpdates into simRows.
// Operation vocabulary (mirrored from formatSimulatedPolicyUpdate, renderer.go:783-808):
//
//	"detach" → opDetach
//	"skip"   → opKeep  (renderer displays "not delete")
//	"create" → opCreate
//	"update" → opUpdate
//	"delete" → opDelete
//	"replace"→ opReplace
func buildPolicyRows(updates []apimodel.PolicyUpdate) []simRow {
	rows := make([]simRow, 0, len(updates))
	for i := range updates {
		pu := &updates[i]
		op, detail := policyOpAndDetail(pu)
		row := simRow{
			key:    fmt.Sprintf("policy/%s/%s", pu.StackLabel, pu.PolicyLabel),
			op:     op,
			label:  pu.PolicyLabel,
			typ:    pu.PolicyType,
			stack:  pu.StackLabel,
			detail: detail,
			policy: pu,
		}
		rows = append(rows, row)
	}
	return rows
}

// policyOpAndDetail maps a PolicyUpdate to its opKind and optional detail.
func policyOpAndDetail(pu *apimodel.PolicyUpdate) (opKind, string) {
	switch pu.Operation {
	case "detach":
		return opDetach, ""
	case "skip":
		// Renderer: "not delete"; we map to opKeep.
		// Detail: "still referenced by: <stacks>" when ReferencingStacks is non-empty.
		detail := ""
		if len(pu.ReferencingStacks) > 0 {
			detail = "still referenced by: " + strings.Join(pu.ReferencingStacks, ", ")
		}
		return opKeep, detail
	default:
		return genericOpKind(pu.Operation), ""
	}
}

// buildResourceRows converts ResourceUpdates into simRows, pairing delete+create
// halves that share a GroupID into a single opReplace row. Read operations are
// excluded defensively.
func buildResourceRows(updates []apimodel.ResourceUpdate) []simRow {
	// Partition into grouped (have GroupID) and singleton updates.
	type groupEntry struct {
		del *apimodel.ResourceUpdate
		cre *apimodel.ResourceUpdate
	}
	grouped := map[string]*groupEntry{}

	var singletons []*apimodel.ResourceUpdate

	for i := range updates {
		ru := &updates[i]
		// Exclude read operations defensively.
		if ru.Operation == apimodel.OperationRead {
			continue
		}
		if ru.GroupID == "" {
			singletons = append(singletons, ru)
			continue
		}
		entry, ok := grouped[ru.GroupID]
		if !ok {
			entry = &groupEntry{}
			grouped[ru.GroupID] = entry
		}
		switch ru.Operation {
		case apimodel.OperationDelete:
			entry.del = ru
		case apimodel.OperationCreate:
			entry.cre = ru
		default:
			// Unexpected grouped operation — treat as singleton.
			singletons = append(singletons, ru)
		}
	}

	var rows []simRow

	// Emit replace rows for complete pairs (both halves present).
	// Process in a stable order (sort by GroupID).
	groupIDs := make([]string, 0, len(grouped))
	for gid := range grouped {
		groupIDs = append(groupIDs, gid)
	}
	sort.Strings(groupIDs)

	for _, gid := range groupIDs {
		entry := grouped[gid]
		if entry.del != nil && entry.cre != nil {
			// Label/type/stack come from the CREATE half.
			cre := entry.cre
			del := entry.del
			row := simRow{
				key:    fmt.Sprintf("resource/%s/%s", cre.StackName, cre.ResourceLabel),
				op:     opReplace,
				label:  cre.ResourceLabel,
				typ:    cre.ResourceType,
				stack:  cre.StackName,
				res:    cre,
				delRes: del,
			}
			rows = append(rows, row)
		} else {
			// Incomplete pair — treat whichever half we have as a singleton.
			if entry.del != nil {
				singletons = append(singletons, entry.del)
			}
			if entry.cre != nil {
				singletons = append(singletons, entry.cre)
			}
		}
	}

	// Emit singleton rows.
	for _, ru := range singletons {
		op := genericOpKind(ru.Operation)
		row := simRow{
			key:        fmt.Sprintf("resource/%s/%s", ru.StackName, ru.ResourceLabel),
			op:         op,
			label:      ru.ResourceLabel,
			typ:        ru.ResourceType,
			stack:      ru.StackName,
			cascade:    ru.IsCascade,
			cascadeSrc: ru.CascadeSource,
			res:        ru,
		}
		rows = append(rows, row)
	}

	return rows
}

// genericOpKind converts a plain operation string to an opKind.
// Mirrors coloredOperation (renderer.go:581-594) — only the four resource
// operation strings are expected; anything else falls through to opCreate.
func genericOpKind(op string) opKind {
	switch op {
	case apimodel.OperationDelete:
		return opDelete
	case apimodel.OperationReplace:
		return opReplace
	case apimodel.OperationUpdate:
		return opUpdate
	case apimodel.OperationCreate:
		return opCreate
	}
	return opCreate
}

// sortByOpKind sorts rows by opKind (destructive-first). Stable sort preserves
// the original relative order of rows with equal opKind.
func sortByOpKind(rows []simRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].op < rows[j].op
	})
}

// opCounts returns the count of rows for each opKind across all groups.
// Used for the header summary and confirmation line.
func opCounts(groups []simGroup) map[opKind]int {
	counts := make(map[opKind]int)
	for _, g := range groups {
		for _, r := range g.rows {
			counts[r.op]++
		}
	}
	return counts
}

// sortRows sorts rows in-place by a chosen column and direction.
// Column mapping:
//
//	0 = label, 1 = op (word), 2 = type, 3 = stack
//
// dir == SortNone applies the default destructive-first sort (by opKind iota).
func sortRows(rows []simRow, col int, dir components.SortDirection) {
	if dir == components.SortNone {
		sortByOpKind(rows)
		return
	}
	asc := dir == components.SortAsc
	sort.SliceStable(rows, func(i, j int) bool {
		var a, b string
		switch col {
		case 1:
			a, b = rows[i].op.word(), rows[j].op.word()
		case 2:
			a, b = rows[i].typ, rows[j].typ
		case 3:
			a, b = rows[i].stack, rows[j].stack
		default: // 0 and anything else
			a, b = rows[i].label, rows[j].label
		}
		if asc {
			return a < b
		}
		return a > b
	})
}
