// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPatchApply(t *testing.T) {
	t.Run("AWS", testPatchApplyAWS)
	t.Run("Azure", testPatchApplyAzure)
}

func testPatchApplyAWS(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	reconcileFixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	patchFixture := filepath.Join(fixturesDir(t), "patch_apply_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply the base resources in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", reconcileFixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Capture inventory snapshot before patch.
	beforeResources := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(beforeResources) != 2 {
		t.Fatalf("expected 2 resources before patch, got %d", len(beforeResources))
	}

	// Step 3: Assert the Role has no Description and no Tags initially.
	roleBefore := RequireResource(t, beforeResources, "e2e-test-role")
	if _, hasDesc := roleBefore.Properties["Description"]; hasDesc {
		t.Error("expected Role to have no Description before patch")
	}
	if _, hasTags := roleBefore.Properties["Tags"]; hasTags {
		t.Error("expected Role to have no Tags before patch")
	}

	// Capture RolePolicy properties before patch for later comparison.
	rolePolicyBefore := RequireResource(t, beforeResources, "e2e-test-role-policy")
	AssertStringProperty(t, rolePolicyBefore, "PolicyName", "formae-e2e-reconcile-policy")

	// Step 4: Apply the patch fixture in patch mode.
	patchCmdID := cli.Apply(t, "patch", patchFixture)
	patchResult := cli.WaitForCommand(t, patchCmdID, commandTimeout)
	RequireCommandSuccess(t, patchResult)

	// Step 5: Capture inventory snapshot after patch.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(afterResources) != 2 {
		t.Fatalf("expected 2 resources after patch, got %d", len(afterResources))
	}

	// Step 6: Assert the Role now has the patched Description.
	roleAfter := RequireResource(t, afterResources, "e2e-test-role")
	AssertStringProperty(t, roleAfter, "Description", "patched by e2e test")

	// Step 7: Assert the Role now has a tag with Key="Environment", Value="e2e-test".
	AssertTagExists(t, roleAfter, "Tags", "Environment", "e2e-test")

	// Step 8: Assert the RolePolicy is unchanged.
	rolePolicyAfter := RequireResource(t, afterResources, "e2e-test-role-policy")
	AssertStringProperty(t, rolePolicyAfter, "PolicyName", "formae-e2e-reconcile-policy")
	// Verify the RolePolicy's RoleName resolvable is still intact.
	rolePolicyRoleName, ok := rolePolicyAfter.Properties["RoleName"]
	if !ok {
		t.Fatal("role policy missing property RoleName after patch")
	}
	AssertResolvable(t, rolePolicyRoleName,
		"e2e-test-role",
		"AWS::IAM::Role",
		"RoleName",
		"formae-e2e-reconcile-role",
	)

	// Step 9: Destroy using the reconcile fixture (which has all resources).
	destroyID := cli.Destroy(t, reconcileFixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

func testPatchApplyAzure(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath())

	reconcileFixture := filepath.Join(fixturesDir(t), "reconcile_apply_azure.pkl")
	patchFixture := filepath.Join(fixturesDir(t), "patch_apply_azure.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply the base resources in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", reconcileFixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Capture inventory snapshot before patch.
	beforeResources := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(beforeResources) != 3 {
		t.Fatalf("expected 3 resources before patch, got %d", len(beforeResources))
	}

	// Step 3: Assert the ResourceGroup has no Tags initially.
	rgBefore := RequireResource(t, beforeResources, "e2e-test-rg")
	if _, hasTags := rgBefore.Properties["Tags"]; hasTags {
		t.Error("expected ResourceGroup to have no Tags before patch")
	}

	// Capture VNet and Subnet properties before patch for later comparison.
	vnetBefore := RequireResource(t, beforeResources, "e2e-test-vnet")
	AssertStringProperty(t, vnetBefore, "name", "formae-e2e-reconcile-vnet")

	subnetBefore := RequireResource(t, beforeResources, "e2e-test-subnet")
	AssertStringProperty(t, subnetBefore, "name", "formae-e2e-reconcile-subnet")

	// Step 4: Apply the patch fixture in patch mode.
	patchCmdID := cli.Apply(t, "patch", patchFixture)
	patchResult := cli.WaitForCommand(t, patchCmdID, commandTimeout)
	RequireCommandSuccess(t, patchResult)

	// Step 5: Capture inventory snapshot after patch.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(afterResources) != 3 {
		t.Fatalf("expected 3 resources after patch, got %d", len(afterResources))
	}

	// Step 6: Assert the ResourceGroup now has a tag with Key="Environment", Value="e2e-test".
	rgAfter := RequireResource(t, afterResources, "e2e-test-rg")
	AssertTagExists(t, rgAfter, "Tags", "Environment", "e2e-test")

	// Step 7: Assert VNet is unchanged.
	vnetAfter := RequireResource(t, afterResources, "e2e-test-vnet")
	AssertStringProperty(t, vnetAfter, "name", "formae-e2e-reconcile-vnet")
	// Verify the VNet's resourceGroupName resolvable is still intact.
	vnetRgName, ok := vnetAfter.Properties["resourceGroupName"]
	if !ok {
		t.Fatal("vnet missing property resourceGroupName after patch")
	}
	AssertResolvable(t, vnetRgName,
		"e2e-test-rg",
		"Azure::Resources::ResourceGroup",
		"name",
		"formae-e2e-reconcile-rg",
	)

	// Step 8: Assert Subnet is unchanged.
	subnetAfter := RequireResource(t, afterResources, "e2e-test-subnet")
	AssertStringProperty(t, subnetAfter, "name", "formae-e2e-reconcile-subnet")

	// Step 9: Destroy using the reconcile fixture (which has all resources).
	destroyID := cli.Destroy(t, reconcileFixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
