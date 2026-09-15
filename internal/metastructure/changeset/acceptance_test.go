// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestAcceptanceContributionsNeverEnterProviderDAG(t *testing.T) {
	updates := []resource_update.ResourceUpdate{
		{Operation: resource_update.OperationAccept, DesiredState: pkgmodel.Resource{Ksuid: "accepted", Label: "accepted"}},
		{Operation: resource_update.OperationWithdraw, DesiredState: pkgmodel.Resource{Ksuid: "withdrawn", Label: "withdrawn"}},
		{Operation: resource_update.OperationAcceptDelete, DesiredState: pkgmodel.Resource{Ksuid: "absent", Label: "absent"}},
	}
	cs, err := NewChangeset(updates, nil, nil, "command", pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.DAG.Nodes) != 0 {
		t.Fatalf("acceptance scheduled %d provider nodes", len(cs.DAG.Nodes))
	}
	if len(updates) != 3 || updates[0].Operation != resource_update.OperationAccept {
		t.Fatal("builder mutated command contributions")
	}
}
