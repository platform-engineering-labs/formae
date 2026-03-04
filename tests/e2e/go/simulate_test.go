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

func TestSimulateApply(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "simulate_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Simulate a reconcile on a fresh stack — should report creates
	// but NOT actually create any resources.
	simResult := cli.Simulate(t, "reconcile", fixture)
	if !simResult.ChangesRequired {
		t.Fatal("expected simulate to report ChangesRequired=true for fresh stack")
	}

	// Verify the simulation includes resource create operations.
	var createCount int
	for _, ru := range simResult.ResourceUpdates {
		t.Logf("simulate resource: %s (%s) operation=%s", ru.Label, ru.Type, ru.Operation)
		if ru.Operation == "create" {
			createCount++
		}
	}
	if createCount == 0 {
		t.Error("expected at least one create operation in simulation")
	}

	// Verify no resources were actually created.
	resources := cli.Inventory(t, "--query", "stack:e2e-simulate-aws")
	if len(resources) != 0 {
		t.Fatalf("expected 0 resources after simulate (no real changes), got %d", len(resources))
	}

	// Step 2: Apply for real, then simulate an update.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Verify resources exist now.
	resources = cli.Inventory(t, "--query", "stack:e2e-simulate-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource after real apply, got %d", len(resources))
	}

	// Make an OOB change so the next simulate detects drift.
	tagAWSRole(t, "formae-e2e-simulate-role", "SimTag", "sim-value")

	// Wait for the agent to detect the OOB change.
	WaitForOOBChange(t, cli, "stack:e2e-simulate-aws", "e2e-simulate-role",
		"Tags", "SimTag", "sim-value", 60*time.Second)

	// Simulate a force reconcile — should report an update to revert the tag
	// but NOT actually modify the resource.
	simResult2 := cli.Simulate(t, "reconcile", fixture, "--force")
	if !simResult2.ChangesRequired {
		t.Fatal("expected simulate to report ChangesRequired=true for drifted resource")
	}

	var updateCount int
	for _, ru := range simResult2.ResourceUpdates {
		t.Logf("simulate update: %s (%s) operation=%s", ru.Label, ru.Type, ru.Operation)
		if ru.Operation == "update" {
			updateCount++
		}
	}
	if updateCount == 0 {
		t.Error("expected at least one update operation in simulation")
	}

	// Verify the OOB tag is still present (simulate didn't revert it).
	resources = cli.Inventory(t, "--query", "stack:e2e-simulate-aws")
	role := RequireResource(t, resources, "e2e-simulate-role")
	AssertTagExists(t, role, "Tags", "SimTag", "sim-value")

	// Step 3: Force reconcile to clear drift, then destroy.
	forceID := cli.Apply(t, "reconcile", fixture, "--force")
	forceResult := cli.WaitForCommand(t, forceID, commandTimeout)
	RequireCommandSuccess(t, forceResult)

	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	remaining := cli.Inventory(t, "--query", "stack:e2e-simulate-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
