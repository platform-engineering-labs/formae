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

func TestReplace(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	t.Run("AWS", func(t *testing.T) { testReplaceAWS(t, cli) })
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

	// Step 3: Apply v2 — RoleName is CreateOnly, so formae should replace
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
