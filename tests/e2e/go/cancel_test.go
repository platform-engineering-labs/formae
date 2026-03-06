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

func TestCancelCommand(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "reconcile_apply_aws.pkl")
	commandTimeout := 5 * time.Minute

	// Step 1: Apply reconcile fixture (7 resources with dependency DAG).
	cmdID := cli.Apply(t, "reconcile", fixture)

	// Step 2: Wait briefly for the command to begin processing, then cancel.
	// The agent needs a moment to transition the command to InProgress state.
	time.Sleep(2 * time.Second)
	canceledIDs := cli.Cancel(t, "id:"+cmdID)
	t.Logf("canceled command IDs: %v", canceledIDs)

	// Step 3: Wait for the command to reach a terminal state.
	result := cli.WaitForCommand(t, cmdID, commandTimeout)

	// Step 4: Check the command state. The cancel might arrive before, during,
	// or after all resources have been processed.
	switch result.State {
	case "Canceled":
		t.Logf("command was canceled as expected")
	case "Success", "NoOp":
		// The apply completed before the cancel arrived — this is acceptable.
		t.Logf("command completed before cancel arrived (state: %s)", result.State)
	default:
		t.Fatalf("unexpected command state: %s (expected Canceled or Success)", result.State)
	}

	// Step 5: Inspect resource updates — every update should be in a terminal
	// state with no failures.
	if result.State == "Canceled" {
		var successCount, canceledCount int
		for _, ru := range result.ResourceUpdates {
			switch ru.State {
			case "Success":
				successCount++
			case "Canceled":
				canceledCount++
			default:
				t.Errorf("resource %s (%s) in unexpected state: %s", ru.Label, ru.Type, ru.State)
			}
		}
		t.Logf("resource updates: %d success, %d canceled (of %d total)",
			successCount, canceledCount, len(result.ResourceUpdates))

		if canceledCount == 0 {
			t.Log("warning: no resources were canceled — cancel arrived after all were processed")
		}
	}

	// Step 6: Destroy to clean up any resources that were created.
	destroyID := cli.Destroy(t, fixture)
	destroyResult := cli.WaitForCommand(t, destroyID, commandTimeout)
	RequireCommandSuccess(t, destroyResult)

	// Step 7: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-reconcile-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after destroy, got %d", len(remaining))
	}
}
