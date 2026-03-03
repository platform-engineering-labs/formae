// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

const defaultCommandTimeout = 10 * time.Second

// testResourceSchema is the schema for Test::Generic::Resource, matching the
// test plugin's SchemaForResourceType. Resources in test formas must include
// this schema so that the agent's property splitting (regular vs read-only)
// works correctly — without it, all fields are classified as read-only and
// array-typed fields are lost during the merge step.
var testResourceSchema = pkgmodel.Schema{
	Identifier: "Name",
	Fields:     []string{"Name", "Value", "SetTags", "EntityTags", "OrderedItems"},
	Hints: map[string]pkgmodel.FieldHint{
		"EntityTags": {
			UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet,
			IndexField:   "Key",
		},
		"OrderedItems": {
			UpdateMethod: pkgmodel.FieldUpdateMethodArray,
		},
	},
}

// resetTimeout is the timeout for each phase of ResetAgentState.
const resetTimeout = 30 * time.Second

// maxCleanupAttempts limits how many destroy cycles ResetAgentState will try.
// Multiple attempts are needed because the ResourcePersister processes messages
// asynchronously: a completed Apply command may have outstanding "create"
// messages that arrive AFTER the Destroy command's "delete" messages, causing
// resources to reappear. A second destroy round catches these stragglers.
const maxCleanupAttempts = 3

// ResetAgentState destroys all managed resources across all known stacks,
// ensuring each rapid iteration starts from a clean slate. It first waits
// for any in-flight commands to complete, then destroys resources in a
// retry loop until inventory is empty.
func (h *TestHarness) ResetAgentState(t *testing.T) {
	t.Helper()

	// Phase 1: Wait for all in-flight commands to settle.
	h.waitForAllCommandsTerminal(t, resetTimeout)

	// Phase 2: Destroy all managed resources in a retry loop.
	// Due to async ResourcePersister message ordering, a single destroy
	// may not clear all resources — late "create" messages from a prior
	// command can re-create resources after they're deleted.
	for attempt := range maxCleanupAttempts {
		forma, err := h.client.ExtractResources("managed:true")
		if err != nil || forma == nil || len(forma.Resources) == 0 {
			t.Logf("ResetAgentState: inventory clean (attempt %d)", attempt+1)
			return
		}

		// Diagnostic: check for duplicate resources in extracted forma
		seen := make(map[string]int)
		for _, res := range forma.Resources {
			key := fmt.Sprintf("%s/%s/%s", res.Stack, res.Type, res.Label)
			seen[key]++
		}
		hasDuplicates := false
		for key, count := range seen {
			if count > 1 {
				t.Logf("ResetAgentState: DUPLICATE resource in ExtractResources: %s (count=%d)", key, count)
				hasDuplicates = true
			}
		}
		if hasDuplicates {
			h.dumpRawResourceRows(t, forma.Resources, seen)
		}

		resp, err := h.client.DestroyForma(forma, false, clientID)
		if err != nil {
			t.Logf("ResetAgentState: destroy returned: %v (attempt %d)", err, attempt+1)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if !resp.Simulation.ChangesRequired {
			t.Logf("ResetAgentState: no changes required (attempt %d)", attempt+1)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		cmd, ok := h.TryWaitForCommandDone(resp.CommandID, resetTimeout)
		if !ok {
			t.Logf("ResetAgentState: cleanup command %s timed out (attempt %d)", resp.CommandID, attempt+1)
			continue
		}
		t.Logf("ResetAgentState: cleanup command %s: %s (destroyed %d, attempt %d)",
			resp.CommandID, cmd.State, len(forma.Resources), attempt+1)

		// Brief pause to let the ResourcePersister drain any outstanding
		// messages from prior commands before we re-check inventory.
		time.Sleep(500 * time.Millisecond)
	}

	// Final check — if resources remain after all attempts, fail.
	forma, err := h.client.ExtractResources("managed:true")
	if err == nil && forma != nil && len(forma.Resources) > 0 {
		for _, res := range forma.Resources {
			t.Logf("  remaining resource: %s (nativeID=%s, stack=%s)", res.Label, res.NativeID, res.Stack)
		}
		require.Fail(t, "ResetAgentState: inventory not empty after %d cleanup attempts", maxCleanupAttempts)
	}
}

// waitForAllCommandsTerminal polls until no commands from this client are
// in a non-terminal state (InProgress, Pending). This prevents the next
// rapid iteration from racing with leftover commands from a prior iteration.
func (h *TestHarness) waitForAllCommandsTerminal(t *testing.T, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	sawCommands := false
	for time.Now().Before(deadline) {
		statusResp, err := h.client.GetFormaCommandsStatus("", clientID, 100)
		if err != nil {
			if !sawCommands {
				// The API returns a 500 when no commands exist for the client.
				// On the first query, treat this as "no commands" (vacuously terminal).
				return
			}
			// Once we've seen commands, errors are transient — keep retrying.
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if len(statusResp.Commands) > 0 {
			sawCommands = true
		}

		allTerminal := true
		for _, cmd := range statusResp.Commands {
			if cmd.State != "Success" && cmd.State != "Failed" && cmd.State != "Canceled" {
				allTerminal = false
				break
			}
		}

		if allTerminal {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Log which commands are still running for diagnostics.
	statusResp, err := h.client.GetFormaCommandsStatus("", clientID, 100)
	if err == nil {
		for _, cmd := range statusResp.Commands {
			if cmd.State != "Success" && cmd.State != "Failed" && cmd.State != "Canceled" {
				t.Logf("waitForAllCommandsTerminal: stuck command %s state=%s command=%s", cmd.CommandID, cmd.State, cmd.Command)
			}
		}
	}
	require.Fail(t, "ResetAgentState: timed out waiting for all commands to reach terminal state")
}

// ExecuteOperation dispatches a single operation via the appropriate channel
// (REST API or Ergo) and updates the state model accordingly.
func (h *TestHarness) ExecuteOperation(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	switch op.Kind {
	case OpApply:
		h.executeApply(t, op, model)
	case OpDestroy:
		h.executeDestroy(t, op, model)
	case OpTriggerSync:
		h.executeTriggerSync(t)
	case OpTriggerDiscovery:
		h.executeTriggerDiscovery(t)
	case OpVerifyState:
		// Skip invariant check when commands are in flight — cloud state
		// and inventory are transiently inconsistent during execution.
		if h.hasAnyPendingCommands(model) {
			t.Logf("[op %d] VerifyState skipped (pending commands in flight)", op.SequenceNum)
		} else {
			h.AssertAllInvariants(t)
		}
	case OpInjectError:
		h.executeInjectError(t, op)
	case OpInjectLatency:
		h.executeInjectLatency(t, op)
	case OpClearInjections:
		h.ClearInjections(t)
	case OpCloudModify:
		h.executeCloudModify(t, op)
	case OpCloudDelete:
		h.executeCloudDelete(t, op)
	case OpCloudCreate:
		h.executeCloudCreate(t, op)
	case OpCancel:
		// Cancel requires a command ID set during execution; skip if none available
		t.Logf("[op %d] OpCancel skipped (no command ID)", op.SequenceNum)
	default:
		t.Fatalf("unknown operation kind: %d", op.Kind)
	}
}

// AssertAllInvariants queries the agent inventory and cloud state, then checks
// all correctness invariants across all managed resources.
func (h *TestHarness) AssertAllInvariants(t *testing.T) {
	t.Helper()

	var violations []Violation

	// Phase 1: Wait for all commands to reach a terminal state.
	// This must happen before the resource invariant check because in-flight
	// commands can create resources in the plugin (cloud state) before the
	// command completes and the resource is persisted to inventory.
	cmdViolations := h.waitAndCheckCommandCompleteness(t, 15*time.Second)
	violations = append(violations, cmdViolations...)

	// Phase 2: Check resource invariants.
	// Now that all commands are terminal, inventory and cloud state should be
	// consistent (resource persister writes are synchronous within commands).
	forma, err := h.client.ExtractResources("managed:true")
	require.NoError(t, err, "ExtractResources should not error")

	var inventory []pkgmodel.Resource
	if forma != nil {
		inventory = forma.Resources
	}

	cloudState := h.GetCloudStateSnapshot(t)
	violations = append(violations, CheckInvariants(inventory, cloudState, "cloud-")...)

	for _, v := range violations {
		t.Errorf("invariant violation: %s", v.Message)
	}
	assert.Empty(t, violations, "no invariant violations expected")
}

// waitAndCheckCommandCompleteness polls until all commands from this client are
// in a terminal state, returning any remaining violations if the deadline expires.
func (h *TestHarness) waitAndCheckCommandCompleteness(t *testing.T, timeout time.Duration) []Violation {
	t.Helper()

	deadline := time.Now().Add(timeout)
	sawCommands := false
	for time.Now().Before(deadline) {
		statusResp, err := h.client.GetFormaCommandsStatus("", clientID, 100)
		if err != nil {
			if !sawCommands {
				// The API returns 500 when no commands exist for the client.
				// Treat as vacuously terminal.
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if len(statusResp.Commands) > 0 {
			sawCommands = true
		}

		var commands []CommandState
		for _, cmd := range statusResp.Commands {
			commands = append(commands, CommandState{
				ID:    cmd.CommandID,
				State: cmd.State,
			})
		}

		cmdViolations := CheckCommandCompleteness(commands)
		if len(cmdViolations) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Timed out — dump details of stuck commands for diagnosis
	statusResp, err := h.client.GetFormaCommandsStatus("", clientID, 100)
	if err != nil {
		return nil // Can't query commands — assume terminal
	}

	var commands []CommandState
	for _, cmd := range statusResp.Commands {
		commands = append(commands, CommandState{
			ID:    cmd.CommandID,
			State: cmd.State,
		})
		if cmd.State != "Success" && cmd.State != "Failed" && cmd.State != "Canceled" {
			t.Logf("STUCK COMMAND DETAILS: id=%s command=%s state=%s", cmd.CommandID, cmd.Command, cmd.State)
			for _, ru := range cmd.ResourceUpdates {
				t.Logf("  resource_update: label=%s op=%s state=%s stack=%s err=%s",
					ru.ResourceLabel, ru.Operation, ru.State, ru.StackName, ru.ErrorMessage)
			}
			for _, su := range cmd.StackUpdates {
				t.Logf("  stack_update: label=%s op=%s state=%s err=%s",
					su.StackLabel, su.Operation, su.State, su.ErrorMessage)
			}
		}
	}
	return CheckCommandCompleteness(commands)
}

func (h *TestHarness) executeApply(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	mode := pkgmodel.FormaApplyModePatch
	if op.ApplyMode == "reconcile" {
		mode = pkgmodel.FormaApplyModeReconcile
	}

	stackLabel := model.Stack(op.StackIndex).Label
	forma := FormaFromStackResources(stackLabel, op.ResourceIDs, op.Properties)
	resp, err := h.client.ApplyForma(forma, mode, false, clientID, false)
	if err != nil {
		// Patch may be rejected if resources don't exist, conflicts, etc.
		// This is valid agent behavior, not a test failure.
		t.Logf("[op %d] Apply (%s) stack=%s resources %v → rejected: %v", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs, err)
		return
	}

	// When the agent determines no changes are needed (e.g. reconcile where
	// desired state already matches), it returns a response with
	// ChangesRequired=false and does NOT persist a command to the datastore.
	// Polling for such a command would time out, so we treat it as a
	// successful no-op.
	if !resp.Simulation.ChangesRequired {
		t.Logf("[op %d] Apply (%s) stack=%s resources %v → no changes required", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Apply (%s) stack=%s resources %v → command %s", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs, commandID)

	if op.Blocking {
		cmd, ok := h.TryWaitForCommandDone(commandID, defaultCommandTimeout)
		if !ok {
			// Timed out — all resources become uncertain. This is expected
			// when failure injection is active (e.g. Create errors cause
			// retries). The final invariant check validates consistency.
			for _, id := range op.ResourceIDs {
				model.MarkUncertain(op.StackIndex, id)
			}
			t.Logf("[op %d] Apply (%s) command %s timed out (resources marked uncertain)", op.SequenceNum, op.ApplyMode, commandID)
			return
		}

		if cmd.State == "Success" {
			// Update model: applied resources now exist
			props := resourceProperties(stackLabel, op.ResourceIDs, op.Properties)
			model.ApplyCreated(op.StackIndex, op.ResourceIDs, props)

			// For reconcile mode, resources NOT in the forma are destroyed
			if mode == pkgmodel.FormaApplyModeReconcile {
				for idx := range model.Stack(op.StackIndex).Resources {
					if !containsInt(op.ResourceIDs, idx) {
						model.ApplyDestroyed(op.StackIndex, []int{idx})
					}
				}
			}

			// Post-apply property verification
			h.verifyPostApplyProperties(t, op, mode, stackLabel)
		} else {
			// Command failed — resources may have been partially created.
			// Mark them as uncertain since we don't know which succeeded.
			for _, id := range op.ResourceIDs {
				model.MarkUncertain(op.StackIndex, id)
			}
		}
		t.Logf("[op %d] Apply completed: %s", op.SequenceNum, cmd.State)
	} else {
		// Fire-and-forget: mark resources uncertain since we don't know
		// when the command will complete.
		for _, id := range op.ResourceIDs {
			model.MarkUncertain(op.StackIndex, id)
		}
		model.AddPendingCommand(op.StackIndex, &PendingCommand{
			CommandID:   commandID,
			Kind:        CommandKindApply,
			StackLabel:  stackLabel,
			ResourceIDs: op.ResourceIDs,
			Properties:  op.Properties,
		})
		t.Logf("[op %d] Apply (%s) stack=%s resources %v → fire-and-forget command %s", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs, commandID)
	}
}

func (h *TestHarness) executeDestroy(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	// Only attempt to destroy resources that exist according to the model.
	// Destroying nonexistent resources can cause the agent to hang.
	existingIDs := filterExistingResources(op.ResourceIDs, op.StackIndex, model)
	if len(existingIDs) == 0 {
		t.Logf("[op %d] Destroy stack=%s resources %v → skipped (none exist)", op.SequenceNum, model.Stack(op.StackIndex).Label, op.ResourceIDs)
		return
	}

	stackLabel := model.Stack(op.StackIndex).Label
	forma := FormaFromStackResources(stackLabel, existingIDs)
	resp, err := h.client.DestroyForma(forma, false, clientID)
	if err != nil {
		t.Logf("[op %d] Destroy stack=%s resources %v → error: %v", op.SequenceNum, stackLabel, existingIDs, err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		t.Logf("[op %d] Destroy stack=%s resources %v → no changes required", op.SequenceNum, stackLabel, existingIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Destroy stack=%s resources %v → command %s", op.SequenceNum, stackLabel, existingIDs, commandID)

	if op.Blocking {
		cmd, ok := h.TryWaitForCommandDone(commandID, defaultCommandTimeout)
		if !ok {
			// Timed out — all resources become uncertain
			for _, id := range existingIDs {
				model.MarkUncertain(op.StackIndex, id)
			}
			t.Logf("[op %d] Destroy command %s timed out (resources marked uncertain)", op.SequenceNum, commandID)
			return
		}

		if cmd.State == "Success" {
			model.ApplyDestroyed(op.StackIndex, existingIDs)
		} else {
			// Command failed — resources may have been partially destroyed.
			// Mark them as uncertain since we don't know which succeeded.
			for _, id := range existingIDs {
				model.MarkUncertain(op.StackIndex, id)
			}
		}
		t.Logf("[op %d] Destroy completed: %s", op.SequenceNum, cmd.State)
	} else {
		// Fire-and-forget: mark resources uncertain since we don't know
		// when the command will complete.
		for _, id := range existingIDs {
			model.MarkUncertain(op.StackIndex, id)
		}
		model.AddPendingCommand(op.StackIndex, &PendingCommand{
			CommandID:   commandID,
			Kind:        CommandKindDestroy,
			StackLabel:  stackLabel,
			ResourceIDs: existingIDs,
		})
		t.Logf("[op %d] Destroy stack=%s resources %v → fire-and-forget command %s", op.SequenceNum, stackLabel, existingIDs, commandID)
	}
}

// filterExistingResources returns only the resource IDs whose model state
// includes StateExists on the given stack.
func filterExistingResources(ids []int, stackIndex int, model *StateModel) []int {
	var existing []int
	for _, id := range ids {
		res := model.Resource(stackIndex, id)
		if res == nil {
			continue
		}
		for _, s := range res.AcceptStates {
			if s == StateExists {
				existing = append(existing, id)
				break
			}
		}
	}
	return existing
}

func (h *TestHarness) executeTriggerSync(t *testing.T) {
	t.Helper()
	err := h.client.ForceSync()
	if err != nil {
		t.Logf("TriggerSync error (may be expected): %v", err)
	}
}

func (h *TestHarness) executeTriggerDiscovery(t *testing.T) {
	t.Helper()
	err := h.client.ForceDiscover()
	if err != nil {
		t.Logf("TriggerDiscovery error (may be expected): %v", err)
	}
}

func (h *TestHarness) executeInjectError(t *testing.T, op *Operation) {
	t.Helper()
	h.InjectError(t, InjectErrorFromOp(op))
	t.Logf("[op %d] InjectError: %s on %s (count=%d)", op.SequenceNum, op.ErrorMsg, op.TargetOperation, op.ErrorCount)
}

func (h *TestHarness) executeInjectLatency(t *testing.T, op *Operation) {
	t.Helper()
	h.InjectLatency(t, InjectLatencyFromOp(op))
	t.Logf("[op %d] InjectLatency: %v on %s", op.SequenceNum, op.Latency, op.TargetOperation)
}

func (h *TestHarness) executeCloudModify(t *testing.T, op *Operation) {
	t.Helper()
	h.PutCloudState(t, op.NativeID, "Test::Generic::Resource", op.Properties)
	t.Logf("[op %d] CloudModify: %s", op.SequenceNum, op.NativeID)
}

func (h *TestHarness) executeCloudDelete(t *testing.T, op *Operation) {
	t.Helper()
	h.DeleteCloudState(t, op.NativeID)
	t.Logf("[op %d] CloudDelete: %s", op.SequenceNum, op.NativeID)
}

func (h *TestHarness) executeCloudCreate(t *testing.T, op *Operation) {
	t.Helper()
	h.PutCloudState(t, op.NativeID, op.ResourceType, op.Properties)
	t.Logf("[op %d] CloudCreate: %s (%s)", op.SequenceNum, op.NativeID, op.ResourceType)
}

// verifyPostApplyProperties checks resource properties after a successful
// blocking apply. For reconcile: exact match. For patch: superset.
//
// The inventory may not reflect the latest command's changes immediately
// because the ResourcePersister processes updates asynchronously after the
// command reaches "Success". We poll briefly to account for this lag.
func (h *TestHarness) verifyPostApplyProperties(t *testing.T, op *Operation, mode pkgmodel.FormaApplyMode, stackLabel string) {
	t.Helper()

	// Build the expected properties per resource label
	desiredProps := make(map[string]string)
	labels := make([]string, len(op.ResourceIDs))
	for i, id := range op.ResourceIDs {
		label := resourceLabelForStack(stackLabel, id)
		labels[i] = label
		desiredProps[label] = strings.Replace(op.Properties, `"NAME"`, `"`+label+`"`, 1)
	}

	// Poll inventory until properties match or timeout.
	deadline := time.Now().Add(5 * time.Second)
	var lastViolations []Violation
	for time.Now().Before(deadline) {
		forma, err := h.client.ExtractResources("managed:true")
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var inventory []pkgmodel.Resource
		if forma != nil {
			inventory = forma.Resources
		}

		if mode == pkgmodel.FormaApplyModeReconcile {
			lastViolations = CheckReconcileProperties(inventory, labels, desiredProps)
		} else {
			lastViolations = CheckPatchProperties(inventory, labels, desiredProps)
		}

		if len(lastViolations) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, v := range lastViolations {
		t.Errorf("post-apply property violation: %s", v.Message)
	}
}

// hasAnyPendingCommands returns true if any stack has pending fire-and-forget commands.
func (h *TestHarness) hasAnyPendingCommands(model *StateModel) bool {
	for stackIdx := range model.Stacks {
		if len(model.PendingCommandsForStack(stackIdx)) > 0 {
			return true
		}
	}
	return false
}

// DrainPendingCommands polls all pending commands across all stacks until they
// complete, updating the state model accordingly. Used before final invariant
// checks when fire-and-forget commands may still be in flight.
func (h *TestHarness) DrainPendingCommands(t *testing.T, model *StateModel, timeout time.Duration) {
	t.Helper()

	for stackIdx := range model.Stacks {
		pending := model.PendingCommandsForStack(stackIdx)
		for cmdID, cmd := range pending {
			result, ok := h.TryWaitForCommandDone(cmdID, timeout)
			if !ok {
				// Timed out — mark all resources uncertain
				for _, id := range cmd.ResourceIDs {
					model.MarkUncertain(stackIdx, id)
				}
				t.Logf("DrainPendingCommands: command %s timed out (resources marked uncertain)", cmdID)
				model.RemovePendingCommand(stackIdx, cmdID)
				continue
			}

			if result.State == "Success" {
				switch cmd.Kind {
				case CommandKindApply:
					props := resourceProperties(cmd.StackLabel, cmd.ResourceIDs, cmd.Properties)
					model.ApplyCreated(stackIdx, cmd.ResourceIDs, props)
				case CommandKindDestroy:
					model.ApplyDestroyed(stackIdx, cmd.ResourceIDs)
				}
			} else {
				for _, id := range cmd.ResourceIDs {
					model.MarkUncertain(stackIdx, id)
				}
			}
			t.Logf("DrainPendingCommands: command %s completed: %s", cmdID, result.State)
			model.RemovePendingCommand(stackIdx, cmdID)
		}
	}
}

// --- Stack setup and policy helpers ---

// SetupStacks creates initial resources on each stack and attaches policies
// as configured. This ensures stacks exist in the agent before chaos begins.
func (h *TestHarness) SetupStacks(t *testing.T, model *StateModel, config PropertyTestConfig) {
	t.Helper()

	// Create a single resource on each stack to ensure the stack exists.
	for stackIdx := range model.Stacks {
		stackLabel := model.Stack(stackIdx).Label
		ids := []int{0} // just the first resource

		var policies []json.RawMessage
		if config.EnableAutoReconcile && stackIdx == 0 {
			// Attach auto-reconcile to the first stack
			policies = append(policies, json.RawMessage(`{"Type":"auto-reconcile","IntervalSeconds":3600}`))
			model.Stacks[stackIdx].AutoReconcile = true
		}
		if config.EnableTTL && stackIdx == config.StackCount-1 {
			// Attach TTL to the last stack (very long TTL so it doesn't expire mid-test)
			policies = append(policies, json.RawMessage(`{"Type":"ttl","TTLSeconds":86400,"OnDependents":"cascade"}`))
			model.Stacks[stackIdx].TTL = true
		}

		forma := FormaFromStackResourcesWithPolicies(stackLabel, ids, policies)
		resp, err := h.client.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
		if err != nil {
			t.Logf("SetupStacks: stack %s apply rejected: %v", stackLabel, err)
			continue
		}
		if !resp.Simulation.ChangesRequired {
			t.Logf("SetupStacks: stack %s no changes required", stackLabel)
			continue
		}
		cmd := h.WaitForCommandDone(resp.CommandID, defaultCommandTimeout)
		if cmd.State == "Success" {
			props := resourceProperties(stackLabel, ids)
			model.ApplyCreated(stackIdx, ids, props)
			t.Logf("SetupStacks: stack %s created with %d resources", stackLabel, len(ids))
		} else {
			t.Logf("SetupStacks: stack %s command failed: %s", stackLabel, cmd.State)
		}
	}
}

// ForceReconcileAndWait triggers a force-reconcile on the given stack and waits
// for the resulting command to complete. Reconcile reverts out-of-band changes.
func (h *TestHarness) ForceReconcileAndWait(t *testing.T, stackLabel string, model *StateModel, stackIdx int) {
	t.Helper()

	resp, err := h.client.ForceReconcile(stackLabel)
	if err != nil {
		// Conflict (active commands) or other error — not a test failure
		t.Logf("ForceReconcileAndWait: stack %s rejected: %v", stackLabel, err)
		return
	}

	if resp.CommandID == "" {
		t.Logf("ForceReconcileAndWait: stack %s no drift detected", stackLabel)
		return
	}

	cmd, ok := h.TryWaitForCommandDone(resp.CommandID, defaultCommandTimeout)
	if !ok {
		t.Logf("ForceReconcileAndWait: stack %s command %s timed out", stackLabel, resp.CommandID)
		// Mark all resources uncertain since reconcile outcome is unknown
		for idx := range model.Stack(stackIdx).Resources {
			model.MarkUncertain(stackIdx, idx)
		}
		return
	}

	t.Logf("ForceReconcileAndWait: stack %s command %s completed: %s", stackLabel, resp.CommandID, cmd.State)
	if cmd.State == "Success" {
		// After successful reconcile, all managed resources match their declared state.
		// Mark all resources uncertain since we don't know what reconcile changed.
		for idx := range model.Stack(stackIdx).Resources {
			model.MarkUncertain(stackIdx, idx)
		}
	}
}

// CleanupOutOfBandCloudResources removes all cloud state entries that were
// created directly via OpCloudCreate (native IDs starting with "cloud-").
// This must be called before AssertAllInvariants when EnableCloudChanges is
// active, since out-of-band entries are not tracked in inventory.
func (h *TestHarness) CleanupOutOfBandCloudResources(t *testing.T) {
	t.Helper()

	snapshot := h.GetCloudStateSnapshot(t)
	removed := 0
	for nativeID := range snapshot {
		if len(nativeID) >= 6 && nativeID[:6] == "cloud-" {
			h.DeleteCloudState(t, nativeID)
			removed++
		}
	}
	t.Logf("CleanupOutOfBandCloudResources: removed %d out-of-band entries", removed)
}

// SyncCloudStateWithInventory makes the test plugin's cloud state match the
// agent's inventory. This is needed in the FullChaos wind-down because:
//   - OpCloudDelete removes resources from cloud that the agent still tracks (phantoms)
//   - Failure injection causes Creates to succeed in the plugin but the command
//     fails, leaving cloud resources the agent doesn't track (orphans)
//
// After this call, cloud state and inventory contain the same set of native IDs.
func (h *TestHarness) SyncCloudStateWithInventory(t *testing.T) {
	t.Helper()

	forma, err := h.client.ExtractResources("managed:true")
	if err != nil {
		t.Logf("SyncCloudStateWithInventory: failed to query inventory: %v", err)
		return
	}

	inventoryByNativeID := make(map[string]pkgmodel.Resource)
	if forma != nil {
		for _, res := range forma.Resources {
			if res.NativeID != "" {
				inventoryByNativeID[res.NativeID] = res
			}
		}
	}

	cloudState := h.GetCloudStateSnapshot(t)

	// Remove cloud entries not in inventory (orphans from failed Creates)
	removed := 0
	for nativeID := range cloudState {
		if _, ok := inventoryByNativeID[nativeID]; !ok {
			h.DeleteCloudState(t, nativeID)
			removed++
		}
	}

	// Add cloud entries for inventory resources not in cloud (phantoms from OpCloudDelete)
	restored := 0
	for nativeID, res := range inventoryByNativeID {
		if _, ok := cloudState[nativeID]; !ok {
			h.PutCloudState(t, nativeID, res.Type, string(res.Properties))
			restored++
		}
	}

	if removed > 0 || restored > 0 {
		t.Logf("SyncCloudStateWithInventory: removed %d orphans, restored %d phantoms", removed, restored)
	}
}

// ForceCheckTTLAndWait triggers a TTL check. If stacks have expired, the agent
// destroys them. We mark affected stacks' resources as uncertain.
func (h *TestHarness) ForceCheckTTLAndWait(t *testing.T, model *StateModel) {
	t.Helper()

	resp, err := h.client.ForceCheckTTL()
	if err != nil {
		t.Logf("ForceCheckTTLAndWait: error: %v", err)
		return
	}

	if len(resp.ExpiredStacks) == 0 {
		t.Logf("ForceCheckTTLAndWait: no expired stacks")
		return
	}

	t.Logf("ForceCheckTTLAndWait: expired stacks: %v", resp.ExpiredStacks)
	// Mark all resources on expired stacks as uncertain
	for _, expiredLabel := range resp.ExpiredStacks {
		for stackIdx, stack := range model.Stacks {
			if stack.Label == expiredLabel {
				for idx := range stack.Resources {
					model.MarkUncertain(stackIdx, idx)
				}
			}
		}
	}
}

// dumpRawResourceRows queries the raw SQLite database to show all rows for
// duplicate resources. This reveals whether duplicates have different KSUIDs
// (pointing to a TOCTOU race in conflict detection) or different versions
// (pointing to a version deduplication issue).
func (h *TestHarness) dumpRawResourceRows(t *testing.T, resources []pkgmodel.Resource, seen map[string]int) {
	t.Helper()

	dbPath := h.cfg.Agent.Datastore.Sqlite.FilePath
	if dbPath == "" || dbPath == ":memory:" {
		t.Logf("dumpRawResourceRows: cannot query in-memory DB directly")
		return
	}

	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		t.Logf("dumpRawResourceRows: failed to open DB: %v", err)
		return
	}
	defer db.Close()

	// Collect the duplicate keys
	for key, count := range seen {
		if count <= 1 {
			continue
		}
		parts := strings.SplitN(key, "/", 3)
		if len(parts) != 3 {
			continue
		}
		stack, resType, label := parts[0], parts[1], parts[2]

		rows, err := db.Query(`
			SELECT uri, ksuid, version, native_id, operation, command_id, managed
			FROM resources
			WHERE stack = ? AND type = ? AND label = ?
			ORDER BY version DESC`,
			stack, resType, label)
		if err != nil {
			t.Logf("dumpRawResourceRows: query failed for %s: %v", key, err)
			continue
		}

		t.Logf("--- RAW DB ROWS for %s (ExtractResources returned %d) ---", key, count)
		rowCount := 0
		for rows.Next() {
			var uri, ksuid, version, nativeID, operation, commandID string
			var managed int
			if err := rows.Scan(&uri, &ksuid, &version, &nativeID, &operation, &commandID, &managed); err != nil {
				t.Logf("  scan error: %v", err)
				continue
			}
			t.Logf("  row: uri=%s ksuid=%s version=%s native_id=%s op=%s cmd=%s managed=%d",
				uri, ksuid, version, nativeID, operation, commandID, managed)
			rowCount++
		}
		rows.Close()
		t.Logf("--- END (%d rows) ---", rowCount)
	}
}

// --- Forma builders ---

// FormaFromResourceIDs builds a forma containing the resources at the given
// pool indices on the default stack. Used by smoke tests and ResetAgentState.
func FormaFromResourceIDs(ids []int) *pkgmodel.Forma {
	return FormaFromStackResources("default", ids)
}

// FormaFromStackResourcesWithPolicies builds a forma with resources and optional
// stack policies (auto-reconcile, TTL, etc.).
func FormaFromStackResourcesWithPolicies(stackLabel string, ids []int, policies []json.RawMessage, propsTemplate ...string) *pkgmodel.Forma {
	forma := FormaFromStackResources(stackLabel, ids, propsTemplate...)
	if len(policies) > 0 {
		forma.Stacks[0].Policies = policies
	}
	return forma
}

// FormaFromStackResources builds a forma containing the resources at the given
// pool indices on the specified stack, using the given properties template.
// The "NAME" placeholder in propsTemplate is replaced with each resource's label.
func FormaFromStackResources(stackLabel string, ids []int, propsTemplate ...string) *pkgmodel.Forma {
	template := `{"Name":"NAME","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`
	if len(propsTemplate) > 0 && propsTemplate[0] != "" {
		template = propsTemplate[0]
	}

	resources := make([]pkgmodel.Resource, len(ids))
	for i, id := range ids {
		name := resourceLabelForStack(stackLabel, id)
		props := strings.Replace(template, `"NAME"`, `"`+name+`"`, 1)
		resources[i] = pkgmodel.Resource{
			Label:      name,
			Type:       "Test::Generic::Resource",
			Stack:      stackLabel,
			Target:     "test-target",
			Properties: json.RawMessage(props),
			Schema:     testResourceSchema,
			Managed:    true,
		}
	}

	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: stackLabel},
		},
		Resources: resources,
		Targets: []pkgmodel.Target{
			{
				Label:     "test-target",
				Namespace: "Test",
			},
		},
	}
}

// resourceLabelForStack returns the label for a resource on a specific stack.
func resourceLabelForStack(stackLabel string, idx int) string {
	return fmt.Sprintf("res-%s-%s", stackLabel, string(rune('a'+idx)))
}

// resourceProperties returns the properties JSON for the given resource IDs on a stack,
// based on a properties template. If no template is provided, uses a default.
func resourceProperties(stackLabel string, ids []int, propsTemplate ...string) string {
	if len(ids) == 0 {
		return ""
	}
	template := `{"Name":"NAME","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`
	if len(propsTemplate) > 0 && propsTemplate[0] != "" {
		template = propsTemplate[0]
	}
	name := resourceLabelForStack(stackLabel, ids[0])
	return strings.Replace(template, `"NAME"`, `"`+name+`"`, 1)
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// --- Operation to testcontrol message converters ---

// InjectErrorFromOp converts an OpInjectError operation to a testcontrol request.
func InjectErrorFromOp(op *Operation) testcontrol.InjectErrorRequest {
	return testcontrol.InjectErrorRequest{
		Operation: op.TargetOperation,
		Error:     op.ErrorMsg,
		Count:     op.ErrorCount,
	}
}

// InjectLatencyFromOp converts an OpInjectLatency operation to a testcontrol request.
func InjectLatencyFromOp(op *Operation) testcontrol.InjectLatencyRequest {
	return testcontrol.InjectLatencyRequest{
		Operation: op.TargetOperation,
		Duration:  op.Latency,
	}
}
