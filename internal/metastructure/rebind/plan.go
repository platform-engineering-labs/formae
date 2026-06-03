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
//
// Each kind of write has its own concrete type so the executor cannot
// accidentally mix scopes. In particular, ResourceRebind only carries
// a new label: moving a resource between stacks or targets via a
// per-resource alias is out of scope for this RFC. Cross-stack moves
// are only possible through a StackRebind + its cascade.
type RebindPlan struct {
	Stacks        []StackRebind
	Resources     []ResourceRebind
	StackCascades []ResourceStackCascade
	// Targets and TargetCascades are intentionally absent. Target rename
	// is deferred to a follow-up RFC (RFC-0041 §Scope of this RFC).
}

// ResourceRebind is a resource label rename. Only `label` is mutable
// here; moving a resource across stacks or targets is out of scope
// for this RFC.
type ResourceRebind struct {
	Ksuid    string // stable identity, unchanged
	NewLabel string
}

// ResourceStackCascade is the per-child write produced by a stack
// rename. It only touches resources.stack; the resource label and
// target stay put.
type ResourceStackCascade struct {
	Ksuid    string // resource being cascaded
	NewStack string // new parent stack label
}

// StackRebind is one identity-column rewrite of a stack.
type StackRebind struct {
	ID       string // stable Stack.ID, unchanged
	NewLabel string
}

// IsEmpty reports whether the plan has nothing to do.
func (p *RebindPlan) IsEmpty() bool {
	return p == nil ||
		(len(p.Stacks) == 0 && len(p.Resources) == 0 && len(p.StackCascades) == 0)
}
