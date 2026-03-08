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

func TestTargetReplace(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testTargetReplaceAWS(t, cli) })
}

func testTargetReplaceAWS(t *testing.T, cli *FormaeCLI) {
	fixtureV1 := filepath.Join(fixturesDir(t), "target_replace_aws_v1.pkl")
	fixtureV2 := filepath.Join(fixturesDir(t), "target_replace_aws_v2.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply v1 — target in us-west-2 with an IAM role.
	cmdID := cli.Apply(t, "reconcile", fixtureV1)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify the role exists.
	resources := cli.Inventory(t, "--query", "stack:e2e-target-replace-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource after v1 apply, got %d", len(resources))
	}
	role := RequireResource(t, resources, "e2e-target-replace-role")
	AssertStringProperty(t, role, "RoleName", "formae-e2e-target-replace-role")

	// Step 3: Simulate v2 — target config changes from us-west-2 to us-east-1.
	// This should report changes are required (target replace + resource replace).
	simResult := cli.Simulate(t, "reconcile", fixtureV2)
	if !simResult.ChangesRequired {
		t.Fatal("expected simulate to report ChangesRequired=true for target config change")
	}

	// The simulation should include resource operations for the replace.
	var hasDelete, hasCreate bool
	for _, ru := range simResult.ResourceUpdates {
		t.Logf("simulate resource: %s (%s) operation=%s", ru.Label, ru.Type, ru.Operation)
		if ru.Operation == "delete" {
			hasDelete = true
		}
		if ru.Operation == "create" {
			hasCreate = true
		}
	}
	if !hasDelete || !hasCreate {
		t.Errorf("expected both delete and create operations in simulation (hasDelete=%v, hasCreate=%v)", hasDelete, hasCreate)
	}

	// Step 4: Apply v2 for real — triggers target replace lifecycle:
	// delete resource → delete target → create target → create resource.
	replaceID := cli.Apply(t, "reconcile", fixtureV2)
	replaceResult := cli.WaitForCommand(t, replaceID, commandTimeout)
	RequireCommandSuccess(t, replaceResult)

	// Step 5: Verify the replace command had both delete and create operations.
	hasDelete = false
	hasCreate = false
	for _, ru := range replaceResult.ResourceUpdates {
		t.Logf("replace resource update: %s (%s) operation=%s state=%s", ru.Label, ru.Type, ru.Operation, ru.State)
		if ru.Operation == "delete" && ru.State == "Success" {
			hasDelete = true
		}
		if ru.Operation == "create" && ru.State == "Success" {
			hasCreate = true
		}
	}
	if !hasDelete {
		t.Error("replace command should have a successful delete operation for the resource")
	}
	if !hasCreate {
		t.Error("replace command should have a successful create operation for the resource")
	}

	// Step 6: Verify the role still exists after replace (was recreated).
	afterResources := cli.Inventory(t, "--query", "stack:e2e-target-replace-aws")
	if len(afterResources) != 1 {
		t.Fatalf("expected 1 resource after target replace, got %d", len(afterResources))
	}
	roleAfter := RequireResource(t, afterResources, "e2e-target-replace-role")
	AssertStringProperty(t, roleAfter, "RoleName", "formae-e2e-target-replace-role")

	// Step 7: Applying v2 again should be a no-op (no changes).
	noopID := cli.Apply(t, "reconcile", fixtureV2)
	noopResult := cli.WaitForCommand(t, noopID, commandTimeout)
	RequireCommandSuccess(t, noopResult)

	// Step 8: Destroy and verify cleanup.
	destroyID := cli.Destroy(t, fixtureV2)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-target-replace-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}

	// Verify the role is actually gone in AWS.
	verifyAWSRoleDeleted(t, "formae-e2e-target-replace-role")
}
