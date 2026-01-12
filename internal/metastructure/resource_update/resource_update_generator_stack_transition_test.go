// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestStackTransition_Generator(t *testing.T) {
	t.Run("unmanaged to new stack", func(t *testing.T) {
		ds, _ := GetDeps(t)

		vpc := newVPCResource("test-vpc", constants.UnmanagedStack, "vpc-123", false)
		subnet := newSubnetResource("test-subnet", constants.UnmanagedStack, "subnet-456", "vpc-123", false)
		storeStack(t, ds, constants.UnmanagedStack, []pkgmodel.Resource{vpc, subnet})

		forma := newForma("new-stack", []formaResource{
			{label: "test-vpc", resourceType: "AWS::EC2::VPC", cidr: "10.0.0.0/16"},
			{label: "test-subnet", resourceType: "AWS::EC2::Subnet", cidr: "10.0.1.0/24"},
		})

		updates := generateUpdates(t, ds, forma)

		assert.Len(t, updates, 2)
		assertAllUpdates(t, updates, "new-stack")

		vpcUpdate := findUpdateByLabel(updates, "test-vpc")
		subnetUpdate := findUpdateByLabel(updates, "test-subnet")

		assert.Equal(t, vpc.Ksuid, vpcUpdate.DesiredState.Ksuid)
		assert.Equal(t, subnet.Ksuid, subnetUpdate.DesiredState.Ksuid)
		assert.Equal(t, "vpc-123", vpcUpdate.DesiredState.NativeID)
		assert.Equal(t, "subnet-456", subnetUpdate.DesiredState.NativeID)
	})

	t.Run("unmanaged to existing stack", func(t *testing.T) {
		ds, _ := GetDeps(t)

		existingVpc := newVPCResource("vpc-1", "existing-stack", "vpc-111", true)
		storeStack(t, ds, "existing-stack", []pkgmodel.Resource{existingVpc})

		unmanagedVpc := newVPCResource("vpc-2", constants.UnmanagedStack, "vpc-222", false)
		unmanagedSubnet := newSubnetResource("subnet-1", constants.UnmanagedStack, "subnet-111", "vpc-222", false)
		storeStack(t, ds, constants.UnmanagedStack, []pkgmodel.Resource{unmanagedVpc, unmanagedSubnet})

		forma := newForma("existing-stack", []formaResource{
			{label: "vpc-1", resourceType: "AWS::EC2::VPC", cidr: "10.1.0.0/16"},
			{label: "vpc-2", resourceType: "AWS::EC2::VPC", cidr: "10.2.0.0/16"},
			{label: "subnet-1", resourceType: "AWS::EC2::Subnet", cidr: "10.2.1.0/24"},
		})

		updates := generateUpdates(t, ds, forma)

		assert.Len(t, updates, 2)

		vpc2Update := findUpdateByLabel(updates, "vpc-2")
		subnet1Update := findUpdateByLabel(updates, "subnet-1")

		assert.Equal(t, OperationUpdate, vpc2Update.Operation)
		assert.Equal(t, OperationUpdate, subnet1Update.Operation)
		assert.Equal(t, unmanagedVpc.Ksuid, vpc2Update.DesiredState.Ksuid)
		assert.Equal(t, unmanagedSubnet.Ksuid, subnet1Update.DesiredState.Ksuid)
		assert.Equal(t, "vpc-222", vpc2Update.DesiredState.NativeID)
		assert.Equal(t, "subnet-111", subnet1Update.DesiredState.NativeID)
	})

	t.Run("mixed transitions and creates to new stack", func(t *testing.T) {
		ds, _ := GetDeps(t)

		unmanagedVpc := newVPCResource("existing-vpc", constants.UnmanagedStack, "vpc-existing", false)
		storeStack(t, ds, constants.UnmanagedStack, []pkgmodel.Resource{unmanagedVpc})

		forma := newForma("mixed-stack", []formaResource{
			{label: "existing-vpc", resourceType: "AWS::EC2::VPC", cidr: "10.1.0.0/16"},
			{label: "new-vpc", resourceType: "AWS::EC2::VPC", cidr: "10.2.0.0/16"},
		})

		updates := generateUpdates(t, ds, forma)

		assert.Len(t, updates, 2)

		existingUpdate := findUpdateByLabel(updates, "existing-vpc")
		newUpdate := findUpdateByLabel(updates, "new-vpc")

		assert.Equal(t, OperationUpdate, existingUpdate.Operation)
		assert.Equal(t, OperationCreate, newUpdate.Operation)
		assert.Equal(t, unmanagedVpc.Ksuid, existingUpdate.DesiredState.Ksuid)
		assert.Equal(t, "vpc-existing", existingUpdate.DesiredState.NativeID)
	})

	t.Run("mixed transitions and creates to existing stack", func(t *testing.T) {
		ds, _ := GetDeps(t)

		unmanaged1 := newVPCResource("unmanaged-vpc-1", constants.UnmanagedStack, "vpc-unmanaged-1", false)
		unmanaged2 := newVPCResource("unmanaged-vpc-2", constants.UnmanagedStack, "vpc-unmanaged-2", false)
		storeStack(t, ds, constants.UnmanagedStack, []pkgmodel.Resource{unmanaged1, unmanaged2})

		managedVpc := newVPCResource("managed-vpc", "target-stack", "vpc-managed", true)
		storeStack(t, ds, "target-stack", []pkgmodel.Resource{managedVpc})

		forma := newForma("target-stack", []formaResource{
			{label: "managed-vpc", resourceType: "AWS::EC2::VPC", cidr: "10.3.0.0/16"},
			{label: "unmanaged-vpc-1", resourceType: "AWS::EC2::VPC", cidr: "10.1.0.0/16"},
			{label: "unmanaged-vpc-2", resourceType: "AWS::EC2::VPC", cidr: "10.2.0.0/16"},
			{label: "new-vpc", resourceType: "AWS::EC2::VPC", cidr: "10.4.0.0/16"},
		})

		updates := generateUpdates(t, ds, forma)

		assert.Len(t, updates, 3)

		unmanaged1Update := findUpdateByLabel(updates, "unmanaged-vpc-1")
		unmanaged2Update := findUpdateByLabel(updates, "unmanaged-vpc-2")
		newUpdate := findUpdateByLabel(updates, "new-vpc")

		assert.Equal(t, OperationUpdate, unmanaged1Update.Operation)
		assert.Equal(t, OperationUpdate, unmanaged2Update.Operation)
		assert.Equal(t, OperationCreate, newUpdate.Operation)

		assert.Equal(t, unmanaged1.Ksuid, unmanaged1Update.DesiredState.Ksuid)
		assert.Equal(t, unmanaged2.Ksuid, unmanaged2Update.DesiredState.Ksuid)
	})

	t.Run("multiple resources same type transition simultaneously", func(t *testing.T) {
		ds, _ := GetDeps(t)

		vpc1 := newVPCResource("vpc-1", constants.UnmanagedStack, "vpc-111", false)
		vpc2 := newVPCResource("vpc-2", constants.UnmanagedStack, "vpc-222", false)
		vpc3 := newVPCResource("vpc-3", constants.UnmanagedStack, "vpc-333", false)
		storeStack(t, ds, constants.UnmanagedStack, []pkgmodel.Resource{vpc1, vpc2, vpc3})

		forma := newForma("multi-stack", []formaResource{
			{label: "vpc-1", resourceType: "AWS::EC2::VPC", cidr: "10.1.0.0/16"},
			{label: "vpc-2", resourceType: "AWS::EC2::VPC", cidr: "10.2.0.0/16"},
			{label: "vpc-3", resourceType: "AWS::EC2::VPC", cidr: "10.3.0.0/16"},
		})

		updates := generateUpdates(t, ds, forma)

		assert.Len(t, updates, 3)
		assertAllUpdates(t, updates, "multi-stack")

		vpc1Update := findUpdateByLabel(updates, "vpc-1")
		vpc2Update := findUpdateByLabel(updates, "vpc-2")
		vpc3Update := findUpdateByLabel(updates, "vpc-3")

		assert.Equal(t, vpc1.Ksuid, vpc1Update.DesiredState.Ksuid)
		assert.Equal(t, vpc2.Ksuid, vpc2Update.DesiredState.Ksuid)
		assert.Equal(t, vpc3.Ksuid, vpc3Update.DesiredState.Ksuid)

		assert.Equal(t, "vpc-111", vpc1Update.DesiredState.NativeID)
		assert.Equal(t, "vpc-222", vpc2Update.DesiredState.NativeID)
		assert.Equal(t, "vpc-333", vpc3Update.DesiredState.NativeID)
	})

	t.Run("parent child dependencies with VPC references", func(t *testing.T) {
		ds, _ := GetDeps(t)

		vpc := newVPCResource("parent-vpc", constants.UnmanagedStack, "vpc-parent", false)
		subnet1 := newSubnetResource("child-subnet-1", constants.UnmanagedStack, "subnet-child-1", "vpc-parent", false)
		subnet2 := newSubnetResource("child-subnet-2", constants.UnmanagedStack, "subnet-child-2", "vpc-parent", false)
		storeStack(t, ds, constants.UnmanagedStack, []pkgmodel.Resource{vpc, subnet1, subnet2})

		forma := newForma("parent-child-stack", []formaResource{
			{label: "parent-vpc", resourceType: "AWS::EC2::VPC", cidr: "10.0.0.0/16"},
			{label: "child-subnet-1", resourceType: "AWS::EC2::Subnet", cidr: "10.0.1.0/24"},
			{label: "child-subnet-2", resourceType: "AWS::EC2::Subnet", cidr: "10.0.2.0/24"},
		})

		updates := generateUpdates(t, ds, forma)

		assert.Len(t, updates, 3)
		assertAllUpdates(t, updates, "parent-child-stack")

		vpcUpdate := findUpdateByLabel(updates, "parent-vpc")
		subnet1Update := findUpdateByLabel(updates, "child-subnet-1")
		subnet2Update := findUpdateByLabel(updates, "child-subnet-2")

		assert.Equal(t, vpc.Ksuid, vpcUpdate.DesiredState.Ksuid)
		assert.Equal(t, subnet1.Ksuid, subnet1Update.DesiredState.Ksuid)
		assert.Equal(t, subnet2.Ksuid, subnet2Update.DesiredState.Ksuid)

		assert.Equal(t, "vpc-parent", vpcUpdate.DesiredState.NativeID)
		assert.Equal(t, "subnet-child-1", subnet1Update.DesiredState.NativeID)
		assert.Equal(t, "subnet-child-2", subnet2Update.DesiredState.NativeID)
	})
}

func newVPCResource(label, stack, nativeID string, managed bool) pkgmodel.Resource {
	return pkgmodel.Resource{
		Ksuid:              util.NewID(),
		Label:              label,
		Type:               "AWS::EC2::VPC",
		Stack:              stack,
		Target:             "test-target",
		NativeID:           nativeID,
		Properties:         json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
		ReadOnlyProperties: json.RawMessage(`{"VpcId": "` + nativeID + `"}`),
		Managed:            managed,
	}
}

func newSubnetResource(label, stack, nativeID, vpcID string, managed bool) pkgmodel.Resource {
	readOnlyProps := `{"SubnetId": "` + nativeID + `", "VpcId": "` + vpcID + `"}`
	return pkgmodel.Resource{
		Ksuid:              util.NewID(),
		Label:              label,
		Type:               "AWS::EC2::Subnet",
		Stack:              stack,
		Target:             "test-target",
		NativeID:           nativeID,
		Properties:         json.RawMessage(`{"CidrBlock": "10.0.1.0/24"}`),
		ReadOnlyProperties: json.RawMessage(readOnlyProps),
		Managed:            managed,
	}
}

func storeStack(t *testing.T, ds *mockDatastore, stackLabel string, resources []pkgmodel.Resource) {
	t.Helper()
	stack := &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: stackLabel}},
		Resources: resources,
	}
	_, err := ds.StoreStack(stack, "test-"+stackLabel)
	require.NoError(t, err)
}

type formaResource struct {
	label        string
	resourceType string
	cidr         string
}

func newForma(stackLabel string, resources []formaResource) *pkgmodel.Forma {
	formaResources := make([]pkgmodel.Resource, len(resources))
	for i, r := range resources {
		formaResources[i] = pkgmodel.Resource{
			Label:      r.label,
			Type:       r.resourceType,
			Stack:      stackLabel,
			Target:     "test-target",
			Properties: json.RawMessage(`{"CidrBlock": "` + r.cidr + `"}`),
			Managed:    true,
		}
	}
	return &pkgmodel.Forma{
		Stacks:    []pkgmodel.Stack{{Label: stackLabel}},
		Resources: formaResources,
	}
}

func generateUpdates(t *testing.T, ds *mockDatastore, forma *pkgmodel.Forma) []ResourceUpdate {
	t.Helper()
	targets := []*pkgmodel.Target{{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}}
	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, targets, ds)
	require.NoError(t, err)
	return updates
}

func assertAllUpdates(t *testing.T, updates []ResourceUpdate, expectedStack string) {
	t.Helper()
	for _, u := range updates {
		assert.Equal(t, OperationUpdate, u.Operation)
		assert.Equal(t, expectedStack, u.StackLabel)
	}
}

func findUpdateByLabel(updates []ResourceUpdate, label string) ResourceUpdate {
	for _, u := range updates {
		if u.DesiredState.Label == label {
			return u
		}
	}
	return ResourceUpdate{}
}
