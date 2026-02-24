// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

	// Step 5: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 6: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

func testReconcileApplyAzure(t *testing.T) {
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

	// Step 6: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 7: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
