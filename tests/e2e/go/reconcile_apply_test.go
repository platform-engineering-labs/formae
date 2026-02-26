// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// fixturesDir returns the absolute path to the fixtures directory relative
// to this test file. Using runtime.Caller ensures it works regardless of
// the working directory.
func fixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path via runtime.Caller")
	}
	return filepath.Join(filepath.Dir(thisFile), "fixtures")
}

func TestReconcileApply(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testReconcileApplyAWS(t, cli) })
	t.Run("Azure", func(t *testing.T) { testReconcileApplyAzure(t, cli) })
}

func testReconcileApplyAWS(t *testing.T, cli *FormaeCLI) {
	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify all 7 resources were created.
	resources := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(resources) != 7 {
		t.Fatalf("expected 7 resources in stack e2e-reconcile-aws, got %d", len(resources))
	}

	// Step 3: Assert each resource exists with correct type and key properties.
	vpc := RequireResource(t, resources, "e2e-vpc")
	if vpc.Type != "AWS::EC2::VPC" {
		t.Errorf("expected VPC type, got %s", vpc.Type)
	}
	AssertStringProperty(t, vpc, "CidrBlock", "10.99.0.0/16")

	subnet := RequireResource(t, resources, "e2e-subnet")
	if subnet.Type != "AWS::EC2::Subnet" {
		t.Errorf("expected Subnet type, got %s", subnet.Type)
	}
	AssertStringProperty(t, subnet, "CidrBlock", "10.99.1.0/24")

	rt := RequireResource(t, resources, "e2e-rt")
	if rt.Type != "AWS::EC2::RouteTable" {
		t.Errorf("expected RouteTable type, got %s", rt.Type)
	}

	igw := RequireResource(t, resources, "e2e-igw")
	if igw.Type != "AWS::EC2::InternetGateway" {
		t.Errorf("expected InternetGateway type, got %s", igw.Type)
	}

	attachment := RequireResource(t, resources, "e2e-igw-attachment")
	if attachment.Type != "AWS::EC2::VPCGatewayAttachment" {
		t.Errorf("expected VPCGatewayAttachment type, got %s", attachment.Type)
	}

	route := RequireResource(t, resources, "e2e-route")
	if route.Type != "AWS::EC2::Route" {
		t.Errorf("expected Route type, got %s", route.Type)
	}
	AssertStringProperty(t, route, "DestinationCidrBlock", "0.0.0.0/0")

	assoc := RequireResource(t, resources, "e2e-subnet-assoc")
	if assoc.Type != "AWS::EC2::SubnetRouteTableAssociation" {
		t.Errorf("expected SubnetRouteTableAssociation type, got %s", assoc.Type)
	}

	// Step 4: Verify cross-resource resolvable references.
	AssertResolvableProperty(t, subnet, "VpcId", "AWS::EC2::VPC")
	AssertResolvableProperty(t, rt, "VpcId", "AWS::EC2::VPC")
	AssertResolvableProperty(t, route, "RouteTableId", "AWS::EC2::RouteTable")
	AssertResolvableProperty(t, route, "GatewayId", "AWS::EC2::InternetGateway")
	AssertResolvableProperty(t, assoc, "SubnetId", "AWS::EC2::Subnet")
	AssertResolvableProperty(t, assoc, "RouteTableId", "AWS::EC2::RouteTable")

	// Step 5: Inventory query filters.
	vpcs := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws type:AWS::EC2::VPC")
	if len(vpcs) != 1 {
		t.Errorf("expected 1 VPC in stack, got %d", len(vpcs))
	}
	subnets := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws type:AWS::EC2::Subnet")
	if len(subnets) != 1 {
		t.Errorf("expected 1 Subnet in stack, got %d", len(subnets))
	}

	byLabel := cli.Inventory(t, "--query", "label:e2e-vpc")
	if len(byLabel) != 1 {
		t.Errorf("expected 1 resource with label e2e-vpc, got %d", len(byLabel))
	}

	managed := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws managed:true")
	if len(managed) != 7 {
		t.Errorf("expected 7 managed resources in stack, got %d", len(managed))
	}
	unmanaged := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws managed:false")
	if len(unmanaged) != 0 {
		t.Errorf("expected 0 unmanaged resources in stack, got %d", len(unmanaged))
	}

	// Step 6: Destroy — this tests reverse dependency ordering.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 7: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

func testReconcileApplyAzure(t *testing.T, cli *FormaeCLI) {
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID not set, skipping Azure tests")
	}

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_azure.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Query inventory for the stack.
	resources := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")

	if len(resources) != 3 {
		t.Fatalf("expected 3 resources in stack e2e-reconcile-azure, got %d", len(resources))
	}

	// Step 3: Assert the ResourceGroup.
	rg := RequireResource(t, resources, "e2e-test-rg")
	if rg.Type != "Azure::Resources::ResourceGroup" {
		t.Errorf("resource group type: got %s, want Azure::Resources::ResourceGroup", rg.Type)
	}
	AssertStringProperty(t, rg, "name", "formae-e2e-reconcile-rg")
	AssertStringProperty(t, rg, "location", "westeurope")

	// Step 4: Assert the VirtualNetwork.
	vnet := RequireResource(t, resources, "e2e-test-vnet")
	if vnet.Type != "Azure::Network::VirtualNetwork" {
		t.Errorf("vnet type: got %s, want Azure::Network::VirtualNetwork", vnet.Type)
	}
	AssertStringProperty(t, vnet, "name", "formae-e2e-reconcile-vnet")

	// resourceGroupName should be a resolvable reference to the ResourceGroup.
	vnetRgName, ok := vnet.Properties["resourceGroupName"]
	if !ok {
		t.Fatal("vnet missing property resourceGroupName")
	}
	AssertResolvable(t, vnetRgName,
		"e2e-test-rg",
		"Azure::Resources::ResourceGroup",
		"name",
		"formae-e2e-reconcile-rg",
	)

	// Step 5: Assert the Subnet.
	subnet := RequireResource(t, resources, "e2e-test-subnet")
	if subnet.Type != "Azure::Network::Subnet" {
		t.Errorf("subnet type: got %s, want Azure::Network::Subnet", subnet.Type)
	}
	AssertStringProperty(t, subnet, "name", "formae-e2e-reconcile-subnet")

	// resourceGroupName should be a resolvable reference to the ResourceGroup.
	subnetRgName, ok := subnet.Properties["resourceGroupName"]
	if !ok {
		t.Fatal("subnet missing property resourceGroupName")
	}
	AssertResolvable(t, subnetRgName,
		"e2e-test-rg",
		"Azure::Resources::ResourceGroup",
		"name",
		"formae-e2e-reconcile-rg",
	)

	// virtualNetworkName should be a resolvable reference to the VirtualNetwork.
	subnetVnetName, ok := subnet.Properties["virtualNetworkName"]
	if !ok {
		t.Fatal("subnet missing property virtualNetworkName")
	}
	AssertResolvable(t, subnetVnetName,
		"e2e-test-vnet",
		"Azure::Network::VirtualNetwork",
		"name",
		"formae-e2e-reconcile-vnet",
	)

	// Step 6: Inventory query filters.
	rgs := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure type:Azure::Resources::ResourceGroup")
	if len(rgs) != 1 {
		t.Errorf("expected 1 ResourceGroup in stack, got %d", len(rgs))
	}
	vnets := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure type:Azure::Network::VirtualNetwork")
	if len(vnets) != 1 {
		t.Errorf("expected 1 VirtualNetwork in stack, got %d", len(vnets))
	}
	subnets := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure type:Azure::Network::Subnet")
	if len(subnets) != 1 {
		t.Errorf("expected 1 Subnet in stack, got %d", len(subnets))
	}

	byLabel := cli.Inventory(t, "--query", "label:e2e-test-rg")
	if len(byLabel) != 1 {
		t.Errorf("expected 1 resource with label e2e-test-rg, got %d", len(byLabel))
	}

	managed := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure managed:true")
	if len(managed) != 3 {
		t.Errorf("expected 3 managed resources in stack, got %d", len(managed))
	}
	unmanagedAzure := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure managed:false")
	if len(unmanagedAzure) != 0 {
		t.Errorf("expected 0 unmanaged resources in stack, got %d", len(unmanagedAzure))
	}

	// Step 7: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 8: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	// Step 9: Verify the resource group is actually gone in Azure.
	verifyAzureResourceGroupDeleted(t, subscriptionID, "formae-e2e-reconcile-rg")
}

// verifyAzureResourceGroupDeleted uses the Azure SDK to confirm that the
// given resource group no longer exists.
func verifyAzureResourceGroupDeleted(t *testing.T, subscriptionID, rgName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("failed to create Azure credential: %v", err)
	}

	client, err := armresources.NewResourceGroupsClient(subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("failed to create Azure resource groups client: %v", err)
	}

	_, err = client.Get(ctx, rgName, nil)
	if err == nil {
		t.Errorf("expected resource group %q to be deleted, but Get succeeded", rgName)
		return
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "ResourceGroupNotFound") && !strings.Contains(errMsg, "ResourceNotFound") && !strings.Contains(errMsg, "404") {
		t.Errorf("expected NotFound error for resource group %q, got: %v", rgName, err)
	}
}
