// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package rebind

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestRebinder_Run_NoAliases_IsNoOp(t *testing.T) {
	ds := &stubDatastore{}
	r := NewRebinder(ds)

	forma := pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "prod"}},
		Targets:   []pkgmodel.Target{{Label: "aws-prod"}},
		Resources: []pkgmodel.Resource{{Label: "web-server", Stack: "prod", Target: "aws-prod"}},
	}

	if err := r.Run(context.Background(), forma, "cmd-1"); err != nil {
		t.Fatalf("Run with no aliases should be a no-op, got error: %v", err)
	}

	if ds.withTxCalls != 0 {
		t.Fatalf("expected WithTx to NOT be called when plan is empty, got %d calls", ds.withTxCalls)
	}
}

func TestRebinder_Validate_NoAliases_ReturnsEmptyPlan(t *testing.T) {
	ds := &stubDatastore{}
	r := NewRebinder(ds)

	forma := pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: "prod"}},
		Resources: []pkgmodel.Resource{{Label: "web-server", Stack: "prod"}},
	}

	plan, err := r.Validate(context.Background(), forma)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !plan.IsEmpty() {
		t.Fatalf("expected empty plan, got %+v", plan)
	}
}

// stubDatastore embeds the Datastore interface to satisfy the compiler;
// only WithTx is overridden because that is the only method exercised
// by the rebinder under test. Any other interface method invocation
// would panic on the nil embedded interface, which is the desired test
// behaviour ("you broke encapsulation").
type stubDatastore struct {
	datastore.Datastore
	withTxCalls int
}

func (s *stubDatastore) WithTx(_ context.Context, fn func(datastore.Tx) error) error {
	s.withTxCalls++
	return fn(s)
}
