// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package rebind

import (
	"context"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Rebinder runs the alias rebind phase of an apply (see RFC-0041).
//
// PR 1 wires the skeleton into the apply pipeline as a no-op when no
// aliases are declared. Resource and stack rename logic land in
// follow-up PRs (RFC-0041 §Implementation plan).
type Rebinder struct {
	ds datastore.Datastore
}

// NewRebinder constructs a Rebinder backed by the given datastore.
func NewRebinder(ds datastore.Datastore) *Rebinder {
	return &Rebinder{ds: ds}
}

// Run validates the forma's aliases, then executes the resulting plan.
// Callers must hold the apply-level commandMu lock to ensure validation
// and execution observe a consistent metastructure snapshot.
func (r *Rebinder) Run(ctx context.Context, forma pkgmodel.Forma, commandID string) error {
	plan, err := r.Validate(ctx, forma)
	if err != nil {
		return err
	}
	if plan.IsEmpty() {
		return nil
	}
	return r.Execute(ctx, plan, commandID)
}

// Validate walks the forma, consults declared aliases, and returns a
// RebindPlan or an error describing the first conflict.
//
// PR 1: always returns an empty plan. Real validation logic lands in
// PR 2 (resource rename) and PR 7 (stack rename).
func (r *Rebinder) Validate(ctx context.Context, forma pkgmodel.Forma) (*RebindPlan, error) {
	_ = ctx
	_ = forma
	return &RebindPlan{}, nil
}

// Execute applies the plan inside a single datastore transaction.
//
// PR 1: empty body; only invoked when the plan is non-empty, which is
// impossible while Validate is a no-op. Reserved for PR 2.
func (r *Rebinder) Execute(ctx context.Context, plan *RebindPlan, commandID string) error {
	_ = commandID
	return r.ds.WithTx(ctx, func(_ datastore.Tx) error {
		_ = plan
		return nil
	})
}
