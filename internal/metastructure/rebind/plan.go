// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package rebind implements the rename rebind phase described in RFC-0041.
//
// The phase is split into a read-only validation step that produces a
// RebindPlan and an execution step that applies the plan inside a single
// datastore transaction. This package owns both halves.
package rebind

// RebindPlan is the output of the validation step. Execution applies the
// identity-column rewrites it contains to the metastructure.
type RebindPlan struct {
	Stacks    []StackRebind
	Resources []ResourceRebind
	// Targets is intentionally absent in this PR. Target rename is
	// deferred to a follow-up RFC (RFC-0041 §Scope of this RFC).
}

// ResourceRebind is one identity-column rewrite of a managed resource.
// The row is identified by Ksuid (stable); identity columns get the
// new values.
type ResourceRebind struct {
	Ksuid     string
	NewLabel  string
	NewStack  string
	NewTarget string
	// Source records what produced this entry: "alias" for a direct
	// alias hit on the resource declaration, "stack-cascade" for a
	// cascade triggered by a renamed stack. (Target-cascade is not
	// used in this PR; reserved for the follow-up RFC.)
	Source string
}

// StackRebind is one identity-column rewrite of a stack.
type StackRebind struct {
	ID       string // stable Stack.ID, unchanged
	NewLabel string
}

// IsEmpty reports whether the plan has nothing to do.
func (p *RebindPlan) IsEmpty() bool {
	return p == nil || (len(p.Stacks) == 0 && len(p.Resources) == 0)
}
