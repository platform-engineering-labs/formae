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

func TestTTL(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	cli := NewFormaeCLI(bin, agent.ConfigPath(), agent.Port())

	fixture := filepath.Join(fixturesDir(t), "ttl_aws.pkl")
	commandTimeout := 2 * time.Minute

	// Step 1: Apply reconcile fixture — creates role + attaches TTL policy.
	cmdID := cli.Apply(t, "reconcile", fixture)
	result := cli.WaitForCommand(t, cmdID, commandTimeout)
	RequireCommandSuccess(t, result)

	// Step 2: Verify 1 resource in the stack.
	resources := cli.Inventory(t, "--query", "stack:e2e-ttl-aws")
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	RequireResource(t, resources, "e2e-ttl-role")

	// Step 3: Wait for TTL to expire and the stack to be destroyed.
	// TTL is 15s, StackExpirer polls every 5s, then the destroy command
	// needs to execute. We allow 90s total.
	WaitForStackEmpty(t, cli, "stack:e2e-ttl-aws", 90*time.Second)

	// Step 4: Verify the stack is empty.
	remaining := cli.Inventory(t, "--query", "stack:e2e-ttl-aws")
	if len(remaining) != 0 {
		t.Errorf("expected 0 resources after TTL expiry, got %d", len(remaining))
	}
}
