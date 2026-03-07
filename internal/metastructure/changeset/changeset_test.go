// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestChangeset_ExecutionOrder_DeleteChainThenCreateChainWithParallelLeaves(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationDelete,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-2",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet2KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(resourceUpdates, "test-command-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	// Print initial DAG state

	// Step 1: Get first executable updates - should be subnet-1 delete (has dependency)
	require.Equal(t, map[string]int{"AWS": 1}, changeset.AvailableExecutableUpdates())
	executableUpdates := changeset.GetExecutableUpdates("AWS", 2)

	// Verify first step: should be subnet-1 delete
	if len(executableUpdates) != 1 {
		t.Fatalf("Expected 1 executable update, got %d", len(executableUpdates))
	}
	if executableUpdates[0].DesiredState.Label != "test-subnet-1" || executableUpdates[0].Operation != resource_update.OperationDelete {
		t.Fatalf("Expected subnet-1 delete, got %s %s", executableUpdates[0].DesiredState.Label, executableUpdates[0].Operation)
	}

	// Step 2: Complete subnet-1 delete
	executableUpdates[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(executableUpdates[0])
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// Verify second step: should be VPC delete
	if len(nextUpdates) != 1 {
		t.Fatalf("Expected 1 next update, got %d", len(nextUpdates))
	}
	if nextUpdates[0].DesiredState.Label != "test-vpc" || nextUpdates[0].Operation != resource_update.OperationDelete {
		t.Fatalf("Expected vpc delete, got %s %s", nextUpdates[0].DesiredState.Label, nextUpdates[0].Operation)
	}

	// Step 3: Complete VPC delete
	nextUpdates[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(nextUpdates[0])
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Verify third step: should be VPC create
	if len(nextUpdates2) != 1 {
		t.Fatalf("Expected 1 next update, got %d", len(nextUpdates2))
	}
	if nextUpdates2[0].DesiredState.Label != "test-vpc" || nextUpdates2[0].Operation != resource_update.OperationCreate {
		t.Fatalf("Expected vpc create, got %s %s", nextUpdates2[0].DesiredState.Label, nextUpdates2[0].Operation)
	}

	// Step 4: Complete VPC create
	nextUpdates2[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(nextUpdates2[0])
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

	// Verify fourth step: should be both subnet creates (parallel)
	if len(nextUpdates3) != 2 {
		t.Fatalf("Expected 2 next updates, got %d", len(nextUpdates3))
	}

	// Verify both are subnet creates
	subnetCreateCount := 0
	for _, update := range nextUpdates3 {
		if (update.DesiredState.Label == "test-subnet-1" || update.DesiredState.Label == "test-subnet-2") &&
			update.Operation == resource_update.OperationCreate {
			subnetCreateCount++
		}
	}
	if subnetCreateCount != 2 {
		t.Fatalf("Expected 2 subnet creates, got %d", subnetCreateCount)
	}

	// Step 5: Complete both subnet creates
	for _, update := range nextUpdates3 {
		update.State = resource_update.ResourceUpdateStateSuccess
		_, err := changeset.UpdateDAG(update)
		if err != nil {
			t.Fatalf("Error updating DAG: %v", err)
		}
	}

	// Verify completion
	if !changeset.IsComplete() {
		t.Fatalf("Expected changeset to be complete")
	}
}

func TestChangeset_ExecutionOrder_IndependentCreateRunsParallelWithDeleteChain(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		vpc2KsuidURI    = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc-2",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpc2KsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationDelete,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-2",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: pkgmodel.FormaeURI("formae://01TEST_SUBNET2_REPLACE2#/").KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(resourceUpdates, "test-command-1", pkgmodel.CommandApply)
	assert.NoError(t, err)

	// Get initial executable updates
	require.Equal(t, map[string]int{"AWS": 2}, changeset.AvailableExecutableUpdates())
	executableUpdates := changeset.GetExecutableUpdates("AWS", 2)

	// Expected initial executable operations:
	// 1. test-vpc-2 create (no dependencies)
	// 2. test-subnet-1 delete (has dependency, so should execute first in delete chain)
	expectedExecutableCount := 2
	if len(executableUpdates) != expectedExecutableCount {
		t.Fatalf("Expected %d executable updates, got %d", expectedExecutableCount, len(executableUpdates))
	}

	// Verify we have the expected operations
	foundVpc2Create := false
	foundSubnet1Delete := false

	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if update.DesiredState.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			foundSubnet1Delete = true
		}
	}

	if !foundVpc2Create {
		t.Fatalf("Expected test-vpc-2 create to be executable initially")
	}
	if !foundSubnet1Delete {
		t.Fatalf("Expected test-subnet-1 delete to be executable initially")
	}

	// Complete test-subnet-1 delete first
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			subnet1Delete = update
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating DAG after subnet-1 delete: %v", err)
	}

	nextUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// After subnet-1 delete, test-vpc delete should now be executable
	// Plus test-vpc-2 create should still be available
	foundVpcDelete := false
	for _, update := range nextUpdates {
		if update.DesiredState.Label == "test-vpc" && update.Operation == resource_update.OperationDelete {
			foundVpcDelete = true
			break
		}
	}

	if !foundVpcDelete {
		t.Fatalf("Expected test-vpc delete to be executable after subnet-1 delete")
	}
}

func TestChangeset_ExecutionOrder_ExternalResolvableDoesNotBlock(t *testing.T) {
	var (
		vpc2KsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		vpcKsuidURI      = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI  = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI  = pkgmodel.NewFormaeURI(util.NewID(), "")
		externalVpcKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc-2",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpc2KsuidURI.KSUID(), // Fixed: use .KSUID()
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{externalVpcKsuid},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(), // Fixed: use .KSUID()
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationDelete,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-2",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet2KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(resourceUpdates, "test-command-1", pkgmodel.CommandApply)
	assert.NoError(t, err)

	// Print initial DAG state for debugging

	// Get initial executable updates
	require.Equal(t, map[string]int{"AWS": 2}, changeset.AvailableExecutableUpdates())
	executableUpdates := changeset.GetExecutableUpdates("AWS", 2)

	// Expected behavior:
	// - test-vpc-2 create should be executable because it depends on test-vpc-something (which is NOT in changeset, so assumed to exist)
	// - test-subnet-1 delete should be executable (start of delete chain)
	// So we should have 2 executable updates

	if len(executableUpdates) != 2 {
		t.Fatalf("Expected 2 executable updates (test-vpc-2 create and subnet-1 delete), got %d", len(executableUpdates))
	}

	// Verify we have both expected operations
	foundVpc2Create := false
	foundSubnet1Delete := false

	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if update.DesiredState.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			foundSubnet1Delete = true
		}
	}

	if !foundVpc2Create {
		t.Fatalf("Expected test-vpc-2 create to be executable (external dependency should not block)")
	}
	if !foundSubnet1Delete {
		t.Fatalf("Expected test-subnet-1 delete to be executable (start of delete chain)")
	}

	// Complete test-vpc-2 create (should not affect anything since it's independent)
	var vpc2Create *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			vpc2Create = update
			break
		}
	}

	vpc2Create.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(vpc2Create)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	// Complete the delete chain to verify the create chain behavior
	// Step 1: Complete subnet-1 delete
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			subnet1Delete = update
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Should have vpc delete now
	foundVpcDelete := false
	for _, update := range nextUpdates2 {
		if update.DesiredState.Label == "test-vpc" && update.Operation == resource_update.OperationDelete {
			foundVpcDelete = true
			// Complete vpc delete
			update.State = resource_update.ResourceUpdateStateSuccess
			_, err := changeset.UpdateDAG(update)
			if err != nil {
				t.Fatalf("Error updating DAG: %v", err)
			}

			// After vpc delete, should have vpc create
			nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

			// Should have vpc create available
			foundVpcCreate := false
			for _, update2 := range nextUpdates3 {
				if update2.DesiredState.Label == "test-vpc" && update2.Operation == resource_update.OperationCreate {
					foundVpcCreate = true
					break
				}
			}
			if !foundVpcCreate {
				t.Fatalf("Expected vpc create to be available after vpc delete")
			}
			break
		}
	}

	if !foundVpcDelete {
		t.Fatalf("Expected vpc delete to be executable after subnet-1 delete")
	}
}

func TestChangeset_ExecutionOrder_MultipleUpstreamDependenciesBothMustComplete(t *testing.T) {
	var (
		vpc2KsuidURI    = pkgmodel.NewFormaeURI(util.NewID(), "")
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc-2",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpc2KsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationDelete,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-2",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet2KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI, vpc2KsuidURI},
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(resourceUpdates, "test-command-1", pkgmodel.CommandApply)
	assert.NoError(t, err)

	// Get initial executable updates
	require.Equal(t, map[string]int{"AWS": 2}, changeset.AvailableExecutableUpdates())
	executableUpdates := changeset.GetExecutableUpdates("AWS", 2)

	// Expected initial executable operations:
	// 1. test-vpc-2 create (no dependencies)
	// 2. test-subnet-1 delete (first in delete chain)
	// test-subnet-2 create should NOT be executable (depends on both VPCs)

	if len(executableUpdates) != 2 {
		t.Fatalf("Expected 2 executable updates, got %d", len(executableUpdates))
	}

	// Verify we have the expected operations
	foundVpc2Create := false
	foundSubnet1Delete := false

	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if update.DesiredState.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			foundSubnet1Delete = true
		}
		// Verify subnet-2 is NOT executable
		if update.DesiredState.Label == "test-subnet-2" {
			t.Fatalf("test-subnet-2 should not be executable initially (depends on both VPCs)")
		}
	}

	if !foundVpc2Create {
		t.Fatalf("Expected test-vpc-2 create to be executable initially")
	}
	if !foundSubnet1Delete {
		t.Fatalf("Expected test-subnet-1 delete to be executable initially")
	}

	// Step 1: Complete test-vpc-2 create
	var vpc2Create *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			vpc2Create = update
			break
		}
	}

	vpc2Create.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(vpc2Create)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	// Step 2: Complete test-subnet-1 delete
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.DesiredState.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			subnet1Delete = update
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Step 3: Complete test-vpc delete
	var vpcDelete *resource_update.ResourceUpdate
	for _, update := range nextUpdates2 {
		if update.DesiredState.Label == "test-vpc" && update.Operation == resource_update.OperationDelete {
			vpcDelete = update
			break
		}
	}

	if vpcDelete == nil {
		t.Fatalf("Expected test-vpc delete to be executable after subnet-1 delete")
		return
	}

	vpcDelete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(vpcDelete)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
		return
	}

	nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

	// Step 4: Complete test-vpc create
	var vpcCreate *resource_update.ResourceUpdate
	for _, update := range nextUpdates3 {
		if update.DesiredState.Label == "test-vpc" && update.Operation == resource_update.OperationCreate {
			vpcCreate = update
			break
		}
	}

	if vpcCreate == nil {
		t.Fatalf("Expected test-vpc create to be executable after vpc delete")
		return
	}

	vpcCreate.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(vpcCreate)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	finalUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// Step 5: Now both subnet creates should be executable
	// (subnet-1 depends only on vpc, subnet-2 depends on both vpc and vpc-2 which are both complete)
	expectedSubnetCreates := 2
	subnetCreateCount := 0

	for _, update := range finalUpdates {
		if (update.DesiredState.Label == "test-subnet-1" || update.DesiredState.Label == "test-subnet-2") &&
			update.Operation == resource_update.OperationCreate {
			subnetCreateCount++
		}
	}

	if subnetCreateCount != expectedSubnetCreates {
		t.Fatalf("Expected %d subnet creates, got %d", expectedSubnetCreates, subnetCreateCount)
	}
}

func TestChangeset_Init_DifferentTypesSameLabelNoFalseReplace(t *testing.T) {
	var (
		vpcKsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdateInitial := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "replace-resource-type",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "replace-resource-type",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnetKsuid.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
	}
	changeset, err := NewChangesetFromResourceUpdates(resourceUpdateInitial, "test-command-1", pkgmodel.CommandApply)
	assert.NoError(t, err)

	executables := changeset.GetExecutableUpdates("AWS", 100)
	assert.Len(t, executables, 2, "Expected 2 initial executable updates (independent operations)")

	// Find the delete and create operations
	var subnetDelete, vpcCreate *resource_update.ResourceUpdate
	for i := range executables {
		if executables[i].Operation == resource_update.OperationDelete {
			subnetDelete = executables[i]
		} else if executables[i].Operation == resource_update.OperationCreate {
			vpcCreate = executables[i]
		}
	}

	assert.NotNil(t, subnetDelete, "Expected to find delete operation")
	assert.NotNil(t, vpcCreate, "Expected to find create operation")
	assert.Equal(t, "AWS::EC2::Subnet", subnetDelete.DesiredState.Type)
	assert.Equal(t, "AWS::EC2::VPC", vpcCreate.DesiredState.Type)

	// Complete delete op
	subnetDelete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(subnetDelete)
	assert.NoError(t, err)

	// Complete create op
	vpcCreate.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdateDAG(vpcCreate)
	assert.NoError(t, err)

	noUpdates := changeset.GetExecutableUpdates("AWS", 5)
	assert.Len(t, noUpdates, 0, "Expected no further updates after both operations complete")
	assert.True(t, changeset.IsComplete())
}

func TestChangeset_ExecutionOrder_DeleteChainReversesCreateDependencies(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationDelete,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(resourceUpdates, "test-command-1", pkgmodel.CommandApply)
	assert.NoError(t, err)

	executables := changeset.GetExecutableUpdates("AWS", 100)
	assert.NotNil(t, executables)
}

func TestChangeset_FailureCascade_TransitiveDependentsAllFail(t *testing.T) {
	var (
		vpcKsuidURI   = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuid   = pkgmodel.NewFormaeURI(util.NewID(), "")
		instanceKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// Dependency chain: VPC -> Subnet -> Instance
	vpcUpdate := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "test-stack",
			Ksuid: vpcKsuidURI.KSUID(),
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateNotStarted,
	}
	subnetUpdate := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "subnet",
			Type:  "AWS::EC2::Subnet",
			Stack: "test-stack",
			Ksuid: subnetKsuid.KSUID(),
		},
		Operation:            resource_update.OperationCreate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
	}
	instanceUpdate := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "instance",
			Type:  "AWS::EC2::Instance",
			Stack: "test-stack",
			Ksuid: instanceKsuid.KSUID(),
		},
		Operation:            resource_update.OperationCreate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{subnetKsuid},
	}

	changeset, err := NewChangesetFromResourceUpdates(
		[]resource_update.ResourceUpdate{vpcUpdate, subnetUpdate, instanceUpdate},
		"test-recursive-cascade",
		pkgmodel.CommandApply,
	)
	assert.NoError(t, err)

	// Simulate VPC failure
	vpcUpdate.State = resource_update.ResourceUpdateStateFailed
	failedUpdates := changeset.failResourceUpdate(&vpcUpdate)

	failedLabels := make([]string, 0, len(failedUpdates))
	for _, update := range failedUpdates {
		failedLabels = append(failedLabels, update.DesiredState.Label)
	}

	// both subnet AND instance should be failed
	assert.Contains(t, failedLabels, "subnet", "Subnet should be failed due to VPC failure")
	assert.Contains(t, failedLabels, "instance", "Instance should be failed due to recursive cascading")
	assert.Len(t, failedLabels, 3) // vpc, subnet, instance
}

func TestChangeset_UpdateDAG_FailureCascadeRemovesNodes(t *testing.T) {
	var (
		vpcKsuidURI   = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuid   = pkgmodel.NewFormaeURI(util.NewID(), "")
		instanceKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// Dependency chain: VPC -> Subnet -> Instance
	vpcUpdate := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "test-stack",
			Ksuid: vpcKsuidURI.KSUID(),
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateNotStarted,
	}
	subnetUpdate := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "subnet",
			Type:  "AWS::EC2::Subnet",
			Stack: "test-stack",
			Ksuid: subnetKsuid.KSUID(),
		},
		Operation:            resource_update.OperationCreate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
	}
	instanceUpdate := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "instance",
			Type:  "AWS::EC2::Instance",
			Stack: "test-stack",
			Ksuid: instanceKsuid.KSUID(),
		},
		Operation:            resource_update.OperationCreate,
		State:                resource_update.ResourceUpdateStateNotStarted,
		RemainingResolvables: []pkgmodel.FormaeURI{subnetKsuid},
	}

	changeset, err := NewChangesetFromResourceUpdates(
		[]resource_update.ResourceUpdate{vpcUpdate, subnetUpdate, instanceUpdate},
		"test-update-DAG-cascade",
		pkgmodel.CommandApply,
	)
	require.NoError(t, err)

	// Verify initial state: 3 groups
	assert.Len(t, changeset.DAG.Nodes, 3)
	assert.False(t, changeset.IsComplete())

	// Mark VPC as failed and call UpdateDAG
	vpcUpdate.State = resource_update.ResourceUpdateStateFailed
	failedUpdates, err := changeset.UpdateDAG(&vpcUpdate)
	require.NoError(t, err)

	// Verify cascading failures were detected
	assert.NotNil(t, failedUpdates)
	assert.Len(t, failedUpdates, 3) // vpc, subnet, instance

	// Verify all failed resources have Failed state
	for _, update := range failedUpdates {
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, update.State,
			"Resource %s should be marked as Failed", update.DesiredState.Label)
	}

	// Verify empty groups were removed from DAG
	assert.Len(t, changeset.DAG.Nodes, 0,
		"All groups should be removed after cascading failures")

	// Verify changeset is now complete
	assert.True(t, changeset.IsComplete(),
		"Changeset should be complete when all resources are failed")
}

func TestChangeset_GetExecutableUpdates_FiltersByNamespace(t *testing.T) {
	var (
		bucket1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		bucket2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		bucket3KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-bucket-1",
				Type:  "AWS::S3::Bucket",
				Stack: "test-stack",
				Ksuid: bucket1KsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-bucket-2",
				Type:  "AWS::S3::Bucket",
				Stack: "test-stack",
				Ksuid: bucket2KsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-bucket-3",
				Type:  "FakeAWS::S3::Bucket",
				Stack: "test-stack",
				Ksuid: bucket3KsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationDelete,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(
		updates,
		"test-max-n",
		pkgmodel.CommandApply,
	)
	assert.NoError(t, err)

	availableUpdates := changeset.AvailableExecutableUpdates()
	require.Len(t, availableUpdates, 2)

	require.Contains(t, availableUpdates, "AWS")
	assert.Equal(t, 2, availableUpdates["AWS"])

	awsUpdates := changeset.GetExecutableUpdates("AWS", 5)
	assert.Len(t, awsUpdates, 2)

	require.Contains(t, availableUpdates, "FakeAWS")
	assert.Equal(t, 1, availableUpdates["FakeAWS"])

	res := changeset.GetExecutableUpdates("FakeAWS", 5)
	assert.Len(t, res, 1)
}

func TestChangeset_Init_CyclicDependenciesReturnError(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{subnet1KsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	_, err := NewChangesetFromResourceUpdates(
		resourceUpdates,
		"test-cycle",
		pkgmodel.CommandApply,
	)

	assert.Error(t, err)
}

func TestChangeset_GetExecutableUpdates_RespectsMaxLimit(t *testing.T) {
	bucket1URI := pkgmodel.NewFormaeURI(util.NewID(), "")
	bucket2URI := pkgmodel.NewFormaeURI(util.NewID(), "")
	bucket3URI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-1", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: bucket1URI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-2", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: bucket2URI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-3", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: bucket3URI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	result := cs.GetExecutableUpdates("AWS", 1)
	assert.Len(t, result, 1, "max=1 should return at most 1 update")

	available := cs.AvailableExecutableUpdates()
	assert.Equal(t, 2, available["AWS"])
}

func TestChangeset_UpdateDAG_RejectedUpdateCascadesToDependents(t *testing.T) {
	vpcURI := pkgmodel.NewFormaeURI(util.NewID(), "")
	subnetURI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "vpc", Type: "AWS::EC2::VPC", Stack: "s", Ksuid: vpcURI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState:         pkgmodel.Resource{Label: "subnet", Type: "AWS::EC2::Subnet", Stack: "s", Ksuid: subnetURI.KSUID()},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "s",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcURI},
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	exec := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec, 1)
	assert.Equal(t, "vpc", exec[0].DesiredState.Label)

	exec[0].State = resource_update.ResourceUpdateStateRejected
	cascaded, err := cs.UpdateDAG(exec[0])
	require.NoError(t, err)

	assert.Len(t, cascaded, 2)
	assert.True(t, cs.IsComplete())
}

func TestChangeset_IsComplete_InProgressIsNotComplete(t *testing.T) {
	bucketURI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: bucketURI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	exec := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec, 1)

	assert.False(t, cs.IsComplete(), "changeset with in-progress update should not be complete")
}

func TestChangeset_IsComplete_EmptyChangesetIsComplete(t *testing.T) {
	cs, err := NewChangesetFromResourceUpdates(nil, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	assert.True(t, cs.IsComplete(), "empty changeset should be complete")
}

func TestChangeset_ExecutionOrder_DestroyChainCompletesInReverseOrder(t *testing.T) {
	vpcURI := pkgmodel.NewFormaeURI(util.NewID(), "")
	subnetURI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "vpc", Type: "AWS::EC2::VPC", Stack: "s", Ksuid: vpcURI.KSUID()},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState:         pkgmodel.Resource{Label: "subnet", Type: "AWS::EC2::Subnet", Stack: "s", Ksuid: subnetURI.KSUID()},
			Operation:            resource_update.OperationDelete,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "s",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcURI},
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	// Subnet delete first (reversed dependency order)
	exec := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec, 1)
	assert.Equal(t, "subnet", exec[0].DesiredState.Label)

	exec[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = cs.UpdateDAG(exec[0])
	require.NoError(t, err)

	// VPC delete now unblocked
	exec2 := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec2, 1)
	assert.Equal(t, "vpc", exec2[0].DesiredState.Label)

	exec2[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = cs.UpdateDAG(exec2[0])
	require.NoError(t, err)

	assert.True(t, cs.IsComplete())
}

func TestChangeset_ExecutionOrder_UpdateOperationRespectsDependencies(t *testing.T) {
	vpcURI := pkgmodel.NewFormaeURI(util.NewID(), "")
	subnetURI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "vpc", Type: "AWS::EC2::VPC", Stack: "s", Ksuid: vpcURI.KSUID()},
			Operation:    resource_update.OperationUpdate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState:         pkgmodel.Resource{Label: "subnet", Type: "AWS::EC2::Subnet", Stack: "s", Ksuid: subnetURI.KSUID()},
			Operation:            resource_update.OperationUpdate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "s",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcURI},
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	// VPC update should be first (subnet depends on it)
	exec := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec, 1)
	assert.Equal(t, "vpc", exec[0].DesiredState.Label)
	assert.Equal(t, resource_update.OperationUpdate, exec[0].Operation)

	exec[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = cs.UpdateDAG(exec[0])
	require.NoError(t, err)

	// Subnet update now unblocked
	exec2 := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec2, 1)
	assert.Equal(t, "subnet", exec2[0].DesiredState.Label)
	assert.Equal(t, resource_update.OperationUpdate, exec2[0].Operation)

	exec2[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = cs.UpdateDAG(exec2[0])
	require.NoError(t, err)

	assert.True(t, cs.IsComplete())
}

func TestChangeset_AvailableExecutableUpdates_SkipsInProgressNodes(t *testing.T) {
	bucket1URI := pkgmodel.NewFormaeURI(util.NewID(), "")
	bucket2URI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-1", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: bucket1URI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-2", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: bucket2URI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	// Start one update (moves to InProgress)
	exec := cs.GetExecutableUpdates("AWS", 1)
	require.Len(t, exec, 1)

	// Only the other bucket should now be available
	available := cs.AvailableExecutableUpdates()
	assert.Equal(t, 1, available["AWS"])
}

func TestChangeset_Init_ReplaceOperationSplitsIntoDeleteAndCreateNodes(t *testing.T) {
	vpcURI := pkgmodel.NewFormaeURI(util.NewID(), "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "vpc", Type: "AWS::EC2::VPC", Stack: "s", Ksuid: vpcURI.KSUID()},
			Operation:    resource_update.OperationReplace,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := NewChangesetFromResourceUpdates(updates, "cmd-1", pkgmodel.CommandApply)
	require.NoError(t, err)

	// Replace should produce 2 DAG nodes: one delete, one create
	assert.Len(t, cs.DAG.Nodes, 2)

	// Delete should execute first
	exec := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec, 1)
	assert.Equal(t, resource_update.OperationDelete, exec[0].Operation)

	exec[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = cs.UpdateDAG(exec[0])
	require.NoError(t, err)

	// Then create
	exec2 := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec2, 1)
	assert.Equal(t, resource_update.OperationCreate, exec2[0].Operation)

	exec2[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = cs.UpdateDAG(exec2[0])
	require.NoError(t, err)

	assert.True(t, cs.IsComplete())
}
