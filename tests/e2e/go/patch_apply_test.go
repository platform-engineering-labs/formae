// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPatchApply(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testPatchApplyAWS(t, cli) })
	t.Run("Azure", func(t *testing.T) { testPatchApplyAzure(t, cli) })
}

func testPatchApplyAWS(t *testing.T, cli *FormaeCLI) {
	baseFixture := filepath.Join(fixturesDir(t), "patch_base_aws.pkl")
	patchFixture := filepath.Join(fixturesDir(t), "patch_apply_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply the base resources in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", baseFixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Capture inventory snapshot before patch.
	beforeResources := cli.Inventory(t, "--query", "stack:e2e-patch-aws")
	if len(beforeResources) != 2 {
		t.Fatalf("expected 2 resources before patch, got %d", len(beforeResources))
	}

	// Step 3: Assert the Role has the base Description and no Tags initially.
	roleBefore := RequireResource(t, beforeResources, "e2e-patch-role")
	AssertStringProperty(t, roleBefore, "Description", "e2e patch test role")
	if _, hasTags := roleBefore.Properties["Tags"]; hasTags {
		t.Error("expected Role to have no Tags before patch")
	}

	// Capture RolePolicy properties before patch for later comparison.
	rolePolicyBefore := RequireResource(t, beforeResources, "e2e-patch-role-policy")
	AssertStringProperty(t, rolePolicyBefore, "PolicyName", "formae-e2e-patch-policy")

	// Step 4: Apply the patch fixture in patch mode.
	patchCmdID := cli.Apply(t, "patch", patchFixture)
	patchResult := cli.WaitForCommand(t, patchCmdID, commandTimeout)
	RequireCommandSuccess(t, patchResult)

	// Step 5: Capture inventory snapshot after patch.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-patch-aws")
	if len(afterResources) != 2 {
		t.Fatalf("expected 2 resources after patch, got %d", len(afterResources))
	}

	// Step 6: Assert the Role now has the patched Description.
	roleAfter := RequireResource(t, afterResources, "e2e-patch-role")
	AssertStringProperty(t, roleAfter, "Description", "patched by e2e test")

	// Step 7: Assert the Role now has a tag with Key="Environment", Value="e2e-test".
	AssertTagExists(t, roleAfter, "Tags", "Environment", "e2e-test")

	// Step 8: Assert the RolePolicy is unchanged.
	rolePolicyAfter := RequireResource(t, afterResources, "e2e-patch-role-policy")
	AssertStringProperty(t, rolePolicyAfter, "PolicyName", "formae-e2e-patch-policy")
	// Verify the RolePolicy's RoleName resolvable is still intact.
	rolePolicyRoleName, ok := rolePolicyAfter.Properties["RoleName"]
	if !ok {
		t.Fatal("role policy missing property RoleName after patch")
	}
	AssertResolvable(t, rolePolicyRoleName,
		"e2e-patch-role",
		"AWS::IAM::Role",
		"RoleName",
		"formae-e2e-patch-role",
	)

	// Step 9: Destroy using the base fixture (which has all resources).
	destroyID := cli.Destroy(t, baseFixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-patch-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

func testPatchApplyAzure(t *testing.T, cli *FormaeCLI) {
	if os.Getenv("AZURE_SUBSCRIPTION_ID") == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID not set, skipping Azure tests")
	}

	baseFixture := filepath.Join(fixturesDir(t), "patch_base_azure.pkl")
	patchFixture := filepath.Join(fixturesDir(t), "patch_apply_azure.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply the base resources in reconcile mode.
	cmdID := cli.Apply(t, "reconcile", baseFixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Capture inventory snapshot before patch.
	beforeResources := cli.Inventory(t, "--query", "stack:e2e-patch-azure")
	if len(beforeResources) != 3 {
		t.Fatalf("expected 3 resources before patch, got %d", len(beforeResources))
	}

	// Step 3: Assert the ResourceGroup has no Tags initially.
	rgBefore := RequireResource(t, beforeResources, "e2e-patch-rg")
	if _, hasTags := rgBefore.Properties["Tags"]; hasTags {
		t.Error("expected ResourceGroup to have no Tags before patch")
	}

	// Capture VNet and Subnet properties before patch for later comparison.
	vnetBefore := RequireResource(t, beforeResources, "e2e-patch-vnet")
	AssertStringProperty(t, vnetBefore, "name", "formae-e2e-patch-vnet")

	subnetBefore := RequireResource(t, beforeResources, "e2e-patch-subnet")
	AssertStringProperty(t, subnetBefore, "name", "formae-e2e-patch-subnet")

	// Step 4: Apply the patch fixture in patch mode.
	patchCmdID := cli.Apply(t, "patch", patchFixture)
	patchResult := cli.WaitForCommand(t, patchCmdID, commandTimeout)
	RequireCommandSuccess(t, patchResult)

	// Step 5: Capture inventory snapshot after patch.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-patch-azure")
	if len(afterResources) != 3 {
		t.Fatalf("expected 3 resources after patch, got %d", len(afterResources))
	}

	// Step 6: Assert the ResourceGroup now has a tag with Key="Environment", Value="e2e-test".
	rgAfter := RequireResource(t, afterResources, "e2e-patch-rg")
	AssertTagExists(t, rgAfter, "Tags", "Environment", "e2e-test")

	// Step 7: Assert VNet is unchanged.
	vnetAfter := RequireResource(t, afterResources, "e2e-patch-vnet")
	AssertStringProperty(t, vnetAfter, "name", "formae-e2e-patch-vnet")
	// Verify the VNet's resourceGroupName resolvable is still intact.
	vnetRgName, ok := vnetAfter.Properties["resourceGroupName"]
	if !ok {
		t.Fatal("vnet missing property resourceGroupName after patch")
	}
	AssertResolvable(t, vnetRgName,
		"e2e-patch-rg",
		"Azure::Resources::ResourceGroup",
		"name",
		"formae-e2e-patch-rg",
	)

	// Step 8: Assert Subnet is unchanged.
	subnetAfter := RequireResource(t, afterResources, "e2e-patch-subnet")
	AssertStringProperty(t, subnetAfter, "name", "formae-e2e-patch-subnet")

	// Step 9: Destroy using the base fixture (which has all resources).
	destroyID := cli.Destroy(t, baseFixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-patch-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
