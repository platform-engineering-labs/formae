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

func TestNewChangesetFromResourceUpdates_Replace_FullExecution(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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

	// Print initial pipeline state

	// Step 1: Get first executable updates - should be subnet-1 delete (has dependency)
	require.Equal(t, map[string]int{"AWS": 1}, changeset.AvailableExecutableUpdates())
	executableUpdates := changeset.GetExecutableUpdates("AWS", 2)

	// Verify first step: should be subnet-1 delete
	if len(executableUpdates) != 1 {
		t.Fatalf("Expected 1 executable update, got %d", len(executableUpdates))
	}
	if executableUpdates[0].Resource.Label != "test-subnet-1" || executableUpdates[0].Operation != resource_update.OperationDelete {
		t.Fatalf("Expected subnet-1 delete, got %s %s", executableUpdates[0].Resource.Label, executableUpdates[0].Operation)
	}

	// Step 2: Complete subnet-1 delete
	executableUpdates[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(executableUpdates[0])
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	nextUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// Verify second step: should be VPC delete
	if len(nextUpdates) != 1 {
		t.Fatalf("Expected 1 next update, got %d", len(nextUpdates))
	}
	if nextUpdates[0].Resource.Label != "test-vpc" || nextUpdates[0].Operation != resource_update.OperationDelete {
		t.Fatalf("Expected vpc delete, got %s %s", nextUpdates[0].Resource.Label, nextUpdates[0].Operation)
	}

	// Step 3: Complete VPC delete
	nextUpdates[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(nextUpdates[0])
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Verify third step: should be VPC create
	if len(nextUpdates2) != 1 {
		t.Fatalf("Expected 1 next update, got %d", len(nextUpdates2))
	}
	if nextUpdates2[0].Resource.Label != "test-vpc" || nextUpdates2[0].Operation != resource_update.OperationCreate {
		t.Fatalf("Expected vpc create, got %s %s", nextUpdates2[0].Resource.Label, nextUpdates2[0].Operation)
	}

	// Step 4: Complete VPC create
	nextUpdates2[0].State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(nextUpdates2[0])
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

	// Verify fourth step: should be both subnet creates (parallel)
	if len(nextUpdates3) != 2 {
		t.Fatalf("Expected 2 next updates, got %d", len(nextUpdates3))
	}

	// Verify both are subnet creates
	subnetCreateCount := 0
	for _, update := range nextUpdates3 {
		if (update.Resource.Label == "test-subnet-1" || update.Resource.Label == "test-subnet-2") &&
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
		_, err := changeset.UpdatePipeline(update)
		if err != nil {
			t.Fatalf("Error updating pipeline: %v", err)
		}
	}

	// Verify completion
	if !changeset.IsComplete() {
		t.Fatalf("Expected changeset to be complete")
	}
}

func TestNewChangesetFromResourceUpdates_Replace2(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		vpc2KsuidURI    = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
		if update.Resource.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if update.Resource.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
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
		if update.Resource.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			subnet1Delete = update
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating pipeline after subnet-1 delete: %v", err)
	}

	nextUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// After subnet-1 delete, test-vpc delete should now be executable
	// Plus test-vpc-2 create should still be available
	foundVpcDelete := false
	for _, update := range nextUpdates {
		if update.Resource.Label == "test-vpc" && update.Operation == resource_update.OperationDelete {
			foundVpcDelete = true
			break
		}
	}

	if !foundVpcDelete {
		t.Fatalf("Expected test-vpc delete to be executable after subnet-1 delete")
	}
}

func TestNewChangesetFromResourceUpdates_Replace3_With_RootNode_Contains_Resolvable_Not_In_Changeset(t *testing.T) {
	var (
		vpc2KsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		vpcKsuidURI      = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI  = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI  = pkgmodel.NewFormaeURI(util.NewID(), "")
		externalVpcKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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

	// Print initial pipeline state for debugging

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
		if update.Resource.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if update.Resource.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
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
		if update.Resource.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			vpc2Create = update
			break
		}
	}

	vpc2Create.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(vpc2Create)
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	// Complete the delete chain to verify the create chain behavior
	// Step 1: Complete subnet-1 delete
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.Resource.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			subnet1Delete = update
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Should have vpc delete now
	foundVpcDelete := false
	for _, update := range nextUpdates2 {
		if update.Resource.Label == "test-vpc" && update.Operation == resource_update.OperationDelete {
			foundVpcDelete = true
			// Complete vpc delete
			update.State = resource_update.ResourceUpdateStateSuccess
			_, err := changeset.UpdatePipeline(update)
			if err != nil {
				t.Fatalf("Error updating pipeline: %v", err)
			}

			// After vpc delete, should have vpc create
			nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

			// Should have vpc create available
			foundVpcCreate := false
			for _, update2 := range nextUpdates3 {
				if update2.Resource.Label == "test-vpc" && update2.Operation == resource_update.OperationCreate {
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

func TestNewChangesetFromResourceUpdates_Replace4(t *testing.T) {
	var (
		vpc2KsuidURI    = pkgmodel.NewFormaeURI(util.NewID(), "")
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
		if update.Resource.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			foundVpc2Create = true
		}
		if update.Resource.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			foundSubnet1Delete = true
		}
		// Verify subnet-2 is NOT executable
		if update.Resource.Label == "test-subnet-2" {
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
		if update.Resource.Label == "test-vpc-2" && update.Operation == resource_update.OperationCreate {
			vpc2Create = update
			break
		}
	}

	vpc2Create.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(vpc2Create)
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	// Step 2: Complete test-subnet-1 delete
	var subnet1Delete *resource_update.ResourceUpdate
	for _, update := range executableUpdates {
		if update.Resource.Label == "test-subnet-1" && update.Operation == resource_update.OperationDelete {
			subnet1Delete = update
			break
		}
	}

	subnet1Delete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(subnet1Delete)
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	nextUpdates2 := changeset.GetExecutableUpdates("AWS", 5)

	// Step 3: Complete test-vpc delete
	var vpcDelete *resource_update.ResourceUpdate
	for _, update := range nextUpdates2 {
		if update.Resource.Label == "test-vpc" && update.Operation == resource_update.OperationDelete {
			vpcDelete = update
			break
		}
	}

	if vpcDelete == nil {
		t.Fatalf("Expected test-vpc delete to be executable after subnet-1 delete")
		return
	}

	vpcDelete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(vpcDelete)
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
		return
	}

	nextUpdates3 := changeset.GetExecutableUpdates("AWS", 5)

	// Step 4: Complete test-vpc create
	var vpcCreate *resource_update.ResourceUpdate
	for _, update := range nextUpdates3 {
		if update.Resource.Label == "test-vpc" && update.Operation == resource_update.OperationCreate {
			vpcCreate = update
			break
		}
	}

	if vpcCreate == nil {
		t.Fatalf("Expected test-vpc create to be executable after vpc delete")
		return
	}

	vpcCreate.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(vpcCreate)
	if err != nil {
		t.Fatalf("Error updating pipeline: %v", err)
	}

	finalUpdates := changeset.GetExecutableUpdates("AWS", 5)

	// Step 5: Now both subnet creates should be executable
	// (subnet-1 depends only on vpc, subnet-2 depends on both vpc and vpc-2 which are both complete)
	expectedSubnetCreates := 2
	subnetCreateCount := 0

	for _, update := range finalUpdates {
		if (update.Resource.Label == "test-subnet-1" || update.Resource.Label == "test-subnet-2") &&
			update.Operation == resource_update.OperationCreate {
			subnetCreateCount++
		}
	}

	if subnetCreateCount != expectedSubnetCreates {
		t.Fatalf("Expected %d subnet creates, got %d", expectedSubnetCreates, subnetCreateCount)
	}
}

func TestNewChangesetFromResourceUpdates_Different_Types_Same_Label(t *testing.T) {
	var (
		vpcKsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdateInitial := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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

	executables := changeset.getExecutableUpdates()
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
	assert.Equal(t, "AWS::EC2::Subnet", subnetDelete.Resource.Type)
	assert.Equal(t, "AWS::EC2::VPC", vpcCreate.Resource.Type)

	// Complete delete op
	subnetDelete.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(subnetDelete)
	assert.NoError(t, err)

	// Complete create op
	vpcCreate.State = resource_update.ResourceUpdateStateSuccess
	_, err = changeset.UpdatePipeline(vpcCreate)
	assert.NoError(t, err)

	noUpdates := changeset.GetExecutableUpdates("AWS", 5)
	assert.Len(t, noUpdates, 0, "Expected no further updates after both operations complete")
	assert.True(t, changeset.IsComplete())
}

func TestNewChangesetFromResourceUpdates_Destroy(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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

	executables := changeset.getExecutableUpdates()
	assert.NotNil(t, executables)
}

func TestChangeset_RecursiveFailureCascading(t *testing.T) {
	var (
		vpcKsuidURI   = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuid   = pkgmodel.NewFormaeURI(util.NewID(), "")
		instanceKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// Dependency chain: VPC -> Subnet -> Instance
	vpcUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "test-stack",
			Ksuid: vpcKsuidURI.KSUID(),
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateNotStarted,
	}
	subnetUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
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
		Resource: pkgmodel.Resource{
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
		failedLabels = append(failedLabels, update.Resource.Label)
	}

	// both subnet AND instance should be failed
	assert.Contains(t, failedLabels, "subnet", "Subnet should be failed due to VPC failure")
	assert.Contains(t, failedLabels, "instance", "Instance should be failed due to recursive cascading")
	assert.Len(t, failedLabels, 3) // vpc, subnet, instance
}

func TestChangeset_UpdatePipeline_FailedResourceCascading(t *testing.T) {
	var (
		vpcKsuidURI   = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnetKsuid   = pkgmodel.NewFormaeURI(util.NewID(), "")
		instanceKsuid = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	// Dependency chain: VPC -> Subnet -> Instance
	vpcUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "test-stack",
			Ksuid: vpcKsuidURI.KSUID(),
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateNotStarted,
	}
	subnetUpdate := resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
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
		Resource: pkgmodel.Resource{
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
		"test-update-pipeline-cascade",
		pkgmodel.CommandApply,
	)
	require.NoError(t, err)

	// Verify initial state: 3 groups
	assert.Len(t, changeset.Pipeline.ResourceUpdateGroups, 3)
	assert.False(t, changeset.IsComplete())

	// Mark VPC as failed and call UpdatePipeline
	vpcUpdate.State = resource_update.ResourceUpdateStateFailed
	failedUpdates, err := changeset.UpdatePipeline(&vpcUpdate)
	require.NoError(t, err)

	// Verify cascading failures were detected
	assert.NotNil(t, failedUpdates)
	assert.Len(t, failedUpdates, 3) // vpc, subnet, instance

	// Verify all failed resources have Failed state
	for _, update := range failedUpdates {
		assert.Equal(t, resource_update.ResourceUpdateStateFailed, update.State,
			"Resource %s should be marked as Failed", update.Resource.Label)
	}

	// Verify empty groups were removed from pipeline
	assert.Len(t, changeset.Pipeline.ResourceUpdateGroups, 0,
		"All groups should be removed after cascading failures")

	// Verify changeset is now complete
	assert.True(t, changeset.IsComplete(),
		"Changeset should be complete when all resources are failed")
}

func TestChangeset_GetExecutableUpdatesUpUntilN(t *testing.T) {
	var (
		bucket1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		bucket2KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
		bucket3KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	updates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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

func TestChangeset_FormaWithCycle(t *testing.T) {
	var (
		vpcKsuidURI     = pkgmodel.NewFormaeURI(util.NewID(), "")
		subnet1KsuidURI = pkgmodel.NewFormaeURI(util.NewID(), "")
	)

	resourceUpdates := []resource_update.ResourceUpdate{
		{
			Resource: pkgmodel.Resource{
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
			Resource: pkgmodel.Resource{
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
