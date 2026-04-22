// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplace(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testReplaceAWS(t, cli) })
	t.Run("Azure", func(t *testing.T) { testReplaceAzure(t, cli) })
}

func testReplaceAWS(t *testing.T, cli *FormaeCLI) {
	fixtureV1 := filepath.Join(fixturesDir(t), "replace_aws_v1.pkl")
	fixtureV2 := filepath.Join(fixturesDir(t), "replace_aws_v2.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply v1 to create the initial role.
	cmdID := cli.Apply(t, "reconcile", fixtureV1)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify the v1 role exists.
	resources := cli.Inventory(t, "--query", "stack:e2e-replace-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	role := RequireResource(t, resources, "e2e-replace-role")
	AssertStringProperty(t, role, "RoleName", "formae-e2e-replace-role-v1")
	AssertStringProperty(t, role, "Description", "e2e replace test role v1")

	// Step 3a: Simulate v2 — RoleName is CreateOnly, so formae should plan a
	// replacement. The simulate response must carry ReplacementPatchDocument
	// on the delete half so the CLI can show *why* the resource is being
	// replaced in the plan preview.
	simResult := cli.Simulate(t, "reconcile", fixtureV2)
	if !simResult.ChangesRequired {
		t.Fatal("expected simulate to report ChangesRequired=true for v1→v2 replacement")
	}
	var deleteRU *ResourceUpdate
	for i := range simResult.ResourceUpdates {
		ru := &simResult.ResourceUpdates[i]
		if ru.Label == "e2e-replace-role" && ru.Operation == "delete" {
			deleteRU = ru
			break
		}
	}
	if deleteRU == nil {
		t.Fatalf("expected a delete operation for the role in the simulate plan, got updates=%+v", simResult.ResourceUpdates)
	}
	if len(deleteRU.ReplacementPatchDocument) == 0 {
		t.Fatal("delete half of the replacement pair must carry ReplacementPatchDocument so the CLI can render the reason")
	}
	if !bytes.Contains(deleteRU.ReplacementPatchDocument, []byte("RoleName")) {
		t.Fatalf("ReplacementPatchDocument must reference the createOnly field (RoleName) that triggered the replace; got %s", string(deleteRU.ReplacementPatchDocument))
	}

	// Step 3b: Apply v2 — RoleName is CreateOnly, so formae should replace
	// the resource (delete v1, create v2).
	replaceID := cli.Apply(t, "reconcile", fixtureV2)
	replaceResult := cli.WaitForCommand(t, replaceID, commandTimeout)
	RequireCommandSuccess(t, replaceResult)

	// Step 4: Verify the role now has v2 properties.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-replace-aws")
	if len(afterResources) != 1 {
		t.Fatalf("expected 1 resource after replace, got %d", len(afterResources))
	}
	roleAfter := RequireResource(t, afterResources, "e2e-replace-role")
	AssertStringProperty(t, roleAfter, "RoleName", "formae-e2e-replace-role-v2")
	AssertStringProperty(t, roleAfter, "Description", "e2e replace test role v2")

	// Step 5: Verify the old role is actually gone in AWS.
	verifyAWSRoleDeleted(t, "formae-e2e-replace-role-v1")

	// Step 6: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixtureV2)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-replace-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}

func testReplaceAzure(t *testing.T, cli *FormaeCLI) {
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if subscriptionID == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID not set, skipping Azure tests")
	}

	fixtureV1 := filepath.Join(fixturesDir(t), "replace_azure_v1.pkl")
	fixtureV2 := filepath.Join(fixturesDir(t), "replace_azure_v2.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply v1 to create the initial resource group.
	cmdID := cli.Apply(t, "reconcile", fixtureV1)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify the v1 resource group exists.
	resources := cli.Inventory(t, "--query", "stack:e2e-replace-azure")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	rg := RequireResource(t, resources, "e2e-replace-rg")
	AssertStringProperty(t, rg, "name", "formae-e2e-replace-rg-v1")

	// Step 3: Apply v2 — name is createOnly on ResourceGroup, so formae
	// should replace the resource (delete v1, create v2).
	replaceID := cli.Apply(t, "reconcile", fixtureV2)
	replaceResult := cli.WaitForCommand(t, replaceID, commandTimeout)
	RequireCommandSuccess(t, replaceResult)

	// Step 4: Verify the resource group now has v2 properties.
	afterResources := cli.Inventory(t, "--query", "stack:e2e-replace-azure")
	if len(afterResources) != 1 {
		t.Fatalf("expected 1 resource after replace, got %d", len(afterResources))
	}
	rgAfter := RequireResource(t, afterResources, "e2e-replace-rg")
	AssertStringProperty(t, rgAfter, "name", "formae-e2e-replace-rg-v2")

	// Step 5: Verify the old resource group is actually gone in Azure.
	verifyAzureResourceGroupDeleted(t, subscriptionID, "formae-e2e-replace-rg-v1")

	// Step 6: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixtureV2)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-replace-azure")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
