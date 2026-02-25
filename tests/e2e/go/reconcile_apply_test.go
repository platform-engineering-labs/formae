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
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
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
	t.Run("AWS", testReconcileApplyAWS)
	t.Run("Azure", testReconcileApplyAzure)
}

func testReconcileApplyAWS(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Query inventory for the stack.
	resources := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")

	if len(resources) != 2 {
		t.Fatalf("expected 2 resources in stack e2e-reconcile-aws, got %d", len(resources))
	}

	// Step 3: Assert the Role resource.
	role := RequireResource(t, resources, "e2e-test-role")
	if role.Type != "AWS::IAM::Role" {
		t.Errorf("role type: got %s, want AWS::IAM::Role", role.Type)
	}
	AssertStringProperty(t, role, "RoleName", "formae-e2e-reconcile-role")
	if _, ok := role.Properties["AssumeRolePolicyDocument"]; !ok {
		t.Error("role missing property AssumeRolePolicyDocument")
	}

	// Step 4: Assert the RolePolicy resource.
	rolePolicy := RequireResource(t, resources, "e2e-test-role-policy")
	if rolePolicy.Type != "AWS::IAM::RolePolicy" {
		t.Errorf("role policy type: got %s, want AWS::IAM::RolePolicy", rolePolicy.Type)
	}
	AssertStringProperty(t, rolePolicy, "PolicyName", "formae-e2e-reconcile-policy")

	// RoleName on the policy should be a resolvable reference to the Role.
	rolePolicyRoleName, ok := rolePolicy.Properties["RoleName"]
	if !ok {
		t.Fatal("role policy missing property RoleName")
	}
	AssertResolvable(t, rolePolicyRoleName,
		"e2e-test-role",
		"AWS::IAM::Role",
		"RoleName",
		"formae-e2e-reconcile-role",
	)

	// Step 5: Inventory query filters.
	// Query by type within the stack.
	roles := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws type:AWS::IAM::Role")
	if len(roles) != 1 {
		t.Errorf("expected 1 Role in stack, got %d", len(roles))
	}
	policies := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws type:AWS::IAM::RolePolicy")
	if len(policies) != 1 {
		t.Errorf("expected 1 RolePolicy in stack, got %d", len(policies))
	}

	// Query by label.
	byLabel := cli.Inventory(t, "--query", "label:e2e-test-role")
	if len(byLabel) != 1 {
		t.Errorf("expected 1 resource with label e2e-test-role, got %d", len(byLabel))
	}

	// All resources should be managed.
	managed := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws managed:true")
	if len(managed) != 2 {
		t.Errorf("expected 2 managed resources in stack, got %d", len(managed))
	}
	unmanaged := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws managed:false")
	if len(unmanaged) != 0 {
		t.Errorf("expected 0 unmanaged resources in stack, got %d", len(unmanaged))
	}

	// Step 6: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 7: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	// Step 8: Verify the IAM role is actually gone in AWS.
	verifyAWSRoleDeleted(t, "formae-e2e-reconcile-role")
}

func testReconcileApplyAzure(t *testing.T) {
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Fatal("AZURE_SUBSCRIPTION_ID environment variable is required for Azure tests")
	}

	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

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
	// Query by type within the stack.
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

	// Query by label.
	byLabel := cli.Inventory(t, "--query", "label:e2e-test-rg")
	if len(byLabel) != 1 {
		t.Errorf("expected 1 resource with label e2e-test-rg, got %d", len(byLabel))
	}

	// All resources should be managed.
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

// verifyAWSRoleDeleted uses the AWS IAM SDK to confirm that the given role
// no longer exists. It expects a NoSuchEntity error from GetRole.
func verifyAWSRoleDeleted(t *testing.T, roleName string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-west-2"))
	if err != nil {
		t.Fatalf("failed to load AWS config: %v", err)
	}

	client := iam.NewFromConfig(cfg)
	_, err = client.GetRole(ctx, &iam.GetRoleInput{
		RoleName: &roleName,
	})
	if err == nil {
		t.Errorf("expected IAM role %q to be deleted, but GetRole succeeded", roleName)
		return
	}

	if !strings.Contains(err.Error(), "NoSuchEntity") {
		t.Errorf("expected NoSuchEntity error for role %q, got: %v", roleName, err)
	}
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
