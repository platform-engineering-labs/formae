// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Compile-time verification that ResourceUpdate satisfies the Update interface
var _ Update = (*resource_update.ResourceUpdate)(nil)

// Compile-time verification that TargetUpdate satisfies the Update interface
var _ Update = (*target_update.TargetUpdate)(nil)

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

	changeset, err := buildChangesetForTest(resourceUpdates, nil, "test-command-1", pkgmodel.CommandApply, nil)
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
	// removeNode must unlink ALL dependents, not just some.
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

	changeset, err := buildChangesetForTest(resourceUpdates, nil, "test-unlink-all", pkgmodel.CommandApply, nil)
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

	changeset, err := buildChangesetForTest(resourceUpdates, nil, "test-command-1", pkgmodel.CommandApply, nil)
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

	changeset, err := buildChangesetForTest(resourceUpdates, nil, "test-command-1", pkgmodel.CommandApply, nil)
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

	changeset, err := buildChangesetForTest(resourceUpdates, nil, "test-command-1", pkgmodel.CommandApply, nil)
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
	changeset, err := buildChangesetForTest(resourceUpdateInitial, nil, "test-command-1", pkgmodel.CommandApply, nil)
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

	changeset, err := buildChangesetForTest(resourceUpdates, nil, "test-command-1", pkgmodel.CommandApply, nil)
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

	changeset, err := buildChangesetForTest(
		[]resource_update.ResourceUpdate{vpcUpdate, subnetUpdate, instanceUpdate},
		nil,
		"test-recursive-cascade",
		pkgmodel.CommandApply,
		nil,
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

	changeset, err := buildChangesetForTest(
		[]resource_update.ResourceUpdate{vpcUpdate, subnetUpdate, instanceUpdate},
		nil,
		"test-update-DAG-cascade",
		pkgmodel.CommandApply,
		nil,
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

	changeset, err := buildChangesetForTest(
		updates,
		nil,
		"test-max-n",
		pkgmodel.CommandApply,
		nil,
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

	_, err := buildChangesetForTest(
		resourceUpdates,
		nil,
		"test-cycle",
		pkgmodel.CommandApply,
		nil,
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	exec := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, exec, 1)

	assert.False(t, cs.IsComplete(), "changeset with in-progress update should not be complete")
}

func TestChangeset_IsComplete_EmptyChangesetIsComplete(t *testing.T) {
	cs, err := buildChangesetForTest(nil, nil, "cmd-1", pkgmodel.CommandApply, nil)
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// Start one update (moves to InProgress)
	exec := cs.GetExecutableUpdates("AWS", 1)
	require.Len(t, exec, 1)

	// Only the other bucket should now be available
	available := cs.AvailableExecutableUpdates()
	assert.Equal(t, 1, available["AWS"])
}

func TestChangeset_GetExecutableUpdates_ReturnsTargetsWithoutCountingAgainstMax(t *testing.T) {
	// Build a changeset manually with mixed resource + target updates
	cs := Changeset{
		CommandID:      "test-mixed",
		DAG:            NewExecutionDAG(),
		trackedUpdates: make(map[string]bool),
	}

	// Add a resource update
	ru := &resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Label: "vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "test-stack",
			Ksuid: pkgmodel.NewFormaeURI(util.NewID(), "").KSUID(),
		},
		Operation:  resource_update.OperationCreate,
		State:      resource_update.ResourceUpdateStateNotStarted,
		StackLabel: "test-stack",
	}
	ruURI := createOperationURI(ru.URI(), ru.Operation)
	cs.DAG.Nodes[ruURI] = &DAGNode{URI: ruURI, Update: ru, Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}

	// Add a target update
	tu := &target_update.TargetUpdate{
		Target:    pkgmodel.Target{Label: "my-target", Namespace: "AWS"},
		Operation: target_update.TargetOperationCreate,
		State:     target_update.TargetUpdateStateNotStarted,
	}
	tuURI := tu.NodeURI()
	cs.DAG.Nodes[tuURI] = &DAGNode{URI: tuURI, Update: tu, Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}

	// With max=1, we should get both: 1 resource (counts against max) + 1 target (does NOT count)
	result := cs.GetExecutableUpdates("AWS", 1)
	assert.Len(t, result, 2, "should return both the resource update and the target update (target doesn't count against max)")
}

func TestChangeset_AvailableExecutableUpdates_CountsOnlyResourceUpdates(t *testing.T) {
	// Build a changeset manually with mixed resource + target updates
	cs := Changeset{
		CommandID:      "test-mixed-available",
		DAG:            NewExecutionDAG(),
		trackedUpdates: make(map[string]bool),
	}

	// Add 3 resource updates
	for _, label := range []string{"vpc-1", "vpc-2", "vpc-3"} {
		ru := &resource_update.ResourceUpdate{
			DesiredState: pkgmodel.Resource{
				Label: label,
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: pkgmodel.NewFormaeURI(util.NewID(), "").KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StackLabel: "test-stack",
		}
		ruURI := createOperationURI(ru.URI(), ru.Operation)
		cs.DAG.Nodes[ruURI] = &DAGNode{URI: ruURI, Update: ru, Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}
	}

	// Add 2 target updates
	for _, label := range []string{"target-1", "target-2"} {
		tu := &target_update.TargetUpdate{
			Target:    pkgmodel.Target{Label: label, Namespace: "AWS"},
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		}
		tuURI := tu.NodeURI()
		cs.DAG.Nodes[tuURI] = &DAGNode{URI: tuURI, Update: tu, Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}
	}

	// AvailableExecutableUpdates should only count rate-limited (resource) updates
	available := cs.AvailableExecutableUpdates()
	assert.Equal(t, map[string]int{"AWS": 3}, available, "should only count resource updates (3), not target updates")
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

	cs, err := buildChangesetForTest(updates, nil, "cmd-1", pkgmodel.CommandApply, nil)
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

func TestChangeset_ExecutionOrder_MixedResourceAndTargetUpdates(t *testing.T) {
	bucket1URI := pkgmodel.NewFormaeURI(util.NewID(), "")
	bucket2URI := pkgmodel.NewFormaeURI(util.NewID(), "")

	resourceUpdates := []resource_update.ResourceUpdate{
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

	targetUpdates := []target_update.TargetUpdate{
		{
			Target:    pkgmodel.Target{Label: "my-target", Namespace: "AWS"},
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-mixed", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// Step 2: AvailableExecutableUpdates should only count resource updates (rate-limited), not targets
	available := cs.AvailableExecutableUpdates()
	assert.Equal(t, map[string]int{"AWS": 2}, available)

	// Step 3: GetExecutableUpdates with max=1 should return 2 results:
	// 1 resource (counts against max) + 1 target (doesn't count against max)
	updates := cs.GetExecutableUpdates("AWS", 1)
	require.Len(t, updates, 2)

	// Separate the target update from the resource update
	var firstResource *resource_update.ResourceUpdate
	var targetUp *target_update.TargetUpdate
	for _, u := range updates {
		switch v := u.(type) {
		case *target_update.TargetUpdate:
			targetUp = v
		case *resource_update.ResourceUpdate:
			firstResource = v
		}
	}
	require.NotNil(t, targetUp, "expected a target update in the results")
	require.NotNil(t, firstResource, "expected a resource update in the results")
	assert.Equal(t, "my-target", targetUp.Target.Label)

	// Step 4: Complete the target update
	targetUp.State = target_update.TargetUpdateStateSuccess
	_, err = cs.UpdateDAG(targetUp.NodeURI(), targetUp)
	require.NoError(t, err)

	// Step 5: Complete the first resource update
	firstResource.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, firstResource)
	require.NoError(t, err)

	// Step 6: GetExecutableUpdates should return the remaining resource update
	updates2 := cs.GetExecutableUpdates("AWS", 1)
	require.Len(t, updates2, 1)
	secondResource := asResourceUpdate(t, updates2[0])

	// Step 7: Complete the last resource update and verify completion
	secondResource.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, secondResource)
	require.NoError(t, err)

	assert.True(t, cs.IsComplete())
}

func TestDAGNode_LinkWith_DuplicateIsNoop(t *testing.T) {
	nodeA := &DAGNode{URI: "a", Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}
	nodeB := &DAGNode{URI: "b", Dependents: []*DAGNode{}, Dependencies: []*DAGNode{}}

	// First link
	nodeA.LinkWith(nodeB)
	assert.Len(t, nodeA.Dependencies, 1)
	assert.Len(t, nodeB.Dependents, 1)

	// Second link with same node — should be a noop
	nodeA.LinkWith(nodeB)
	assert.Len(t, nodeA.Dependencies, 1, "duplicate LinkWith should not add a second dependency")
	assert.Len(t, nodeB.Dependents, 1, "duplicate LinkWith should not add a second dependent")
}

func TestChangeset_UpdateDAG_UnexpectedStateTreatedAsFailure(t *testing.T) {
	uri := pkgmodel.NewFormaeURI(util.NewID(), "")

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: uri.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, nil, "cmd-unexpected", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// Get the update to mark it in progress (so it's not "ready" and not "success" and not "failed")
	updates := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, updates, 1)
	ru := asResourceUpdate(t, updates[0])

	// Set to InProgress — this is neither success nor failed nor ready
	ru.State = resource_update.ResourceUpdateStateInProgress

	// UpdateDAG should treat the unexpected state as failure
	failedUpdates, err := updateDAGHelper(t, cs, ru)
	require.NoError(t, err)
	// The unexpected-state code calls MarkFailed and recursively calls UpdateDAG,
	// so we should get the update back in the failed list
	require.Len(t, failedUpdates, 1)
	assert.True(t, failedUpdates[0].IsFailed())
}

func TestChangeset_IsComplete_AllFailedIsComplete(t *testing.T) {
	uri1 := pkgmodel.NewFormaeURI(util.NewID(), "")
	uri2 := pkgmodel.NewFormaeURI(util.NewID(), "")

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-1", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: uri1.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState: pkgmodel.Resource{Label: "bucket-2", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: uri2.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, nil, "cmd-allfailed", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// Get both updates and fail them
	updates := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, updates, 2)

	for _, u := range updates {
		ru := asResourceUpdate(t, u)
		ru.State = resource_update.ResourceUpdateStateFailed
		_, err = updateDAGHelper(t, cs, ru)
		require.NoError(t, err)
	}

	// All nodes removed after failure — should be complete
	assert.True(t, cs.IsComplete())
}

func TestChangeset_FailureCascade_SkipsInProgressDependents(t *testing.T) {
	parentURI := pkgmodel.NewFormaeURI(util.NewID(), "")
	child1URI := pkgmodel.NewFormaeURI(util.NewID(), "")
	child2URI := pkgmodel.NewFormaeURI(util.NewID(), "")

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "parent", Type: "AWS::EC2::VPC", Stack: "s", Ksuid: parentURI.KSUID()},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState:         pkgmodel.Resource{Label: "child-1", Type: "AWS::EC2::Subnet", Stack: "s", Ksuid: child1URI.KSUID()},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "s",
			RemainingResolvables: []pkgmodel.FormaeURI{parentURI},
		},
		{
			DesiredState:         pkgmodel.Resource{Label: "child-2", Type: "AWS::EC2::Subnet", Stack: "s", Ksuid: child2URI.KSUID()},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "s",
			RemainingResolvables: []pkgmodel.FormaeURI{parentURI},
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, nil, "cmd-cascade-skip", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// Start the parent
	updates := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, updates, 1)
	parent := asResourceUpdate(t, updates[0])
	assert.Equal(t, "parent", parent.DesiredState.Label)

	// Complete parent — both children become available
	parent.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, parent)
	require.NoError(t, err)

	// Start both children (marks them InProgress)
	children := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, children, 2)

	child1 := asResourceUpdate(t, children[0])
	child2 := asResourceUpdate(t, children[1])

	// Fail child1 — child2 is InProgress (not Ready), so cascade should NOT touch it
	child1.State = resource_update.ResourceUpdateStateFailed
	failedUpdates, err := updateDAGHelper(t, cs, child1)
	require.NoError(t, err)
	// Only child1 itself — child2 is in progress and should not be cascade-failed
	require.Len(t, failedUpdates, 1)

	// child2 should still be completable
	child2.State = resource_update.ResourceUpdateStateSuccess
	_, err = updateDAGHelper(t, cs, child2)
	require.NoError(t, err)

	assert.True(t, cs.IsComplete())
}

func TestChangeset_AvailableExecutableUpdates_NonRateLimitedShowsZeroCount(t *testing.T) {
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:    pkgmodel.Target{Label: "my-target", Namespace: "AWS"},
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(nil, targetUpdates, "cmd-nonrl", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	available := cs.AvailableExecutableUpdates()
	// Non-rate-limited updates should appear in the map with count 0
	count, exists := available["AWS"]
	assert.True(t, exists, "namespace should be present for non-rate-limited update")
	assert.Equal(t, 0, count, "non-rate-limited update should contribute 0 to token count")
}

func TestChangeset_TargetReplace_SplitsIntoDeleteAndCreate(t *testing.T) {
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:    pkgmodel.Target{Label: "aws-prod", Namespace: "AWS"},
			Operation: target_update.TargetOperationReplace,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(nil, targetUpdates, "cmd-1", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// Should have 2 nodes: delete + create
	assert.Len(t, cs.DAG.Nodes, 2)

	deleteNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://aws-prod/delete")]
	createNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://aws-prod/create")]
	require.NotNil(t, deleteNode, "expected delete node")
	require.NotNil(t, createNode, "expected create node")

	// Create depends on delete
	require.Len(t, createNode.Dependencies, 1)
	assert.Equal(t, deleteNode.URI, createNode.Dependencies[0].URI)
}

func TestChangeset_TargetReplace_ResourceEdges(t *testing.T) {
	bucketURI := pkgmodel.NewFormaeURI(util.NewID(), "")

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "bucket", Type: "AWS::S3::Bucket",
				Stack: "web", Ksuid: bucketURI.KSUID(),
				Target: "aws-prod",
			},
			Operation:  resource_update.OperationReplace,
			State:      resource_update.ResourceUpdateStateNotStarted,
			StackLabel: "web",
		},
	}
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:    pkgmodel.Target{Label: "aws-prod", Namespace: "AWS"},
			Operation: target_update.TargetOperationReplace,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-2", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// 4 nodes: resource delete, resource create, target delete, target create
	assert.Len(t, cs.DAG.Nodes, 4)

	targetDeleteNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://aws-prod/delete")]
	targetCreateNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://aws-prod/create")]
	require.NotNil(t, targetDeleteNode)
	require.NotNil(t, targetCreateNode)

	// Target delete should depend on resource delete(s)
	assert.GreaterOrEqual(t, len(targetDeleteNode.Dependencies), 1,
		"target delete should depend on at least one resource delete")

	// Resource create should depend on target create
	resourceCreateURI := createOperationURI(bucketURI, resource_update.OperationCreate)
	resourceCreateNode := cs.DAG.Nodes[resourceCreateURI]
	require.NotNil(t, resourceCreateNode, "expected resource create node")

	hasTargetCreateDep := false
	for _, dep := range resourceCreateNode.Dependencies {
		if dep.URI == targetCreateNode.URI {
			hasTargetCreateDep = true
		}
	}
	assert.True(t, hasTargetCreateDep,
		"resource create should depend on target create")
}

func TestChangeset_SyncReadsDoNotCreateDependencyEdges(t *testing.T) {
	vpcURI := pkgmodel.NewFormaeURI("vpc-ksuid", "")
	subnetURI := pkgmodel.NewFormaeURI("subnet-ksuid", "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Ksuid: vpcURI.KSUID(), Stack: "stack", Label: "vpc", Type: "AWS::EC2::VPC"},
			Operation:    resource_update.OperationRead,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "stack",
		},
		{
			DesiredState:         pkgmodel.Resource{Ksuid: subnetURI.KSUID(), Stack: "stack", Label: "subnet", Type: "AWS::EC2::Subnet"},
			Operation:            resource_update.OperationRead,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcURI},
		},
	}

	cs, err := buildChangesetForTest(updates, nil, "cmd-sync", pkgmodel.CommandSync, nil)
	assert.NoError(t, err)

	vpcNode := cs.DAG.Nodes[createOperationURI(vpcURI, resource_update.OperationRead)]
	subnetNode := cs.DAG.Nodes[createOperationURI(subnetURI, resource_update.OperationRead)]
	assert.NotNil(t, vpcNode)
	assert.NotNil(t, subnetNode)
	assert.Empty(t, vpcNode.Dependencies)
	assert.Empty(t, vpcNode.Dependents)
	assert.Empty(t, subnetNode.Dependencies)
	assert.Empty(t, subnetNode.Dependents)
}

// TestChangeset_CrashRecovery_SuccessParentBlocksChildren demonstrates the bug
// where including already-completed resources in a recovery changeset creates
// unresolvable dependency links. When a parent resource (VPC) already succeeded
// before a crash, it must be excluded from the recovery changeset so its
// children (Subnets) are immediately executable.
func TestChangeset_CrashRecovery_SuccessParentBlocksChildren(t *testing.T) {
	var (
		vpcKsuidURI    = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// Simulate crash recovery: VPC already succeeded, Subnet was interrupted.
	// If we include both in the changeset, the Subnet's dependency on the
	// VPC creates a link that can never be resolved (VPC is Success so it's
	// never returned by GetExecutableUpdates, and thus never processed
	// through UpdatePipeline to unlink its dependents).
	allUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuidURI.KSUID(),
			},
			Operation:  resource_update.OperationCreate,
			State:      resource_update.ResourceUpdateStateSuccess,
			StartTs:    util.TimeNow(),
			StackLabel: "test-stack",
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnetKsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	cs, err := buildChangesetForTest(allUpdates, nil, "test-crash-bug", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// BUG: Subnet is blocked because VPC (Success) is in the pipeline as an
	// upstream dependency but will never be processed.
	updates := cs.GetExecutableUpdates("AWS", 5)
	assert.Empty(t, updates, "Subnet should be blocked when Success parent is in the changeset")
}

// TestChangeset_CrashRecovery_FilteredPendingUpdatesOnly verifies the fix:
// when only pending (non-terminal) resource updates are included in the
// recovery changeset, dependent resources are immediately executable because
// their parent's resolved dependency reference points to a resource that
// doesn't exist in the changeset (so no link is created).
func TestChangeset_CrashRecovery_FilteredPendingUpdatesOnly(t *testing.T) {
	var (
		vpcKsuidURI    = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// Only include the pending Subnet — VPC (Success) is filtered out,
	// exactly as ReRunIncompleteCommands should do.
	pendingOnly := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "test-subnet",
				Type:  "AWS::EC2::Subnet",
				Stack: "test-stack",
				Ksuid: subnetKsuidURI.KSUID(),
			},
			Operation:            resource_update.OperationCreate,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StartTs:              util.TimeNow(),
			StackLabel:           "test-stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcKsuidURI},
		},
	}

	cs, err := buildChangesetForTest(pendingOnly, nil, "test-crash-fix", pkgmodel.CommandApply, nil)
	require.NoError(t, err)

	// VPC URI is in RemainingResolvables but not in the changeset, so no
	// dependency link is created. Subnet is immediately executable.
	updates := cs.GetExecutableUpdates("AWS", 5)
	require.Len(t, updates, 1, "Subnet should be immediately executable")
	assert.Equal(t, "test-subnet", updates[0].(*resource_update.ResourceUpdate).DesiredState.Label)
}

func TestChangeset_SyncReadFailureDoesNotCascade(t *testing.T) {
	vpcURI := pkgmodel.NewFormaeURI("vpc-ksuid", "")
	subnetURI := pkgmodel.NewFormaeURI("subnet-ksuid", "")

	updates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Ksuid: vpcURI.KSUID(), Stack: "stack", Label: "vpc", Type: "AWS::EC2::VPC"},
			Operation:    resource_update.OperationRead,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "stack",
		},
		{
			DesiredState:         pkgmodel.Resource{Ksuid: subnetURI.KSUID(), Stack: "stack", Label: "subnet", Type: "AWS::EC2::Subnet"},
			Operation:            resource_update.OperationRead,
			State:                resource_update.ResourceUpdateStateNotStarted,
			StackLabel:           "stack",
			RemainingResolvables: []pkgmodel.FormaeURI{vpcURI},
		},
	}

	cs, err := buildChangesetForTest(updates, nil, "cmd-sync", pkgmodel.CommandSync, nil)
	require.NoError(t, err)

	opURI := createOperationURI(vpcURI, resource_update.OperationRead)
	failed := cs.DAG.Nodes[opURI].Update
	failed.MarkFailed()
	failedUpdates, err := cs.UpdateDAG(opURI, failed)
	require.NoError(t, err)
	assert.Len(t, failedUpdates, 1)
	assert.Equal(t, failed.NodeURI(), failedUpdates[0].NodeURI())
	remaining := cs.GetExecutableUpdates("AWS", 10)
	require.Len(t, remaining, 1)
	assert.NotEqual(t, failed.NodeURI(), remaining[0].NodeURI())
}

// dagNodeForOp returns the DAG node for (URI, operation).
func dagNodeForOp(t *testing.T, dag *ExecutionDAG, uri pkgmodel.FormaeURI, op resource_update.OperationType) *DAGNode {
	t.Helper()
	opURI := createOperationURI(uri, op)
	node, ok := dag.Nodes[opURI]
	if !ok {
		t.Fatalf("DAG missing node %s", opURI)
	}
	return node
}

// hasDependency reports whether `child` has `parent` in its Dependencies list.
func hasDependency(child, parent *DAGNode) bool {
	for _, d := range child.Dependencies {
		if d == parent {
			return true
		}
	}
	return false
}

func TestBuildDeleteDependencies_AttachesToInvertsEdgeDirection(t *testing.T) {
	var (
		aURI = pkgmodel.NewFormaeURI("A1", "")
		bURI = pkgmodel.NewFormaeURI("B1", "")
	)

	build := func(t *testing.T, hint pkgmodel.FieldHint) *ExecutionDAG {
		t.Helper()
		aResource := pkgmodel.Resource{
			Ksuid: "A1", Label: "a", Type: "Test::A", Stack: "s",
			Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"f1": hint}},
			Properties: json.RawMessage(`{
				"f1": {"$ref":"formae://B1#/Out","$value":"v"}
			}`),
		}
		bResource := pkgmodel.Resource{
			Ksuid: "B1", Label: "b", Type: "Test::B", Stack: "s",
			Properties: json.RawMessage(`{}`),
		}

		aDelete := resource_update.ResourceUpdate{
			DesiredState:         aResource,
			Operation:            resource_update.OperationDelete,
			RemainingResolvables: []pkgmodel.FormaeURI{bURI},
		}
		bDelete := resource_update.ResourceUpdate{
			DesiredState: bResource,
			Operation:    resource_update.OperationDelete,
		}
		cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{aDelete, bDelete}, nil, "c1", pkgmodel.CommandApply, nil)
		if err != nil {
			t.Fatalf("NewChangeset: %v", err)
		}
		return cs.DAG
	}

	t.Run("no hint: B.delete waits for A.delete (reverse construction)", func(t *testing.T) {
		dag := build(t, pkgmodel.FieldHint{})
		bNode := dagNodeForOp(t, dag, bURI, resource_update.OperationDelete)
		aNode := dagNodeForOp(t, dag, aURI, resource_update.OperationDelete)
		if !hasDependency(bNode, aNode) {
			t.Fatalf("expected B.delete to depend on A.delete")
		}
		if hasDependency(aNode, bNode) {
			t.Fatalf("did not expect A.delete to depend on B.delete without AttachesTo")
		}
	})

	t.Run("AttachesTo: A.delete waits for B.delete (reachability order)", func(t *testing.T) {
		dag := build(t, pkgmodel.FieldHint{EdgeKind: pkgmodel.EdgeKindAttachesTo})
		bNode := dagNodeForOp(t, dag, bURI, resource_update.OperationDelete)
		aNode := dagNodeForOp(t, dag, aURI, resource_update.OperationDelete)
		if !hasDependency(aNode, bNode) {
			t.Fatalf("expected A.delete to depend on B.delete under AttachesTo")
		}
		if hasDependency(bNode, aNode) {
			t.Fatalf("did not expect B.delete to depend on A.delete under AttachesTo")
		}
	})
}

func TestBuildDeleteDependencies_MixedRefsOnOneResourceGetIndependentDirections(t *testing.T) {
	// A has two refs: f1 → B (AttachesTo), f2 → C (no hint).
	// Expected: A.delete waits for B.delete (AttachesTo) AND C.delete waits for A.delete (reverse construction).
	var (
		aURI = pkgmodel.NewFormaeURI("A1", "")
		bURI = pkgmodel.NewFormaeURI("B1", "")
		cURI = pkgmodel.NewFormaeURI("C1", "")
	)
	aResource := pkgmodel.Resource{
		Ksuid: "A1", Label: "a", Type: "Test::A", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"f1": {EdgeKind: pkgmodel.EdgeKindAttachesTo},
			// "f2" intentionally absent → plain construction
		}},
		Properties: json.RawMessage(`{
			"f1": {"$ref":"formae://B1#/Out","$value":"v"},
			"f2": {"$ref":"formae://C1#/Out","$value":"v"}
		}`),
	}
	bResource := pkgmodel.Resource{Ksuid: "B1", Label: "b", Type: "Test::B", Stack: "s", Properties: json.RawMessage(`{}`)}
	cResource := pkgmodel.Resource{Ksuid: "C1", Label: "c", Type: "Test::C", Stack: "s", Properties: json.RawMessage(`{}`)}

	cs, err := buildChangesetForTest(
		[]resource_update.ResourceUpdate{
			{DesiredState: aResource, Operation: resource_update.OperationDelete, RemainingResolvables: []pkgmodel.FormaeURI{bURI, cURI}},
			{DesiredState: bResource, Operation: resource_update.OperationDelete},
			{DesiredState: cResource, Operation: resource_update.OperationDelete},
		}, nil, "c2", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	aNode := dagNodeForOp(t, cs.DAG, aURI, resource_update.OperationDelete)
	bNode := dagNodeForOp(t, cs.DAG, bURI, resource_update.OperationDelete)
	cNode := dagNodeForOp(t, cs.DAG, cURI, resource_update.OperationDelete)

	if !hasDependency(aNode, bNode) {
		t.Errorf("A.delete should depend on B.delete (AttachesTo edge)")
	}
	if !hasDependency(cNode, aNode) {
		t.Errorf("C.delete should depend on A.delete (reverse construction edge)")
	}
	if hasDependency(bNode, aNode) {
		t.Errorf("B.delete should not depend on A.delete (AttachesTo inverts this edge)")
	}
}

func TestBuildDeleteDependencies_MissingHintFallsBackToConstructionReverse(t *testing.T) {
	// A refs B via f1 but A's Schema.Hints is empty/nil. Expected: default
	// construction-reverse behavior (B.delete waits for A.delete). No panic
	// when the hint map is nil for the stripped path.
	var (
		aURI = pkgmodel.NewFormaeURI("A1", "")
		bURI = pkgmodel.NewFormaeURI("B1", "")
	)
	aResource := pkgmodel.Resource{
		Ksuid: "A1", Label: "a", Type: "Test::A", Stack: "s",
		Schema:     pkgmodel.Schema{}, // no Hints map at all
		Properties: json.RawMessage(`{"f1": {"$ref":"formae://B1#/Out","$value":"v"}}`),
	}
	bResource := pkgmodel.Resource{Ksuid: "B1", Label: "b", Type: "Test::B", Stack: "s", Properties: json.RawMessage(`{}`)}

	cs, err := buildChangesetForTest(
		[]resource_update.ResourceUpdate{
			{DesiredState: aResource, Operation: resource_update.OperationDelete, RemainingResolvables: []pkgmodel.FormaeURI{bURI}},
			{DesiredState: bResource, Operation: resource_update.OperationDelete},
		}, nil, "c3", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	aNode := dagNodeForOp(t, cs.DAG, aURI, resource_update.OperationDelete)
	bNode := dagNodeForOp(t, cs.DAG, bURI, resource_update.OperationDelete)
	if !hasDependency(bNode, aNode) {
		t.Fatalf("missing hint should fall through to construction-reverse: B.delete must depend on A.delete")
	}
}

// TestBuildDeleteDependencies_RuntimeDependency_AddsChildEdges verifies that
// a runtimeDependency edge from consumer→producer also pulls in edges from
// the consumer to every containment child of the producer. The destroy order
// becomes: consumer first, then children, then producer.
func TestBuildDeleteDependencies_RuntimeDependency_AddsChildEdges(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI = pkgmodel.NewFormaeURI("PROD1", "")
		mtAURI      = pkgmodel.NewFormaeURI("MTA1", "")
		mtBURI      = pkgmodel.NewFormaeURI("MTB1", "")
	)

	// Consumer: TaskDef-like, references producer (FileSystem) via filesystemId
	// with a runtimeDependency edge.
	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://PROD1#/FileSystemId","$value":"fs-abc"}
		}`),
	}

	// Producer: FileSystem with a literal identifier.
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema: pkgmodel.Schema{
			Identifier: "FileSystemId",
		},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}

	// Two MountTargets pointing at the producer by literal FileSystemId.
	mtA := pkgmodel.Resource{
		Ksuid: "MTA1", Label: "mtA", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}
	mtB := pkgmodel.Resource{
		Ksuid: "MTB1", Label: "mtB", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}

	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationDelete, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		{DesiredState: producer, Operation: resource_update.OperationDelete},
		{DesiredState: mtA, Operation: resource_update.OperationDelete},
		{DesiredState: mtB, Operation: resource_update.OperationDelete},
	}, nil, "rt1", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationDelete)
	prodNode := dagNodeForOp(t, cs.DAG, producerURI, resource_update.OperationDelete)
	mtANode := dagNodeForOp(t, cs.DAG, mtAURI, resource_update.OperationDelete)
	mtBNode := dagNodeForOp(t, cs.DAG, mtBURI, resource_update.OperationDelete)

	// Default destroy edge: producer.delete depends on consumer.delete
	// (consumer first, producer second).
	if !hasDependency(prodNode, consNode) {
		t.Errorf("producer.delete should depend on consumer.delete (default destroy edge)")
	}
	// New runtimeDependency edges: each MountTarget.delete depends on
	// consumer.delete (consumer first, child second).
	if !hasDependency(mtANode, consNode) {
		t.Errorf("mountTargetA.delete should depend on consumer.delete (runtimeDependency child edge)")
	}
	if !hasDependency(mtBNode, consNode) {
		t.Errorf("mountTargetB.delete should depend on consumer.delete (runtimeDependency child edge)")
	}
	// Consumer must not depend on its own producer/children (no cycles).
	if hasDependency(consNode, prodNode) {
		t.Errorf("consumer.delete should NOT depend on producer.delete under runtimeDependency")
	}
	if hasDependency(consNode, mtANode) {
		t.Errorf("consumer.delete should NOT depend on mountTargetA.delete (cycle)")
	}
}

// TestBuildDeleteDependencies_RuntimeDependency_InstanceSafe verifies that
// runtimeDependency only pulls in containment children of the *referenced*
// producer instance, not arbitrary children of the same type.
func TestBuildDeleteDependencies_RuntimeDependency_InstanceSafe(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		fs1URI      = pkgmodel.NewFormaeURI("FS1", "")
		fs2URI      = pkgmodel.NewFormaeURI("FS2", "")
		mt1AURI     = pkgmodel.NewFormaeURI("MT1A", "")
		mt1BURI     = pkgmodel.NewFormaeURI("MT1B", "")
		mt2AURI     = pkgmodel.NewFormaeURI("MT2A", "")
		mt2BURI     = pkgmodel.NewFormaeURI("MT2B", "")
	)

	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://FS1#/FileSystemId","$value":"fs-1"}
		}`),
	}
	fs1 := pkgmodel.Resource{
		Ksuid: "FS1", Label: "fs1", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-1"}`),
	}
	fs2 := pkgmodel.Resource{
		Ksuid: "FS2", Label: "fs2", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-2"}`),
	}

	mountTarget := func(ksuid, label, fsID string) pkgmodel.Resource {
		return pkgmodel.Resource{
			Ksuid: ksuid, Label: label, Type: "Test::MountTarget", Stack: "s",
			Schema: pkgmodel.Schema{
				Parent: "Test::FileSystem",
				ParentMappings: []pkgmodel.ParentMapping{
					{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
				},
			},
			Properties: json.RawMessage(`{"FileSystemId":"` + fsID + `"}`),
		}
	}

	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationDelete, RemainingResolvables: []pkgmodel.FormaeURI{fs1URI}},
		{DesiredState: fs1, Operation: resource_update.OperationDelete},
		{DesiredState: fs2, Operation: resource_update.OperationDelete},
		{DesiredState: mountTarget("MT1A", "mt1A", "fs-1"), Operation: resource_update.OperationDelete},
		{DesiredState: mountTarget("MT1B", "mt1B", "fs-1"), Operation: resource_update.OperationDelete},
		{DesiredState: mountTarget("MT2A", "mt2A", "fs-2"), Operation: resource_update.OperationDelete},
		{DesiredState: mountTarget("MT2B", "mt2B", "fs-2"), Operation: resource_update.OperationDelete},
	}, nil, "rt2", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationDelete)
	mt1ANode := dagNodeForOp(t, cs.DAG, mt1AURI, resource_update.OperationDelete)
	mt1BNode := dagNodeForOp(t, cs.DAG, mt1BURI, resource_update.OperationDelete)
	mt2ANode := dagNodeForOp(t, cs.DAG, mt2AURI, resource_update.OperationDelete)
	mt2BNode := dagNodeForOp(t, cs.DAG, mt2BURI, resource_update.OperationDelete)
	_ = dagNodeForOp(t, cs.DAG, fs2URI, resource_update.OperationDelete) // verify present

	// FS1's children get the runtimeDependency edge from the consumer.
	if !hasDependency(mt1ANode, consNode) {
		t.Errorf("mt1A.delete should depend on consumer.delete (FS1's child)")
	}
	if !hasDependency(mt1BNode, consNode) {
		t.Errorf("mt1B.delete should depend on consumer.delete (FS1's child)")
	}
	// FS2's children must NOT get the edge — consumer doesn't reference FS2.
	if hasDependency(mt2ANode, consNode) {
		t.Errorf("mt2A.delete should NOT depend on consumer.delete (FS2's child, not referenced)")
	}
	if hasDependency(mt2BNode, consNode) {
		t.Errorf("mt2B.delete should NOT depend on consumer.delete (FS2's child, not referenced)")
	}
}

// TestBuildDeleteDependencies_RuntimeDependency_NoMatchingChildren_FallsBackToDefault
// verifies that when a runtimeDependency edge fires but no containment children
// of the producer are in the changeset, the default consumer→producer edge is
// still added and no spurious edges appear.
func TestBuildDeleteDependencies_RuntimeDependency_NoMatchingChildren_FallsBackToDefault(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI = pkgmodel.NewFormaeURI("PROD1", "")
	)

	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://PROD1#/FileSystemId","$value":"fs-abc"}
		}`),
	}
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}

	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationDelete, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		{DesiredState: producer, Operation: resource_update.OperationDelete},
	}, nil, "rt3", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationDelete)
	prodNode := dagNodeForOp(t, cs.DAG, producerURI, resource_update.OperationDelete)

	// Default destroy edge: producer.delete depends on consumer.delete.
	if !hasDependency(prodNode, consNode) {
		t.Errorf("producer.delete should still depend on consumer.delete (default edge under runtimeDependency)")
	}
	// No reverse edge.
	if hasDependency(consNode, prodNode) {
		t.Errorf("consumer.delete should NOT depend on producer.delete")
	}
	// Producer should have exactly one dependency (the consumer) — no spurious
	// edges from absent children.
	if got := len(prodNode.Dependencies); got != 1 {
		t.Errorf("producer.delete should have exactly 1 dependency, got %d", got)
	}
	// Consumer should have no dependencies (it's the entry point).
	if got := len(consNode.Dependencies); got != 0 {
		t.Errorf("consumer.delete should have no dependencies, got %d", got)
	}
}

// TestBuildCreateUpdateDependencies_RuntimeDependency_AddsChildEdges verifies
// that a runtimeDependency edge from consumer→producer on the create phase
// also pulls in edges from each containment child of the producer to the
// consumer. The create order becomes: producer first, then children, then
// consumer.
func TestBuildCreateUpdateDependencies_RuntimeDependency_AddsChildEdges(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI = pkgmodel.NewFormaeURI("PROD1", "")
		mtAURI      = pkgmodel.NewFormaeURI("MTA1", "")
		mtBURI      = pkgmodel.NewFormaeURI("MTB1", "")
	)

	// Consumer: TaskDef-like, references producer (FileSystem) via filesystemId
	// with a runtimeDependency edge.
	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://PROD1#/FileSystemId","$value":""}
		}`),
	}

	// Producer: FileSystem with a literal identifier.
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema: pkgmodel.Schema{
			Identifier: "FileSystemId",
		},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}

	// Two MountTargets whose FileSystemId Resolvable points at the producer
	// (the create-phase URI-based parent-reference shape).
	mtA := pkgmodel.Resource{
		Ksuid: "MTA1", Label: "mtA", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":{"$ref":"formae://PROD1#/FileSystemId","$value":""}}`),
	}
	mtB := pkgmodel.Resource{
		Ksuid: "MTB1", Label: "mtB", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":{"$ref":"formae://PROD1#/FileSystemId","$value":""}}`),
	}

	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		{DesiredState: producer, Operation: resource_update.OperationCreate},
		{DesiredState: mtA, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		{DesiredState: mtB, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
	}, nil, "rtc1", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationCreate)
	prodNode := dagNodeForOp(t, cs.DAG, producerURI, resource_update.OperationCreate)
	mtANode := dagNodeForOp(t, cs.DAG, mtAURI, resource_update.OperationCreate)
	mtBNode := dagNodeForOp(t, cs.DAG, mtBURI, resource_update.OperationCreate)

	// Natural create edge: consumer.create depends on producer.create
	// (producer first, consumer second).
	if !hasDependency(consNode, prodNode) {
		t.Errorf("consumer.create should depend on producer.create (natural create edge)")
	}
	// New runtimeDependency edges: consumer.create depends on each
	// MountTarget.create (children first, consumer second).
	if !hasDependency(consNode, mtANode) {
		t.Errorf("consumer.create should depend on mountTargetA.create (runtimeDependency child edge)")
	}
	if !hasDependency(consNode, mtBNode) {
		t.Errorf("consumer.create should depend on mountTargetB.create (runtimeDependency child edge)")
	}
	// Children/producer must not depend on the consumer (no cycles).
	if hasDependency(prodNode, consNode) {
		t.Errorf("producer.create should NOT depend on consumer.create")
	}
	if hasDependency(mtANode, consNode) {
		t.Errorf("mountTargetA.create should NOT depend on consumer.create (cycle)")
	}
	if hasDependency(mtBNode, consNode) {
		t.Errorf("mountTargetB.create should NOT depend on consumer.create (cycle)")
	}
}

// TestBuildCreateUpdateDependencies_RuntimeDependency_InstanceSafe verifies
// that on the create phase, runtimeDependency only pulls in containment
// children of the *referenced* producer instance, not arbitrary children of
// the same type.
func TestBuildCreateUpdateDependencies_RuntimeDependency_InstanceSafe(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		fs1URI      = pkgmodel.NewFormaeURI("FS1", "")
		fs2URI      = pkgmodel.NewFormaeURI("FS2", "")
		mt1AURI     = pkgmodel.NewFormaeURI("MT1A", "")
		mt1BURI     = pkgmodel.NewFormaeURI("MT1B", "")
		mt2AURI     = pkgmodel.NewFormaeURI("MT2A", "")
		mt2BURI     = pkgmodel.NewFormaeURI("MT2B", "")
	)

	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://FS1#/FileSystemId","$value":""}
		}`),
	}
	fs1 := pkgmodel.Resource{
		Ksuid: "FS1", Label: "fs1", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-1"}`),
	}
	fs2 := pkgmodel.Resource{
		Ksuid: "FS2", Label: "fs2", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-2"}`),
	}

	mountTarget := func(ksuid, label, fsKsuid string) pkgmodel.Resource {
		return pkgmodel.Resource{
			Ksuid: ksuid, Label: label, Type: "Test::MountTarget", Stack: "s",
			Schema: pkgmodel.Schema{
				Parent: "Test::FileSystem",
				ParentMappings: []pkgmodel.ParentMapping{
					{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
				},
			},
			Properties: json.RawMessage(`{"FileSystemId":{"$ref":"formae://` + fsKsuid + `#/FileSystemId","$value":""}}`),
		}
	}

	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{fs1URI}},
		{DesiredState: fs1, Operation: resource_update.OperationCreate},
		{DesiredState: fs2, Operation: resource_update.OperationCreate},
		{DesiredState: mountTarget("MT1A", "mt1A", "FS1"), Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{fs1URI}},
		{DesiredState: mountTarget("MT1B", "mt1B", "FS1"), Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{fs1URI}},
		{DesiredState: mountTarget("MT2A", "mt2A", "FS2"), Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{fs2URI}},
		{DesiredState: mountTarget("MT2B", "mt2B", "FS2"), Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{fs2URI}},
	}, nil, "rtc2", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationCreate)
	mt1ANode := dagNodeForOp(t, cs.DAG, mt1AURI, resource_update.OperationCreate)
	mt1BNode := dagNodeForOp(t, cs.DAG, mt1BURI, resource_update.OperationCreate)
	mt2ANode := dagNodeForOp(t, cs.DAG, mt2AURI, resource_update.OperationCreate)
	mt2BNode := dagNodeForOp(t, cs.DAG, mt2BURI, resource_update.OperationCreate)
	_ = dagNodeForOp(t, cs.DAG, fs2URI, resource_update.OperationCreate) // verify present

	// FS1's children get the runtimeDependency edge into the consumer.
	if !hasDependency(consNode, mt1ANode) {
		t.Errorf("consumer.create should depend on mt1A.create (FS1's child)")
	}
	if !hasDependency(consNode, mt1BNode) {
		t.Errorf("consumer.create should depend on mt1B.create (FS1's child)")
	}
	// FS2's children must NOT pull the consumer in — consumer doesn't reference FS2.
	if hasDependency(consNode, mt2ANode) {
		t.Errorf("consumer.create should NOT depend on mt2A.create (FS2's child, not referenced)")
	}
	if hasDependency(consNode, mt2BNode) {
		t.Errorf("consumer.create should NOT depend on mt2B.create (FS2's child, not referenced)")
	}
}

// TestBuildCreateUpdateDependencies_RuntimeDependency_NoMatchingChildren_FallsBackToDefault
// verifies that when a runtimeDependency edge fires on the create phase but no
// containment children of the producer are in the changeset, the default
// natural create edge (consumer depends on producer) is still added and no
// spurious child edges appear. An unrelated resource is included to confirm
// that the children-scan loop does not produce false positives.
func TestBuildCreateUpdateDependencies_RuntimeDependency_NoMatchingChildren_FallsBackToDefault(t *testing.T) {
	var (
		consumerURI  = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI  = pkgmodel.NewFormaeURI("PROD1", "")
		unrelatedURI = pkgmodel.NewFormaeURI("UNREL1", "")
	)

	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://PROD1#/FileSystemId","$value":""}
		}`),
	}
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}
	// Unrelated resource with a Resolvable that does NOT reference the producer.
	// Confirms the children-scan loop doesn't fire spurious edges for resources
	// whose parent type doesn't match the producer.
	unrelated := pkgmodel.Resource{
		Ksuid: "UNREL1", Label: "unrelated", Type: "Test::Other", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::SomethingElse",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "OtherId", ChildProperty: "OtherId"},
			},
		},
		Properties: json.RawMessage(`{"OtherId":"other-1"}`),
	}

	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		{DesiredState: producer, Operation: resource_update.OperationCreate},
		{DesiredState: unrelated, Operation: resource_update.OperationCreate},
	}, nil, "rtc3", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationCreate)
	prodNode := dagNodeForOp(t, cs.DAG, producerURI, resource_update.OperationCreate)
	unrelNode := dagNodeForOp(t, cs.DAG, unrelatedURI, resource_update.OperationCreate)

	// Default create edge: consumer.create depends on producer.create.
	if !hasDependency(consNode, prodNode) {
		t.Errorf("consumer.create should depend on producer.create (default edge under runtimeDependency)")
	}
	// No reverse edge.
	if hasDependency(prodNode, consNode) {
		t.Errorf("producer.create should NOT depend on consumer.create")
	}
	// Consumer should have exactly one dependency (the producer) — no spurious
	// edges from absent children or unrelated resources.
	if got := len(consNode.Dependencies); got != 1 {
		t.Errorf("consumer.create should have exactly 1 dependency, got %d", got)
	}
	// Consumer's only dependency is the producer.
	if consNode.Dependencies[0] != prodNode {
		t.Errorf("consumer.create's only dependency should be producer.create")
	}
	// Producer should have no dependencies (it's the entry point on the create
	// phase).
	if got := len(prodNode.Dependencies); got != 0 {
		t.Errorf("producer.create should have no dependencies, got %d", got)
	}
	// Unrelated resource should have no dependencies — it doesn't reference
	// anything in the changeset.
	if got := len(unrelNode.Dependencies); got != 0 {
		t.Errorf("unrelated.create should have no dependencies, got %d", got)
	}
}

// TestBuildDeleteDependencies_RuntimeDependency_MultipleRefsToSameProducer_PrefersRuntimeDependency
// guards against a per-ksuid map collision: when a consumer carries two refs
// pointing at the same producer KSUID but at different paths — one with
// EdgeKind=default and one with EdgeKind=runtimeDependency — the planner must
// honor the runtimeDependency annotation regardless of map iteration order.
//
// The original implementation keyed the hint lookup by bare KSUID, so the
// second path silently overwrote the first; whichever ordering map iteration
// produced determined which EdgeKind the planner saw. With the per-ref
// iteration in place, both hints fire and the runtimeDependency child edge
// is added every time.
func TestBuildDeleteDependencies_RuntimeDependency_MultipleRefsToSameProducer_PrefersRuntimeDependency(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI = pkgmodel.NewFormaeURI("PROD1", "")
		mtAURI      = pkgmodel.NewFormaeURI("MTA1", "")
	)

	// Consumer references PROD1 at two paths whose Resolvables point at
	// different producer properties (so they live under different map keys
	// in the resolver's internal index, exposing iteration-order sensitivity):
	//   - "fieldA" → formae://PROD1#/FieldA with EdgeKind=default
	//   - "fieldB" → formae://PROD1#/FieldB with EdgeKind=runtimeDependency
	// Both refs share the same producer KSUID — the collision case that the
	// per-ksuid lookup map exhibited.
	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"fieldA": {EdgeKind: pkgmodel.EdgeKindDefault},
			"fieldB": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"fieldA": {"$ref":"formae://PROD1#/FieldA","$value":"a"},
			"fieldB": {"$ref":"formae://PROD1#/FieldB","$value":"b"}
		}`),
	}
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}
	mtA := pkgmodel.Resource{
		Ksuid: "MTA1", Label: "mtA", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}

	// Repeat the build several times to exercise non-deterministic map
	// iteration — Go intentionally randomizes iteration order so one
	// invocation could happen to pick the "good" ordering by luck.
	for i := 0; i < 50; i++ {
		cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
			{DesiredState: consumer, Operation: resource_update.OperationDelete, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
			{DesiredState: producer, Operation: resource_update.OperationDelete},
			{DesiredState: mtA, Operation: resource_update.OperationDelete},
		}, nil, "rtmulti1", pkgmodel.CommandApply, nil)
		if err != nil {
			t.Fatalf("iteration %d NewChangeset: %v", i, err)
		}

		consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationDelete)
		mtANode := dagNodeForOp(t, cs.DAG, mtAURI, resource_update.OperationDelete)

		// The runtimeDependency child edge must fire regardless of which
		// path the planner visited first.
		if !hasDependency(mtANode, consNode) {
			t.Fatalf("iteration %d: mountTargetA.delete should depend on consumer.delete (runtimeDependency child edge must survive multi-ref collision)", i)
		}
	}
}

// TestBuildCreateUpdateDependencies_RuntimeDependency_MultipleRefsToSameProducer_PrefersRuntimeDependency
// is the create-side counterpart of the destroy-side test. Same
// shape: consumer carries two refs to the same producer KSUID — one default,
// one runtimeDependency — and the planner must honor the runtimeDependency
// hint regardless of map iteration order.
func TestBuildCreateUpdateDependencies_RuntimeDependency_MultipleRefsToSameProducer_PrefersRuntimeDependency(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI = pkgmodel.NewFormaeURI("PROD1", "")
		mtAURI      = pkgmodel.NewFormaeURI("MTA1", "")
	)

	// Consumer references PROD1 at two paths whose Resolvables point at
	// different producer properties (so they live under different map keys
	// in the resolver's internal index, exposing iteration-order
	// sensitivity):
	//   - "fieldA" → formae://PROD1#/FieldA with EdgeKind=default
	//   - "fieldB" → formae://PROD1#/FieldB with EdgeKind=runtimeDependency
	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"fieldA": {EdgeKind: pkgmodel.EdgeKindDefault},
			"fieldB": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"fieldA": {"$ref":"formae://PROD1#/FieldA","$value":""},
			"fieldB": {"$ref":"formae://PROD1#/FieldB","$value":""}
		}`),
	}
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}
	mtA := pkgmodel.Resource{
		Ksuid: "MTA1", Label: "mtA", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":{"$ref":"formae://PROD1#/FileSystemId","$value":""}}`),
	}

	for i := 0; i < 50; i++ {
		cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
			{DesiredState: consumer, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
			{DesiredState: producer, Operation: resource_update.OperationCreate},
			{DesiredState: mtA, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		}, nil, "rtcmulti1", pkgmodel.CommandApply, nil)
		if err != nil {
			t.Fatalf("iteration %d NewChangeset: %v", i, err)
		}

		consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationCreate)
		mtANode := dagNodeForOp(t, cs.DAG, mtAURI, resource_update.OperationCreate)

		// runtimeDependency child edge: consumer.create depends on mtA.create.
		if !hasDependency(consNode, mtANode) {
			t.Fatalf("iteration %d: consumer.create should depend on mountTargetA.create (runtimeDependency child edge must survive multi-ref collision)", i)
		}
	}
}

// TestBuildCreateUpdateDependencies_RuntimeDependency_UpdateChild_StillLinks
// guards against a hardcoded operation lookup in the create-side
// runtimeDependency branch: a containment child of the producer that's in
// the changeset as OperationUpdate (not OperationCreate) must still receive
// the runtimeDependency edge. The original implementation looked up the
// child's node under (uri, OperationCreate) regardless of the child's actual
// operation, silently dropping the edge for child updates.
func TestBuildCreateUpdateDependencies_RuntimeDependency_UpdateChild_StillLinks(t *testing.T) {
	var (
		consumerURI = pkgmodel.NewFormaeURI("CONS1", "")
		producerURI = pkgmodel.NewFormaeURI("PROD1", "")
		mtAURI      = pkgmodel.NewFormaeURI("MTA1", "")
	)

	consumer := pkgmodel.Resource{
		Ksuid: "CONS1", Label: "consumer", Type: "Test::TaskDef", Stack: "s",
		Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{
			"filesystemId": {EdgeKind: pkgmodel.EdgeKindRuntimeDependency},
		}},
		Properties: json.RawMessage(`{
			"filesystemId": {"$ref":"formae://PROD1#/FileSystemId","$value":""}
		}`),
	}
	producer := pkgmodel.Resource{
		Ksuid: "PROD1", Label: "fs", Type: "Test::FileSystem", Stack: "s",
		Schema:     pkgmodel.Schema{Identifier: "FileSystemId"},
		Properties: json.RawMessage(`{"FileSystemId":"fs-abc"}`),
	}
	mtA := pkgmodel.Resource{
		Ksuid: "MTA1", Label: "mtA", Type: "Test::MountTarget", Stack: "s",
		Schema: pkgmodel.Schema{
			Parent: "Test::FileSystem",
			ParentMappings: []pkgmodel.ParentMapping{
				{ParentProperty: "FileSystemId", ChildProperty: "FileSystemId"},
			},
		},
		Properties: json.RawMessage(`{"FileSystemId":{"$ref":"formae://PROD1#/FileSystemId","$value":""}}`),
	}

	// mtA is in the changeset as Update, not Create — the hardcoded lookup
	// would silently miss its node.
	cs, err := buildChangesetForTest([]resource_update.ResourceUpdate{
		{DesiredState: consumer, Operation: resource_update.OperationCreate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
		{DesiredState: producer, Operation: resource_update.OperationCreate},
		{DesiredState: mtA, Operation: resource_update.OperationUpdate, RemainingResolvables: []pkgmodel.FormaeURI{producerURI}},
	}, nil, "rtcupd1", pkgmodel.CommandApply, nil)
	if err != nil {
		t.Fatalf("NewChangeset: %v", err)
	}

	consNode := dagNodeForOp(t, cs.DAG, consumerURI, resource_update.OperationCreate)
	mtANode := dagNodeForOp(t, cs.DAG, mtAURI, resource_update.OperationUpdate)

	// runtimeDependency child edge must fire even when the child is in the
	// changeset as Update.
	if !hasDependency(consNode, mtANode) {
		t.Fatalf("consumer.create should depend on mountTargetA.update (runtimeDependency child edge must follow the child's actual operation, not a hardcoded Create)")
	}
}

// stubResolveDatastore is a minimal datastore.Datastore used by the synthetic
// Resolve-target tests. Only LoadTarget is exercised; every other method
// inherits the embedded interface and panics if called, which keeps the stub
// honest about what these tests actually depend on.
type stubResolveDatastore struct {
	datastore.Datastore
	targets   map[string]*pkgmodel.Target
	resources map[string]*pkgmodel.Resource
}

func (s *stubResolveDatastore) LoadTarget(label string) (*pkgmodel.Target, error) {
	return s.targets[label], nil
}

func (s *stubResolveDatastore) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	return s.resources[ksuid], nil
}

// opaqueRefConfig is a target config carrying a single opaque $ref, mirroring an
// otherwise-unchanged target whose secret must be resolved before dependent
// resource ops dispatch.
func opaqueRefConfig(refURI string) json.RawMessage {
	return json.RawMessage(`{"auth":{"$ref":"` + refURI + `","$visibility":"Opaque"}}`)
}

// jsonCredConfig mirrors how a Grafana target's basic-auth credentials persist
// when sourced from a JSON secret via secret.res.secretValue.json("<key>"): a
// $ref to the secret's value property plus a $json extraction path. Crucially the
// envelope's $visibility is "Clear" — opacity is derived from the source secret's
// FieldHint at resolve time, not stamped on the envelope — even though the
// resolved value is a credential the plugin needs to authenticate.
func jsonCredConfig(refURI string) json.RawMessage {
	return json.RawMessage(`{` +
		`"username":{"$ref":"` + refURI + `","$json":"username","$visibility":"Clear"},` +
		`"password":{"$ref":"` + refURI + `","$json":"password","$visibility":"Clear"}` +
		`}`)
}

func hasDependencyOn(node *DAGNode, uri pkgmodel.FormaeURI) bool {
	for _, dep := range node.Dependencies {
		if dep.URI == uri {
			return true
		}
	}
	return false
}

// TestNewChangeset_SynthesizesResolveNodeForUnchangedOpaqueTarget asserts that a
// resource op referencing a persisted target whose config carries an opaque $ref,
// with NO real target update in the changeset, produces exactly one synthetic
// Resolve node keyed target://<label>/resolve carrying the target's resolvables.
func TestNewChangeset_SynthesizesResolveNodeForUnchangedOpaqueTarget(t *testing.T) {
	const targetLabel = "aws-prod"
	refURI := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(refURI))},
	}}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: util.NewID(), Target: targetLabel},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, nil, "cmd-synth", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	resolveNode := cs.DAG.Nodes[pkgmodel.FormaeURI("target://"+targetLabel+"/resolve")]
	require.NotNil(t, resolveNode, "expected a synthetic Resolve node for the unchanged opaque target")

	tu, ok := resolveNode.Update.(*target_update.TargetUpdate)
	require.True(t, ok)
	assert.Equal(t, target_update.TargetOperationResolve, tu.Operation)
	require.Len(t, tu.RemainingResolvables, 1)
	assert.Equal(t, refURI, tu.RemainingResolvables[0])
}

// TestNewChangeset_NoSynthesisWhenTargetAlreadyHasUpdate asserts that a target
// already covered by a real Create/Update target update never gets a duplicate
// synthetic Resolve node.
func TestNewChangeset_NoSynthesisWhenTargetAlreadyHasUpdate(t *testing.T) {
	const targetLabel = "aws-prod"
	refURI := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(refURI))},
	}}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: util.NewID(), Target: targetLabel},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:    pkgmodel.Target{Label: targetLabel, Namespace: "AWS"},
			Operation: target_update.TargetOperationUpdate,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-nodup", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	assert.Nil(t, cs.DAG.Nodes[pkgmodel.FormaeURI("target://"+targetLabel+"/resolve")],
		"a target with a real update must not get a synthetic Resolve node")
}

// TestNewChangeset_NoSynthesisWhenTargetHasNoOpaqueRefs asserts that a persisted
// target whose config carries no $ref produces no synthetic Resolve node.
func TestNewChangeset_NoSynthesisWhenTargetHasNoOpaqueRefs(t *testing.T) {
	const targetLabel = "aws-prod"

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: json.RawMessage(`{"region":"us-east-1"}`)},
	}}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: util.NewID(), Target: targetLabel},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, nil, "cmd-norefs", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	assert.Nil(t, cs.DAG.Nodes[pkgmodel.FormaeURI("target://"+targetLabel+"/resolve")],
		"a target with no $ref config must not get a synthetic Resolve node")
}

// TestNewChangeset_SyntheticResolveNodeIsDependencyOfEveryResourceOp asserts that
// every resource op on the unchanged opaque target depends on the synthetic
// Resolve node, so none dispatch before the target config is resolved.
func TestNewChangeset_SyntheticResolveNodeIsDependencyOfEveryResourceOp(t *testing.T) {
	const targetLabel = "aws-prod"
	refURI := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(refURI))},
	}}

	createKsuid := util.NewID()
	updateKsuid := util.NewID()
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: createKsuid, Target: targetLabel},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			DesiredState: pkgmodel.Resource{Label: "queue", Type: "AWS::SQS::Queue", Stack: "s", Ksuid: updateKsuid, Target: targetLabel},
			Operation:    resource_update.OperationUpdate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, nil, "cmd-dep", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	resolveURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	require.NotNil(t, cs.DAG.Nodes[resolveURI])

	createNode := cs.DAG.Nodes[createOperationURI(resourceUpdates[0].URI(), resource_update.OperationCreate)]
	updateNode := cs.DAG.Nodes[createOperationURI(resourceUpdates[1].URI(), resource_update.OperationUpdate)]
	require.NotNil(t, createNode)
	require.NotNil(t, updateNode)

	assert.True(t, hasDependencyOn(createNode, resolveURI), "create op must depend on the Resolve node")
	assert.True(t, hasDependencyOn(updateNode, resolveURI), "update op must depend on the Resolve node")
}

// TestNewChangeset_RejectsTransitivelyOpaqueTargetReference asserts that resolving a
// target whose config opaquely $refs a secret source, where that source's OWN target
// also carries opaque $ref config, is rejected: bootstrapping a credential would
// require first bootstrapping another credential. The error must name both target
// labels and leak no plaintext secret material.
func TestNewChangeset_RejectsTransitivelyOpaqueTargetReference(t *testing.T) {
	const (
		t1            = "consumer"
		t2            = "secret-source-target"
		secretValue   = "super-secret-plaintext"
		outerRefKSUID = "sourceResourceKsuid00000001"
	)

	// T1's config opaquely refs a secret whose source resource lives on T2.
	t1RefURI := pkgmodel.NewFormaeURI(outerRefKSUID, "SecretString")
	// T2's OWN config also carries an opaque $ref (its credential itself needs resolving).
	t2InnerRefURI := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{
		targets: map[string]*pkgmodel.Target{
			t1: {Label: t1, Namespace: "AWS", Config: opaqueRefConfig(string(t1RefURI))},
			t2: {Label: t2, Namespace: "AWS", Config: opaqueRefConfig(string(t2InnerRefURI))},
		},
		// The source resource behind T1's ref lives on T2 (persisted, not in-command).
		resources: map[string]*pkgmodel.Resource{
			outerRefKSUID: {Label: "the-secret", Type: "AWS::SecretsManager::Secret", Ksuid: outerRefKSUID, Target: t2},
		},
	}

	// A resource op on T1 triggers T1's synthetic Resolve op.
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: util.NewID(), Target: t1},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	_, err := buildChangesetForTest(resourceUpdates, nil, "cmd-transitive-opaque", pkgmodel.CommandApply, ds)
	require.Error(t, err, "resolving a target whose secret source's target is itself opaque must be rejected")
	assert.Contains(t, err.Error(), t1, "error must name the referencing target")
	assert.Contains(t, err.Error(), t2, "error must name the source resource's target")
	assert.NotContains(t, err.Error(), secretValue, "error must not leak the plaintext secret")
	assert.NotContains(t, err.Error(), "$value", "error must not include any $value envelope")
}

// TestNewChangeset_AllowsOpaqueRefWhenSourceTargetIsPlain asserts the normal case:
// T1 opaquely $refs a secret whose source resource lives on T2, and T2's own config
// carries NO opaque $ref. This is a single-hop bootstrap and must be allowed.
func TestNewChangeset_AllowsOpaqueRefWhenSourceTargetIsPlain(t *testing.T) {
	const (
		t1            = "consumer"
		t2            = "secret-source-target"
		outerRefKSUID = "sourceResourceKsuid00000002"
	)

	t1RefURI := pkgmodel.NewFormaeURI(outerRefKSUID, "SecretString")

	ds := &stubResolveDatastore{
		targets: map[string]*pkgmodel.Target{
			t1: {Label: t1, Namespace: "AWS", Config: opaqueRefConfig(string(t1RefURI))},
			// T2 carries plain config only: no opaque bootstrapping needed.
			t2: {Label: t2, Namespace: "AWS", Config: json.RawMessage(`{"region":"us-east-1"}`)},
		},
		resources: map[string]*pkgmodel.Resource{
			outerRefKSUID: {Label: "the-secret", Type: "AWS::SecretsManager::Secret", Ksuid: outerRefKSUID, Target: t2},
		},
	}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: util.NewID(), Target: t1},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	_, err := buildChangesetForTest(resourceUpdates, nil, "cmd-plain-source-target", pkgmodel.CommandApply, ds)
	require.NoError(t, err, "an opaque ref to a secret on a plain-config target is the normal single-hop case")
}

// TestNewChangeset_TransitiveOpaqueSourceFoundInCommand asserts the guard also
// consults in-command resource ops for the secret source (a source being CREATED in
// THIS command is not yet persisted), following it to its target and rejecting when
// that target itself carries opaque config.
func TestNewChangeset_TransitiveOpaqueSourceFoundInCommand(t *testing.T) {
	const (
		t1            = "consumer"
		t2            = "secret-source-target"
		outerRefKSUID = "sourceResourceKsuid00000003"
	)

	t1RefURI := pkgmodel.NewFormaeURI(outerRefKSUID, "SecretString")
	t2InnerRefURI := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{
		targets: map[string]*pkgmodel.Target{
			t1: {Label: t1, Namespace: "AWS", Config: opaqueRefConfig(string(t1RefURI))},
			t2: {Label: t2, Namespace: "AWS", Config: opaqueRefConfig(string(t2InnerRefURI))},
		},
		// No persisted resource — the source is created in-command below.
		resources: map[string]*pkgmodel.Resource{},
	}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "bucket", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: util.NewID(), Target: t1},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			// The secret source resource, created in this same command, lives on T2.
			DesiredState: pkgmodel.Resource{Label: "the-secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: outerRefKSUID, Target: t2},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	_, err := buildChangesetForTest(resourceUpdates, nil, "cmd-in-command-source", pkgmodel.CommandApply, ds)
	require.Error(t, err, "in-command secret source on an opaque target must also be rejected")
	assert.Contains(t, err.Error(), t1)
	assert.Contains(t, err.Error(), t2)
}

// TestNewChangeset_MutuallyReferencingInCommandTargets_ReturnsCycleError covers two
// NEW in-command targets whose configs reference secrets hosted on each other. The
// transitive-opaque guard does not catch this — neither peer is persisted, so its
// LoadTarget returns nil and the guard skips it — yet the target-resolvable edges form
// a cycle (A.create → resource-on-B → B.create → resource-on-A → A.create). Without a
// full-graph cycle check this hangs the executor; NewChangeset must return an error.
func TestNewChangeset_MutuallyReferencingInCommandTargets_ReturnsCycleError(t *testing.T) {
	const (
		targetA = "target-a"
		targetB = "target-b"
	)
	// The secret sources: a resource created on A, and a resource created on B.
	resOnA := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")
	resOnB := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	// Both targets are NEW in-command creates: not persisted, so the transitive
	// guard's LoadTarget of the peer returns nil and skips.
	ds := &stubResolveDatastore{
		targets:   map[string]*pkgmodel.Target{},
		resources: map[string]*pkgmodel.Resource{},
	}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			// Source of the secret that target B references; lives on A.
			DesiredState: pkgmodel.Resource{Label: "secret-on-a", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: resOnA.KSUID(), Target: targetA},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			// Source of the secret that target A references; lives on B.
			DesiredState: pkgmodel.Resource{Label: "secret-on-b", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: resOnB.KSUID(), Target: targetB},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	// A's config refers to the secret hosted on B; B's config refers to the secret hosted on A.
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:               pkgmodel.Target{Label: targetA, Namespace: "AWS", Config: opaqueRefConfig(string(resOnB))},
			Operation:            target_update.TargetOperationCreate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{resOnB},
		},
		{
			Target:               pkgmodel.Target{Label: targetB, Namespace: "AWS", Config: opaqueRefConfig(string(resOnA))},
			Operation:            target_update.TargetOperationCreate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{resOnA},
		},
	}

	_, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-mutual-cycle", pkgmodel.CommandApply, ds)
	require.Error(t, err, "mutually referencing in-command targets form a cycle and must be rejected, not hang")
}

// TestNewChangeset_SelfReferentialTarget_ReturnsCycleError covers a target whose config
// references a secret hosted on a resource on the SAME target. The transitive-opaque
// guard explicitly skips a self-reference, but the target-resolvable edges still form a
// self-cycle (target.create → resource-on-target → target.create). NewChangeset must
// return an error rather than let the executor hang.
func TestNewChangeset_SelfReferentialTarget_ReturnsCycleError(t *testing.T) {
	const targetLabel = "self-target"
	secretOnSelf := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{
		targets:   map[string]*pkgmodel.Target{},
		resources: map[string]*pkgmodel.Resource{},
	}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: secretOnSelf.KSUID(), Target: targetLabel},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	targetUpdates := []target_update.TargetUpdate{
		{
			Target:               pkgmodel.Target{Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(secretOnSelf))},
			Operation:            target_update.TargetOperationCreate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{secretOnSelf},
		},
	}

	_, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-self-cycle", pkgmodel.CommandApply, ds)
	require.Error(t, err, "a self-referential target forms a self-cycle and must be rejected, not hang")
}

// TestNewChangeset_ValidTargetResolvableEdge_NoFalsePositive asserts the full-graph
// cycle check does not reject a legitimate target-resolvable edge: target A resolves
// from a resource hosted on target B, and B has no back-edge to A. This acyclic shape
// must build without error.
func TestNewChangeset_ValidTargetResolvableEdge_NoFalsePositive(t *testing.T) {
	const (
		targetA = "consumer"
		targetB = "provider"
	)
	// The secret source lives on B; A's config references it. B references nothing back.
	secretOnB := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{
		targets:   map[string]*pkgmodel.Target{},
		resources: map[string]*pkgmodel.Resource{},
	}

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{Label: "secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: secretOnB.KSUID(), Target: targetB},
			Operation:    resource_update.OperationCreate,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	targetUpdates := []target_update.TargetUpdate{
		{
			Target:               pkgmodel.Target{Label: targetA, Namespace: "AWS", Config: opaqueRefConfig(string(secretOnB))},
			Operation:            target_update.TargetOperationCreate,
			State:                target_update.TargetUpdateStateNotStarted,
			RemainingResolvables: []pkgmodel.FormaeURI{secretOnB},
		},
		{
			// B is a plain create with no resolvables — no back-edge to A.
			Target:    pkgmodel.Target{Label: targetB, Namespace: "AWS", Config: json.RawMessage(`{"region":"us-east-1"}`)},
			Operation: target_update.TargetOperationCreate,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-valid-resolvable", pkgmodel.CommandApply, ds)
	require.NoError(t, err, "a valid acyclic target-resolvable edge must not be rejected")
	assert.False(t, cs.DAG.HasCycles(), "the built graph must be acyclic")
}

// TestNewChangeset_SynthesizesResolveForDeleteOnlyOpaqueTarget asserts that a target
// carrying opaque $ref config gets a synthetic Resolve node even when the only target
// update for that target is a Delete op. A Delete TU does not carry re-resolved config,
// so resource-delete ops on that target must still get a Resolve dependency or they
// dispatch with an unresolved credential.
func TestNewChangeset_SynthesizesResolveForDeleteOnlyOpaqueTarget(t *testing.T) {
	const targetLabel = "t1"
	refURI := pkgmodel.NewFormaeURI(util.NewID(), "SecretString")

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(refURI))},
	}}

	deleteKsuid := util.NewID()
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			PriorState:   pkgmodel.Resource{Label: "res", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: deleteKsuid, Target: targetLabel},
			DesiredState: pkgmodel.Resource{Label: "res", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: deleteKsuid, Target: targetLabel},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	// The only TU for t1 is a Delete — it must NOT suppress Resolve synthesis.
	targetUpdates := []target_update.TargetUpdate{
		{
			Target:    pkgmodel.Target{Label: targetLabel, Namespace: "AWS"},
			Operation: target_update.TargetOperationDelete,
			State:     target_update.TargetUpdateStateNotStarted,
		},
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-delete-resolve", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	resolveURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	resolveNode := cs.DAG.Nodes[resolveURI]
	require.NotNil(t, resolveNode, "expected a synthetic Resolve node for the delete-only opaque target")

	tu, ok := resolveNode.Update.(*target_update.TargetUpdate)
	require.True(t, ok)
	assert.Equal(t, target_update.TargetOperationResolve, tu.Operation)
	require.Len(t, tu.RemainingResolvables, 1)
	assert.Equal(t, refURI, tu.RemainingResolvables[0])

	deleteNode := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(deleteKsuid, ""), resource_update.OperationDelete)
	assert.True(t, hasDependencyOn(deleteNode, resolveURI),
		"resource-delete must depend on the Resolve node so it dispatches with resolved config")
}

// TestNewChangeset_SynthesizesResolveForCascadeDeletedOpaqueTarget asserts that a
// target cascade-deleted because its $ref source resource is torn down in the same
// command DOES get a synthetic Resolve node. The cascade orders the source-secret
// delete AFTER the target delete, so the secret is still present when the target
// resolves — the resource deletes on that target must dispatch with the resolved
// credential, exactly like a plain delete.
//
// It also asserts the DAG ordering holds and is acyclic:
//
//	Resolve(target) → resource-delete → target-delete → secret-delete
func TestNewChangeset_SynthesizesResolveForCascadeDeletedOpaqueTarget(t *testing.T) {
	const targetLabel = "consumer"
	// The secret source resource lives on some other target; the cascade target's
	// config $refs it. Deleting the secret is what triggers the cascade.
	secretKsuid := util.NewID()
	refURI := pkgmodel.NewFormaeURI(secretKsuid, "SecretString")

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(refURI))},
	}}

	// A resource-delete on the cascade target (the deployment being torn down).
	deploymentKsuid := util.NewID()
	// The source secret's own delete op — it must run LAST (after the target delete).
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			PriorState:   pkgmodel.Resource{Label: "deployment", Type: "AWS::S3::Bucket", Stack: "consumer-stack", Ksuid: deploymentKsuid, Target: targetLabel},
			DesiredState: pkgmodel.Resource{Label: "deployment", Type: "AWS::S3::Bucket", Stack: "consumer-stack", Ksuid: deploymentKsuid, Target: targetLabel},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "consumer-stack",
		},
		{
			PriorState:   pkgmodel.Resource{Label: "secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: secretKsuid, Target: "provider"},
			DesiredState: pkgmodel.Resource{Label: "secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: secretKsuid, Target: "provider"},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	// A cascade Delete TU: the target's $ref source is being deleted in this command.
	// ExistingTarget carries the opaque $ref config so the reversed delete edge and the
	// synthetic Resolve can both find the resolvable.
	targetUpdates := []target_update.TargetUpdate{
		target_update.NewTargetUpdateForCascadeDelete(
			&pkgmodel.Target{Label: targetLabel, Namespace: "AWS", Config: opaqueRefConfig(string(refURI))},
			"secret",
		),
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-cascade-resolve", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	// A synthetic Resolve node must exist for the cascade target.
	resolveURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	resolveNode := cs.DAG.Nodes[resolveURI]
	require.NotNil(t, resolveNode, "a cascade-deleted opaque target must get a synthetic Resolve node")

	tu, ok := resolveNode.Update.(*target_update.TargetUpdate)
	require.True(t, ok)
	assert.Equal(t, target_update.TargetOperationResolve, tu.Operation)
	require.Len(t, tu.RemainingResolvables, 1)
	assert.Equal(t, refURI, tu.RemainingResolvables[0])

	// The deployment resource-delete depends on the Resolve node.
	deploymentDelete := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(deploymentKsuid, ""), resource_update.OperationDelete)
	assert.True(t, hasDependencyOn(deploymentDelete, resolveURI),
		"resource-delete on the cascade target must depend on the Resolve node")

	// The source secret-delete depends on the cascade target-delete (reversed edge),
	// so the secret is still present at Resolve time.
	targetDeleteURI := pkgmodel.FormaeURI("target://" + targetLabel + "/delete")
	secretDelete := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(secretKsuid, ""), resource_update.OperationDelete)
	assert.True(t, hasDependencyOn(secretDelete, targetDeleteURI),
		"source secret-delete must wait for the cascade target-delete so the secret is present at Resolve time")

	assert.False(t, cs.DAG.HasCycles(), "the cascade delete graph must be acyclic")
}

// TestNewChangeset_SynthesizesResolveForCascadeDeletedJSONCredTarget asserts the
// headline case for the delete path: a cascade-deleted target whose
// credentials come from a JSON secret via .json() (persisted as $ref+$json with
// $visibility "Clear") must still get a synthetic Resolve, and its resource-delete
// ops must depend on that Resolve — otherwise the plugin dispatches the teardown
// with unresolved credentials and the real cloud resource is orphaned. This is the
// same shape as the opaque-$ref cascade test, but with .json()-derived Clear
// credentials rather than a directly-opaque $ref.
func TestNewChangeset_SynthesizesResolveForCascadeDeletedJSONCredTarget(t *testing.T) {
	const targetLabel = "consumer"
	secretKsuid := util.NewID()
	refURI := pkgmodel.NewFormaeURI(secretKsuid, "SecretString")

	// The source secret resource: its SecretString property is opaque at rest, so
	// the .json() credential refs deriving from it are really secrets even though
	// their consumer-side envelopes are Clear.
	ds := &stubResolveDatastore{
		targets: map[string]*pkgmodel.Target{
			targetLabel: {Label: targetLabel, Namespace: "GRAFANA", Config: jsonCredConfig(string(refURI))},
		},
		resources: map[string]*pkgmodel.Resource{
			secretKsuid: {
				Label: "secret", Type: "AWS::SecretsManager::Secret", Ksuid: secretKsuid, Target: "provider",
				Properties: json.RawMessage(`{"SecretString":{"$value":"deadbeef","$visibility":"Opaque","$hashed":true,"$strategy":"Update"}}`),
			},
		},
	}

	// A resource-delete on the cascade target (the folder being torn down), plus
	// the source secret's own delete op (must run last).
	folderKsuid := util.NewID()
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			PriorState:   pkgmodel.Resource{Label: "folder", Type: "GRAFANA::Core::Folder", Stack: "consumer-stack", Ksuid: folderKsuid, Target: targetLabel},
			DesiredState: pkgmodel.Resource{Label: "folder", Type: "GRAFANA::Core::Folder", Stack: "consumer-stack", Ksuid: folderKsuid, Target: targetLabel},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "consumer-stack",
		},
		{
			PriorState:   pkgmodel.Resource{Label: "secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: secretKsuid, Target: "provider"},
			DesiredState: pkgmodel.Resource{Label: "secret", Type: "AWS::SecretsManager::Secret", Stack: "s", Ksuid: secretKsuid, Target: "provider"},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	targetUpdates := []target_update.TargetUpdate{
		target_update.NewTargetUpdateForCascadeDelete(
			&pkgmodel.Target{Label: targetLabel, Namespace: "GRAFANA", Config: jsonCredConfig(string(refURI))},
			"secret",
		),
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-cascade-jsoncred", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	// The cascade target must get a synthetic Resolve carrying its credential ref,
	// so the folder-delete can dispatch with resolved credentials.
	resolveURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	resolveNode := cs.DAG.Nodes[resolveURI]
	require.NotNil(t, resolveNode, "a cascade-deleted target whose creds come via .json() must get a synthetic Resolve node")

	tu, ok := resolveNode.Update.(*target_update.TargetUpdate)
	require.True(t, ok)
	assert.Equal(t, target_update.TargetOperationResolve, tu.Operation)
	require.NotEmpty(t, tu.RemainingResolvables, "the synthetic Resolve must carry the credential resolvable")
	assert.Contains(t, tu.RemainingResolvables, refURI)

	// The folder resource-delete depends on the Resolve so it dispatches with creds.
	folderDelete := dagNodeForOp(t, cs.DAG, pkgmodel.NewFormaeURI(folderKsuid, ""), resource_update.OperationDelete)
	assert.True(t, hasDependencyOn(folderDelete, resolveURI),
		"resource-delete on the cascade target must depend on the Resolve node")

	assert.False(t, cs.DAG.HasCycles(), "the cascade delete graph must be acyclic")
}

// TestNewChangeset_CascadeDeleteOpaqueTargetWithNonOpaqueRef_SyntheticResolveOpaqueOnly asserts
// that when a cascade-deleted target carries BOTH an opaque $ref AND a non-opaque
// cross-resource $ref whose source is being deleted in the same command, the synthetic
// Resolve node for that target includes ONLY the opaque resolvable. Including the
// non-opaque ref would attempt to read a vanishing source; the non-opaque ref carries
// its stored value in the resource's NativeID and does not need in-flight resolution.
func TestNewChangeset_CascadeDeleteOpaqueTargetWithNonOpaqueRef_SyntheticResolveOpaqueOnly(t *testing.T) {
	const targetLabel = "consumer"

	// The opaque ref: a secret credential still present at Resolve time (deleted after the target).
	secretKsuid := util.NewID()
	opaqueRef := pkgmodel.NewFormaeURI(secretKsuid, "SecretString")

	// The non-opaque ref: a cross-resource ref whose source is being deleted in the same command.
	vanishingKsuid := util.NewID()
	nonOpaqueRef := pkgmodel.NewFormaeURI(vanishingKsuid, "Arn")

	// Mixed config: both an opaque and a non-opaque $ref.
	mixedConfig := json.RawMessage(`{
		"auth":   {"$ref":"` + string(opaqueRef) + `","$visibility":"Opaque"},
		"roleArn":{"$ref":"` + string(nonOpaqueRef) + `","$value":"arn:aws:iam::123:role/r"}
	}`)

	ds := &stubResolveDatastore{targets: map[string]*pkgmodel.Target{
		targetLabel: {Label: targetLabel, Namespace: "AWS", Config: mixedConfig},
	}}

	deploymentKsuid := util.NewID()
	resourceUpdates := []resource_update.ResourceUpdate{
		{
			PriorState:   pkgmodel.Resource{Label: "deployment", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: deploymentKsuid, Target: targetLabel},
			DesiredState: pkgmodel.Resource{Label: "deployment", Type: "AWS::S3::Bucket", Stack: "s", Ksuid: deploymentKsuid, Target: targetLabel},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
		{
			// The vanishing non-opaque source being deleted in this command.
			PriorState:   pkgmodel.Resource{Label: "role", Type: "AWS::IAM::Role", Stack: "s", Ksuid: vanishingKsuid, Target: "provider"},
			DesiredState: pkgmodel.Resource{Label: "role", Type: "AWS::IAM::Role", Stack: "s", Ksuid: vanishingKsuid, Target: "provider"},
			Operation:    resource_update.OperationDelete,
			State:        resource_update.ResourceUpdateStateNotStarted,
			StackLabel:   "s",
		},
	}

	// Cascade Delete: the target's opaque $ref source is being torn down.
	targetUpdates := []target_update.TargetUpdate{
		target_update.NewTargetUpdateForCascadeDelete(
			&pkgmodel.Target{Label: targetLabel, Namespace: "AWS", Config: mixedConfig},
			"secret",
		),
	}

	cs, err := buildChangesetForTest(resourceUpdates, targetUpdates, "cmd-mixed-visibility", pkgmodel.CommandApply, ds)
	require.NoError(t, err)

	resolveURI := pkgmodel.FormaeURI("target://" + targetLabel + "/resolve")
	resolveNode := cs.DAG.Nodes[resolveURI]
	require.NotNil(t, resolveNode, "a cascade-deleted target with an opaque $ref must get a synthetic Resolve node")

	tu, ok := resolveNode.Update.(*target_update.TargetUpdate)
	require.True(t, ok)
	assert.Equal(t, target_update.TargetOperationResolve, tu.Operation)

	// The synthetic Resolve must carry ONLY the opaque resolvable, not the
	// non-opaque one whose source is being deleted in this command.
	require.Len(t, tu.RemainingResolvables, 1,
		"only the opaque resolvable must be included; the non-opaque vanishing source must be excluded")
	assert.Equal(t, opaqueRef, tu.RemainingResolvables[0],
		"the single resolvable must be the opaque $ref, not the non-opaque one")
}
