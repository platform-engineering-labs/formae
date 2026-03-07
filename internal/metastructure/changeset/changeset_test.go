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

// Compile-time verification that ResourceUpdate satisfies the Update interface
var _ Update = (*resource_update.ResourceUpdate)(nil)

// asResourceUpdate is a test helper that type-asserts an Update to *resource_update.ResourceUpdate
func asResourceUpdate(t *testing.T, u Update) *resource_update.ResourceUpdate {
	t.Helper()
	ru, ok := u.(*resource_update.ResourceUpdate)
	require.True(t, ok, "expected *resource_update.ResourceUpdate, got %T", u)
	return ru
}

// updateDAGHelper computes the node URI and calls UpdateDAG — convenience for tests
func updateDAGHelper(t *testing.T, c Changeset, ru *resource_update.ResourceUpdate) ([]Update, error) {
	t.Helper()
	nodeURI := createOperationURI(ru.URI(), ru.Operation)
	return c.UpdateDAG(nodeURI, ru)
}

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
	ru0 := asResourceUpdate(t, executableUpdates[0])
	if ru0.DesiredState.Label != "test-subnet-1" || ru0.Operation != resource_update.OperationDelete {
		t.Fatalf("Expected subnet-1 delete, got %s %s", ru0.DesiredState.Label, ru0.Operation)
	}

	// Step 2: Complete subnet-1 delete
	ru0.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, ru0)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// Verify second step: should be VPC delete
	if len(nextUpdates) != 1 {
		t.Fatalf("Expected 1 next update, got %d", len(nextUpdates))
	}
	ruNext0 := asResourceUpdate(t, nextUpdates[0])
	if ruNext0.DesiredState.Label != "test-vpc" || ruNext0.Operation != resource_update.OperationDelete {
		t.Fatalf("Expected vpc delete, got %s %s", ruNext0.DesiredState.Label, ruNext0.Operation)
	}

	// Step 3: Complete VPC delete
	ruNext0.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, ruNext0)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Verify third step: should be VPC create
	if len(nextUpdates2) != 1 {
		t.Fatalf("Expected 1 next update, got %d", len(nextUpdates2))
	}
	ruNext2 := asResourceUpdate(t, nextUpdates2[0])
	if ruNext2.DesiredState.Label != "test-vpc" || ruNext2.Operation != resource_update.OperationCreate {
		t.Fatalf("Expected vpc create, got %s %s", ruNext2.DesiredState.Label, ruNext2.Operation)
	}

	// Step 4: Complete VPC create
	ruNext2.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, ruNext2)
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
		ru := asResourceUpdate(t, update)
		if (ru.DesiredState.Label == "test-subnet-1" || ru.DesiredState.Label == "test-subnet-2") &&
			ru.Operation == resource_update.OperationCreate {
			subnetCreateCount++
		}
	}
	if subnetCreateCount != 2 {
		t.Fatalf("Expected 2 subnet creates, got %d", subnetCreateCount)
	}

	// Step 5: Complete both subnet creates
	for _, update := range nextUpdates3 {
		ru := asResourceUpdate(t, update)
		ru.State = resource_update.ResourceUpdateStateSuccess
		_, err := updateDAGHelper(t, changeset, ru)
		if err != nil {
			t.Fatalf("Error updating DAG: %v", err)
		}
	}

	// Verify completion
	if !changeset.IsComplete() {
		t.Fatalf("Expected changeset to be complete")
	}
}

func TestChangeset_RemoveNode_UnlinksAllDependentsWhenThreeOrMore(t *testing.T) {
	// Regression test: removeNode must unlink ALL dependents, not just some.
	// With 3+ dependents, iterating node.Dependents while Unlink modifies
	// the same slice causes elements to be skipped (Go range captures the
	// backing array pointer but Unlink shifts elements via append).
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet3KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// VPC create with 3 dependent subnet creates
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "subnet-1",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet1KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "subnet-2",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet2KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "subnet-3",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnet3KsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	changeset, err := NewChangesetFromResourceUpdates(resourceUpdates, "test-unlink-all", pkgmodel.CommandApply)
	require.NoError(t, err)

	// Step 1: Only VPC should be executable (no dependencies)
	executable := changeset.GetExecutableUpdates("AWS", 10)
	require.Len(t, executable, 1)
	vpcRU := asResourceUpdate(t, executable[0])
	assert.Equal(t, "vpc", vpcRU.DesiredState.Label)

	// Step 2: Complete VPC — all 3 subnets must become executable
	vpcRU.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, vpcRU)
	require.NoError(t, err)

	executable2 := changeset.GetExecutableUpdates("AWS", 10)
	require.Len(t, executable2, 3, "all 3 subnets should be executable after VPC completes")

	labels := make([]string, 0, 3)
	for _, u := range executable2 {
		labels = append(labels, asResourceUpdate(t, u).DesiredState.Label)
	}
	assert.ElementsMatch(t, []string{"subnet-1", "subnet-2", "subnet-3"}, labels)

	// Step 3: Complete all subnets
	for _, u := range executable2 {
		ru := asResourceUpdate(t, u)
		ru.State = resource_update.ResourceUpdateStateSuccess
		_, err := updateDAGHelper(t, changeset, ru)
		require.NoError(t, err)
	}

	assert.True(t, changeset.IsComplete())
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
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc-2" && ru.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if ru.DesiredState.Label == "test-subnet-1" && ru.Operation == resource_update.OperationDelete {
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
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-subnet-1" && ru.Operation == resource_update.OperationDelete {
			subnet1Delete = ru
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating DAG after subnet-1 delete: %v", err)
	}

	nextUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// After subnet-1 delete, test-vpc delete should now be executable
	// Plus test-vpc-2 create should still be available
	foundVpcDelete := false
	for _, update := range nextUpdates {
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc" && ru.Operation == resource_update.OperationDelete {
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
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc-2" && ru.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if ru.DesiredState.Label == "test-subnet-1" && ru.Operation == resource_update.OperationDelete {
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
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc-2" && ru.Operation == resource_update.OperationCreate {
			vpc2Create = ru
			break
		}
	}

	vpc2Create.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, vpc2Create)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	// Complete the delete chain to verify the create chain behavior
	// Step 1: Complete subnet-1 delete
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-subnet-1" && ru.Operation == resource_update.OperationDelete {
			subnet1Delete = ru
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Should have vpc delete now
	foundVpcDelete := false
	for _, update := range nextUpdates2 {
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc" && ru.Operation == resource_update.OperationDelete {
			foundVpcDelete = true
			// Complete vpc delete
			ru.State = resource_update.ResourceUpdateStateSuccess
			_, err := updateDAGHelper(t, changeset, ru)
			if err != nil {
				t.Fatalf("Error updating DAG: %v", err)
			}

			// After vpc delete, should have vpc create
			nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

			// Should have vpc create available
			foundVpcCreate := false
			for _, update2 := range nextUpdates3 {
				ru2 := asResourceUpdate(t, update2)
				if ru2.DesiredState.Label == "test-vpc" && ru2.Operation == resource_update.OperationCreate {
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
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc-2" && ru.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if ru.DesiredState.Label == "test-subnet-1" && ru.Operation == resource_update.OperationDelete {
			foundSubnet1Delete = true
		}
		// Verify subnet-2 is NOT executable
		if ru.DesiredState.Label == "test-subnet-2" {
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
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc-2" && ru.Operation == resource_update.OperationCreate {
			vpc2Create = ru
			break
		}
	}

	vpc2Create.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, vpc2Create)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	// Step 2: Complete test-subnet-1 delete
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-subnet-1" && ru.Operation == resource_update.OperationDelete {
			subnet1Delete = ru
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Step 3: Complete test-vpc delete
	var vpcDelete *resource_update.ResourceUpdate
	for _, update := range nextUpdates2 {
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc" && ru.Operation == resource_update.OperationDelete {
			vpcDelete = ru
			break
		}
	}

	if vpcDelete == nil {
		t.Fatalf("Expected test-vpc delete to be executable after subnet-1 delete")
		return
	}

	vpcDelete.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, vpcDelete)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
		return
	}

	nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

	// Step 4: Complete test-vpc create
	var vpcCreate *resource_update.ResourceUpdate
	for _, update := range nextUpdates3 {
		ru := asResourceUpdate(t, update)
		if ru.DesiredState.Label == "test-vpc" && ru.Operation == resource_update.OperationCreate {
			vpcCreate = ru
			break
		}
	}

	if vpcCreate == nil {
		t.Fatalf("Expected test-vpc create to be executable after vpc delete")
		return
	}

	vpcCreate.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, vpcCreate)
	if err != nil {
		t.Fatalf("Error updating DAG: %v", err)
	}

	finalUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// Step 5: Now both subnet creates should be executable
	// (subnet-1 depends only on vpc, subnet-2 depends on both vpc and vpc-2 which are both complete)
	expectedSubnetCreates := 2
	subnetCreateCount := 0

	for _, update := range finalUpdates {
		ru := asResourceUpdate(t, update)
		if (ru.DesiredState.Label == "test-subnet-1" || ru.DesiredState.Label == "test-subnet-2") &&
			ru.Operation == resource_update.OperationCreate {
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
		ru := asResourceUpdate(t, executables[i])
		if ru.Operation == resource_update.OperationDelete {
			subnetDelete = ru
		} else if ru.Operation == resource_update.OperationCreate {
			vpcCreate = ru
		}
	}

	assert.NotNil(t, subnetDelete, "Expected to find delete operation")
	assert.NotNil(t, vpcCreate, "Expected to find create operation")
	assert.Equal(t, "AWS::EC2::Subnet", subnetDelete.DesiredState.Type)
	assert.Equal(t, "AWS::EC2::VPC", vpcCreate.DesiredState.Type)

	// Complete delete op
	subnetDelete.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, subnetDelete)
	assert.NoError(t, err)

	// Complete create op
	vpcCreate.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, changeset, vpcCreate)
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
	vpcNodeURI := createOperationURI(vpcUpdate.URI(), vpcUpdate.Operation)
	failedNodes := changeset.failDependents(changeset.DAG.Nodes[vpcNodeURI])

	failedLabels := make([]string, 0, len(failedNodes)+1)
	failedLabels = append(failedLabels, vpcUpdate.DesiredState.Label)
	for _, node := range failedNodes {
		ru := node.Update.(*resource_update.ResourceUpdate)
		failedLabels = append(failedLabels, ru.DesiredState.Label)
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
	failedUpdates, err := updateDAGHelper(t, changeset, &vpcUpdate)
	require.NoError(t, err)

	// Verify cascading failures were detected
	assert.NotNil(t, failedUpdates)
	assert.Len(t, failedUpdates, 3) // vpc, subnet, instance

	// Verify all failed resources have Failed state
	for _, update := range failedUpdates {
		ru := asResourceUpdate(t, update)
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, ru.State,
			"Resource %s should be marked as Failed", ru.DesiredState.Label)
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
	ruExec0 := asResourceUpdate(t, exec[0])
	assert.Equal(t, "vpc", ruExec0.DesiredState.Label)

	ruExec0.State = resource_update.ResourceUpdateStateRejected
	cascaded, err := updateDAGHelper(t, cs, ruExec0)
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
	ruExec := asResourceUpdate(t, exec[0])
	assert.Equal(t, "subnet", ruExec.DesiredState.Label)

	ruExec.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, ruExec)
	require.NoError(t, err)

	// VPC delete now unblocked
	exec2 := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec2, 1)
	ruExec2 := asResourceUpdate(t, exec2[0])
	assert.Equal(t, "vpc", ruExec2.DesiredState.Label)

	ruExec2.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, ruExec2)
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
	ruExec := asResourceUpdate(t, exec[0])
	assert.Equal(t, "vpc", ruExec.DesiredState.Label)
	assert.Equal(t, resource_update.OperationUpdate, ruExec.Operation)

	ruExec.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, ruExec)
	require.NoError(t, err)

	// Subnet update now unblocked
	exec2 := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec2, 1)
	ruExec2 := asResourceUpdate(t, exec2[0])
	assert.Equal(t, "subnet", ruExec2.DesiredState.Label)
	assert.Equal(t, resource_update.OperationUpdate, ruExec2.Operation)

	ruExec2.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, ruExec2)
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
	ruExec := asResourceUpdate(t, exec[0])
	assert.Equal(t, resource_update.OperationDelete, ruExec.Operation)

	ruExec.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, ruExec)
	require.NoError(t, err)

	// Then create
	exec2 := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec2, 1)
	ruExec2 := asResourceUpdate(t, exec2[0])
	assert.Equal(t, resource_update.OperationCreate, ruExec2.Operation)

	ruExec2.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, ruExec2)
	require.NoError(t, err)

	assert.True(t, cs.IsComplete())
}
