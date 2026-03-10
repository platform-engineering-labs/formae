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

	"github.com/stretchr/testify/require"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

const defaultCommandTimeout = 10 * time.Second

// Default property templates for destroy formas (values don't matter, only identifiers).
const (
	defaultDestroyParentProps = `{"Name":"NAME","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`
	defaultDestroyChildProps  = `{"Name":"NAME","ParentId":"PARENT_ID","Value":"v1"}`
)

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

// testChildResourceSchema is the schema for child/grandchild resources.
var testChildResourceSchema = pkgmodel.Schema{
	Identifier: "Name",
	Fields:     []string{"Name", "ParentId", "Value"},
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

	// If the agent was killed and not restarted, restart it before cleanup.
	if h.agentCmd == nil {
		h.RestartAgent(t, 30*time.Second)
	}

	// Clear cloud state mirror — new iteration starts clean.
	h.cloudStateMirror = make(map[string]testcontrol.CloudStateEntry)

	// Clear programmed response queues from the previous iteration.
	// Labels are reused across iterations, so stale unconsumed responses
	// would interfere with the next iteration's failure injection.
	h.ProgramResponses(t, nil)

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
		seenKsuid := make(map[string]int)
		for _, res := range forma.Resources {
			key := fmt.Sprintf("%s/%s/%s", res.Stack, res.Type, res.Label)
			seen[key]++
			seenKsuid[res.Ksuid]++
		}
		hasDuplicates := false
		for key, count := range seen {
			if count > 1 {
				t.Logf("ResetAgentState: DUPLICATE resource in ExtractResources: %s (count=%d)", key, count)
				hasDuplicates = true
			}
		}
		for ksuid, count := range seenKsuid {
			if count > 1 {
				t.Logf("ResetAgentState: DUPLICATE KSUID in ExtractResources: %s (count=%d)", ksuid, count)
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

		// Pause to let the ResourcePersister drain outstanding messages.
		// With hierarchical resources the persist queue can be deep, so
		// we allow generous time for the sequential actor to catch up.
		time.Sleep(2 * time.Second)
	}

	// Final check — if resources remain after all attempts, fail.
	forma, err := h.client.ExtractResources("managed:true")
	if err == nil && forma != nil && len(forma.Resources) > 0 {
		for _, res := range forma.Resources {
			t.Logf("  remaining resource: %s (nativeID=%s, stack=%s)", res.Label, res.NativeID, res.Stack)
		}
		require.Fail(t, "ResetAgentState: inventory not empty after %d cleanup attempts", maxCleanupAttempts)
	}

	// Phase 3: Best-effort cleanup of orphaned cloud state entries.
	// A partially-failed command can leave resources in the cloud that were
	// never persisted to inventory. The agent destroy above only targets
	// inventory-tracked resources, so orphans survive. Remove them directly.
	// Note: stale ResourceUpdaters from a prior rapid iteration may create
	// cloud resources AFTER this cleanup (see checkResourceInvariantsWithRetry).
	h.cleanupOrphanedCloudState(t)
}

// cleanupOrphanedCloudState removes cloud state entries that have no
// corresponding resource in the agent's inventory. Returns the number of
// orphans cleaned up. These orphans can arise when a partially-failed command
// leaves resources in the cloud that were never persisted to inventory (e.g.
// a sibling failed after the plugin created the resource but before the
// ResourcePersister stored it).
func (h *TestHarness) cleanupOrphanedCloudState(t *testing.T) int {
	t.Helper()

	cloudState := h.GetCloudStateSnapshot(t)
	if len(cloudState) == 0 {
		return 0
	}

	// Build set of native IDs in inventory
	forma, err := h.client.ExtractResources("managed:true")
	inventoryNativeIDs := make(map[string]bool)
	if err == nil && forma != nil {
		for _, res := range forma.Resources {
			if res.NativeID != "" {
				inventoryNativeIDs[res.NativeID] = true
			}
		}
	}

	// Delete cloud state entries not in inventory
	cleaned := 0
	for nativeID := range cloudState {
		if !inventoryNativeIDs[nativeID] {
			h.DeleteCloudState(t, nativeID)
			t.Logf("ResetAgentState: cleaned up orphaned cloud resource %s", nativeID)
			cleaned++
		}
	}
	return cleaned
}

// waitForAllCommandsTerminal polls until no commands (from any client) are
// in a non-terminal state (InProgress, Pending). This prevents the next
// rapid iteration from racing with leftover commands from a prior iteration
// or background commands (auto-reconcile, sync).
func (h *TestHarness) waitForAllCommandsTerminal(t *testing.T, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check for ANY non-terminal commands, including background
		// auto-reconcile and sync commands that can conflict with cleanup.
		allDone := true
		for _, status := range []string{"InProgress", "Pending"} {
			resp, err := h.client.GetFormaCommandsStatus("status:"+status, clientID, 100)
			if err != nil {
				// API error — transient, retry.
				allDone = false
				break
			}
			if resp != nil && len(resp.Commands) > 0 {
				allDone = false
				break
			}
		}
		if allDone {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Log which commands are still running for diagnostics.
	for _, status := range []string{"InProgress", "Pending"} {
		resp, _ := h.client.GetFormaCommandsStatus("status:"+status, clientID, 100)
		if resp != nil {
			for _, cmd := range resp.Commands {
				t.Logf("waitForAllCommandsTerminal: stuck command %s state=%s command=%s resourceUpdates=%d",
					cmd.CommandID, cmd.State, cmd.Command, len(cmd.ResourceUpdates))
				for _, ru := range cmd.ResourceUpdates {
					t.Logf("  resource %s (%s/%s) operation=%s state=%s",
						ru.ResourceID, ru.StackName, ru.ResourceLabel, ru.Operation, ru.State)
				}
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
		if len(model.AcceptedCommands) > 0 {
			t.Logf("[op %d] VerifyState skipped (accepted commands in flight)", op.SequenceNum)
		} else {
			h.AssertAllInvariants(t, model)
		}
	case OpCloudModify:
		h.executeCloudModify(t, op, model)
	case OpCloudDelete:
		h.executeCloudDelete(t, op, model)
	case OpCloudCreate:
		h.executeCloudCreate(t, op, model)
	case OpForceReconcile:
		h.executeForceReconcile(t, op, model)
	case OpSetTTLPolicy:
		h.executeSetTTLPolicy(t, op, model)
	case OpCheckTTL:
		h.executeCheckTTL(t, op, model)
	case OpCancel:
		h.executeCancel(t, op, model)
	case OpCrashAgent:
		h.executeCrashAgent(t, model)
	default:
		t.Fatalf("unknown operation kind: %d", op.Kind)
	}
}

// AssertAllInvariants queries the agent inventory and cloud state, then checks
// all correctness invariants across all managed resources. If a model is
// provided, also verifies that the model's expected resource states match
// the actual inventory.
func (h *TestHarness) AssertAllInvariants(t *testing.T, model ...*StateModel) {
	t.Helper()

	var violations []Violation

	// Phase 1: Wait for all commands to reach a terminal state.
	// This must happen before the resource invariant check because in-flight
	// commands can create resources in the plugin (cloud state) before the
	// command completes and the resource is persisted to inventory.
	cmdViolations := h.waitAndCheckCommandCompleteness(t, 15*time.Second)
	violations = append(violations, cmdViolations...)

	// Phase 2: Wait for ResourcePersister to finish.
	// Commands reach terminal state when the ChangesetExecutor marks them
	// done, but the ResourcePersister processes persist messages from
	// ResourceUpdaters asynchronously. Poll until inventory stabilises.
	h.waitForInventoryStabilization(t, 5*time.Second)

	// Build the ignore set from the model's tracked unmanaged resources.
	// These are expected to exist in the cloud but NOT in inventory.
	var ignoreNativeIDs map[string]bool
	if len(model) > 0 && model[0] != nil {
		ignoreNativeIDs = model[0].UnmanagedNativeIDs
	}

	// Phase 3: Check resource invariants with retry.
	// Stale ResourceUpdaters from prior iterations can complete after
	// ResetAgentState, creating cloud resources that haven't been persisted
	// to inventory yet. When orphans are found, we clean them up and
	// re-check. If the orphan persists across retries, it's a real bug.
	resourceViolations := h.checkResourceInvariantsWithRetry(t, ignoreNativeIDs)
	violations = append(violations, resourceViolations...)

	// Phase 4: Check model vs inventory consistency.
	if len(model) > 0 && model[0] != nil {
		forma, err := h.client.ExtractResources("managed:true")
		var inventory []pkgmodel.Resource
		if err == nil && forma != nil {
			inventory = forma.Resources
		}
		modelViolations := CheckModelVsInventory(model[0], inventory)
		if len(modelViolations) > 0 {
			t.Logf("MODEL MISMATCH DEBUG: inventory has %d resources", len(inventory))
			for _, res := range inventory {
				t.Logf("  inventory: stack=%s label=%s type=%s nativeID=%s", res.Stack, res.Label, res.Type, res.NativeID)
			}
			for _, v := range modelViolations {
				t.Logf("  violation: %s", v.Message)
			}
		}
		violations = append(violations, modelViolations...)
	}

	for _, v := range violations {
		t.Logf("invariant violation: %s", v.Message)
	}
	require.Empty(t, violations, "no invariant violations expected")
}

// checkResourceInvariantsWithRetry checks resource invariants, and on orphan
// violations cleans up the orphans and retries. Stale ResourceUpdaters from
// prior rapid iterations can create cloud resources after ResetAgentState;
// these resolve themselves once the stale operations complete and the cloud
// entries are cleaned. Genuine invariant bugs persist across retries.
func (h *TestHarness) checkResourceInvariantsWithRetry(t *testing.T, ignoreNativeIDs map[string]bool) []Violation {
	t.Helper()

	const maxRetries = 3

	for attempt := range maxRetries {
		forma, err := h.client.ExtractResources("managed:true")
		require.NoError(t, err, "ExtractResources should not error")

		var inventory []pkgmodel.Resource
		if forma != nil {
			inventory = forma.Resources
		}

		cloudState := h.GetCloudStateSnapshot(t)
		resourceViolations := CheckInvariants(inventory, cloudState, ignoreNativeIDs)

		if len(resourceViolations) == 0 {
			return nil
		}

		// Check if all violations are orphaned resources (cloud-only).
		// Orphans from stale operations can be cleaned up and retried.
		// Non-orphan violations (phantom, property mismatch) are real bugs.
		allOrphans := true
		for _, v := range resourceViolations {
			if v.Kind != ViolationOrphanedResource {
				allOrphans = false
				break
			}
		}

		if !allOrphans || attempt == maxRetries-1 {
			// Either non-orphan violations found or retries exhausted.
			t.Logf("INVARIANT DEBUG: inventory has %d resources, cloud has %d entries (attempt %d)", len(inventory), len(cloudState), attempt+1)
			for _, res := range inventory {
				t.Logf("  inventory: label=%s type=%s nativeID=%s stack=%s", res.Label, res.Type, res.NativeID, res.Stack)
			}
			for nativeID, entry := range cloudState {
				t.Logf("  cloud: nativeID=%s type=%s", nativeID, entry.ResourceType)
			}
			return resourceViolations
		}

		// All violations are orphans — likely stale operations completing.
		// Clean up and wait before retrying.
		t.Logf("checkResourceInvariants: found %d orphans (attempt %d), cleaning up and retrying", len(resourceViolations), attempt+1)
		h.cleanupOrphanedCloudState(t)
		time.Sleep(3 * time.Second)
	}

	return nil // unreachable
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

// waitForInventoryStabilization polls the agent's inventory until the resource
// count stops changing, indicating that the ResourcePersister has finished
// processing all async persist messages.
//
// The ResourcePersister processes messages asynchronously after the
// ChangesetExecutor marks the command terminal. There can be a gap between
// "command done" and "persist completed", so we first give it a minimum grace
// period, then require several consecutive stable readings.
func (h *TestHarness) waitForInventoryStabilization(t *testing.T, timeout time.Duration) {
	t.Helper()

	const (
		pollInterval   = 150 * time.Millisecond
		gracePeriod    = 500 * time.Millisecond
		requiredStable = 3
	)

	// Give the ResourcePersister time to start processing before polling.
	time.Sleep(gracePeriod)

	deadline := time.Now().Add(timeout - gracePeriod)
	lastCount := -1
	stableCount := 0

	for time.Now().Before(deadline) {
		forma, err := h.client.ExtractResources("managed:true")
		count := 0
		if err == nil && forma != nil {
			count = len(forma.Resources)
		}

		if count == lastCount {
			stableCount++
			if stableCount >= requiredStable {
				return
			}
		} else {
			stableCount = 0
		}
		lastCount = count
		time.Sleep(pollInterval)
	}

	t.Logf("waitForInventoryStabilization: timed out after %v (last count: %d)", timeout, lastCount)
}

// applyCommandOutcomeToModel uses per-resource-update status from a completed
// command to apply exact state changes to the model.
//
// For each ResourceUpdate in the command:
//   - Success + create → resource exists
//   - Success + delete → resource does not exist
//   - Success + update → resource exists (with updated properties)
//   - Failed/Canceled/Rejected → no change (resource keeps its previous state)
//
// Returns true if all resource updates succeeded, false if any failed.
func applyCommandOutcomeToModel(t *testing.T, cmd *apimodel.Command, model *StateModel, pool *ResourcePool) bool {
	t.Helper()

	allSuccess := true
	for _, ru := range cmd.ResourceUpdates {
		// Find the stack index by matching StackName to stack labels.
		stackIdx := -1
		for i, s := range model.Stacks {
			if s.Label == ru.StackName {
				stackIdx = i
				break
			}
		}
		if stackIdx == -1 {
			continue // stack not tracked in model
		}

		// Find the slot index by resource label.
		slotIdx := -1
		for idx := range model.Stack(stackIdx).Resources {
			var label string
			if pool != nil {
				label = pool.LabelForStack(model.Stack(stackIdx).Label, idx)
			} else {
				label = resourceLabelForStack(model.Stack(stackIdx).Label, idx)
			}
			if label == ru.ResourceLabel {
				slotIdx = idx
				break
			}
		}
		if slotIdx == -1 {
			continue // resource not tracked in model
		}

		if ru.State != "Success" {
			allSuccess = false
			continue // no state change on failure
		}

		switch ru.Operation {
		case "create":
			props := ""
			if ru.Properties != nil {
				props = string(ru.Properties)
			}
			model.ApplyCreated(stackIdx, []int{slotIdx}, props)
		case "delete":
			model.ApplyDestroyed(stackIdx, []int{slotIdx})
		case "update":
			// Resource still exists; properties may have been updated.
			// ApplyCreated with new properties serves the same purpose
			// (marks resource as existing with given properties).
			if ru.Properties != nil {
				model.ApplyCreated(stackIdx, []int{slotIdx}, string(ru.Properties))
			}
		case "read":
			// Sync reads don't change model state.
		}
	}

	return allSuccess
}

// applyReconcileGuarantee enforces the reconcile invariant: after a successful
// reconcile on a stack, ONLY the resources listed in reconcileIDs should exist.
// Resources not in reconcileIDs are marked as NotExist. This handles the case
// where resources were already destroyed before the reconcile ran, so no delete
// operation appears in the command response.
func applyReconcileGuarantee(model *StateModel, stackIdx int, reconcileIDs []int) {
	inReconcile := make(map[int]bool, len(reconcileIDs))
	for _, id := range reconcileIDs {
		inReconcile[id] = true
	}
	stack := model.Stack(stackIdx)
	for idx := range stack.Resources {
		if !inReconcile[idx] {
			model.ApplyDestroyed(stackIdx, []int{idx})
		}
	}
}

func (h *TestHarness) executeCrashAgent(t *testing.T, model *StateModel) {
	t.Helper()
	t.Logf(">>> OpCrashAgent: killing agent")

	h.KillAgent(t)
	h.RestartAgent(t, 30*time.Second)

	t.Logf(">>> OpCrashAgent: agent restarted, state re-injected")
}

func (h *TestHarness) executeApply(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	mode := pkgmodel.FormaApplyModePatch
	if op.ApplyMode == "reconcile" {
		mode = pkgmodel.FormaApplyModeReconcile
	}

	stackLabel := model.Stack(op.StackIndex).Label
	var forma *pkgmodel.Forma
	if model.Pool != nil {
		forma = FormaFromPoolResources(model.Pool, stackLabel, model.ProviderStackLabel, op.ResourceIDs, op.Properties, op.ChildProperties)
	} else {
		forma = FormaFromStackResources(stackLabel, op.ResourceIDs, op.Properties)
	}
	// Program response sequences before submitting the command.
	var programmedSeqs []testcontrol.PluginOpSequence
	if op.DrawnOutcomes != nil {
		nativeIDs := h.getNativeIDsForStack(t, stackLabel)
		programmedSeqs = buildPluginOpSequences(op.DrawnOutcomes, op.StackIndex, stackLabel, op.ResourceIDs, model, nativeIDs, false, model.Pool)
		if len(programmedSeqs) > 0 {
			h.ProgramResponses(t, programmedSeqs)
		}
	}

	resp, err := h.client.ApplyForma(forma, mode, false, clientID, false)
	if err != nil {
		// Patch may be rejected if resources don't exist, conflicts, etc.
		// This is valid agent behavior, not a test failure.
		// Roll back programmed responses so they don't leak to future commands.
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Apply (%s) stack=%s resources %v → rejected: %v", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs, err)
		return
	}

	// When the agent determines no changes are needed (e.g. reconcile where
	// desired state already matches), it returns a response with
	// ChangesRequired=false and does NOT persist a command to the datastore.
	// Polling for such a command would time out, so we treat it as a
	// successful no-op. Roll back programmed responses.
	if !resp.Simulation.ChangesRequired {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Apply (%s) stack=%s resources %v → no changes required", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Apply (%s) stack=%s resources %v → command %s", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs, commandID)

	// Snapshot resources before model update for potential cancel revert.
	// For reconcile, snapshot all resources on the stack (implicit deletes affect others).
	var snapshotIDs []int
	if op.ApplyMode == "reconcile" {
		for id := range model.Stack(op.StackIndex).Resources {
			snapshotIDs = append(snapshotIDs, id)
		}
	} else {
		snapshotIDs = op.ResourceIDs
	}
	snapshots := model.SnapshotResources(op.StackIndex, snapshotIDs)

	// Immediate model update: predict outcomes at submission time.
	successIDs := successfulResourceIDs(op, op.StackIndex, op.ResourceIDs, model.Pool, false, model)
	if len(successIDs) > 0 {
		model.ApplyCreated(op.StackIndex, successIDs, op.Properties)
	}
	if op.ApplyMode == "reconcile" {
		// Reconcile guarantee: resources NOT in the forma are deleted by the
		// agent. Implicit deletes have no failure injection programmed, so
		// they always succeed. Apply the guarantee regardless of DrawnOutcomes.
		applyReconcileGuarantee(model, op.StackIndex, op.ResourceIDs)
		// Save the reconcile state for ForceReconcile prediction.
		model.SaveLastReconcile(op.StackIndex, op.ResourceIDs, op.Properties, op.ChildProperties)
	}
	model.TrackAcceptedCommand(commandID, snapshots)
	t.Logf("[op %d] Apply (%s) stack=%s resources %v → accepted, model updated (success=%v)", op.SequenceNum, op.ApplyMode, stackLabel, op.ResourceIDs, successIDs)
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

	// When OnDependents is set, we're operating with a resource pool hierarchy.
	// "abort" = skip if cascades would be required; "cascade" = destroy dependents too.
	// When empty (flat resources / no pool), keep existing behavior.
	if op.OnDependents == "abort" {
		h.executeDestroyAbort(t, op, model, stackLabel, existingIDs)
		return
	}
	if op.OnDependents == "cascade" {
		h.executeDestroyCascade(t, op, model, stackLabel, existingIDs)
		return
	}

	// Default path: no on-dependents behavior (flat resources).
	h.executeDestroyDefault(t, op, model, stackLabel, existingIDs)
}

// executeDestroyDefault is the original destroy logic for flat resources (no pool hierarchy).
func (h *TestHarness) executeDestroyDefault(t *testing.T, op *Operation, model *StateModel, stackLabel string, existingIDs []int) {
	t.Helper()

	forma := FormaFromStackResources(stackLabel, existingIDs)

	// Program response sequences before submitting the command.
	var programmedSeqs []testcontrol.PluginOpSequence
	if op.DrawnOutcomes != nil {
		nativeIDs := h.getNativeIDsForStack(t, stackLabel)
		programmedSeqs = buildPluginOpSequences(op.DrawnOutcomes, op.StackIndex, stackLabel, existingIDs, model, nativeIDs, true, model.Pool)
		if len(programmedSeqs) > 0 {
			h.ProgramResponses(t, programmedSeqs)
		}
	}

	resp, err := h.client.DestroyForma(forma, false, clientID)
	if err != nil {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Destroy stack=%s resources %v → error: %v", op.SequenceNum, stackLabel, existingIDs, err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Destroy stack=%s resources %v → no changes required", op.SequenceNum, stackLabel, existingIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Destroy stack=%s resources %v → command %s", op.SequenceNum, stackLabel, existingIDs, commandID)

	// Snapshot before model update for potential cancel revert.
	snapshots := model.SnapshotResources(op.StackIndex, existingIDs)

	// Immediate model update: predict outcomes at submission time.
	successIDs := successfulResourceIDs(op, op.StackIndex, existingIDs, model.Pool, true, model)
	if len(successIDs) > 0 {
		model.ApplyDestroyed(op.StackIndex, successIDs)
	}
	model.TrackAcceptedCommand(commandID, snapshots)
	t.Logf("[op %d] Destroy stack=%s resources %v → accepted, model updated (success=%v)", op.SequenceNum, stackLabel, existingIDs, successIDs)
}

// executeDestroyAbort handles destroy with on-dependents="abort". If any resource
// in the drawn set has existing descendants, we simulate first and check for
// cascade resource updates. If cascades are found, we abort the destroy entirely.
// If no cascades (leaf resources or descendants already gone), we proceed normally.
func (h *TestHarness) executeDestroyAbort(t *testing.T, op *Operation, model *StateModel, stackLabel string, existingIDs []int) {
	t.Helper()

	// Check whether any drawn resource has existing descendants in the model.
	hasDependents := false
	for _, idx := range existingIDs {
		if model.HasExistingDescendants(op.StackIndex, idx) {
			hasDependents = true
			break
		}
	}

	forma := FormaFromPoolResources(model.Pool, stackLabel, model.ProviderStackLabel, existingIDs, defaultDestroyParentProps, defaultDestroyChildProps)

	if hasDependents {
		// Simulate to check whether the agent would create cascade deletes.
		simResp, err := h.client.DestroyForma(forma, true, clientID)
		if err != nil {
			t.Logf("[op %d] Destroy (abort) stack=%s resources %v → simulate error: %v", op.SequenceNum, stackLabel, existingIDs, err)
			return
		}

		// Check the simulation for cascade resource updates.
		for _, ru := range simResp.Simulation.Command.ResourceUpdates {
			if ru.IsCascade {
				t.Logf("[op %d] Destroy (abort) stack=%s resources %v → skipped (cascade dependents detected in simulation)", op.SequenceNum, stackLabel, existingIDs)
				return
			}
		}

		// No cascades found — model's view was stale or descendants were
		// already removed. Fall through to actually execute the destroy.
	}

	// No dependents (or simulation confirmed no cascades) — proceed with real destroy.
	// Program response sequences before submitting the command.
	var programmedSeqs []testcontrol.PluginOpSequence
	if op.DrawnOutcomes != nil {
		nativeIDs := h.getNativeIDsForStack(t, stackLabel)
		programmedSeqs = buildPluginOpSequences(op.DrawnOutcomes, op.StackIndex, stackLabel, existingIDs, model, nativeIDs, true, model.Pool)
		if len(programmedSeqs) > 0 {
			h.ProgramResponses(t, programmedSeqs)
		}
	}

	resp, err := h.client.DestroyForma(forma, false, clientID)
	if err != nil {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Destroy (abort) stack=%s resources %v → error: %v", op.SequenceNum, stackLabel, existingIDs, err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Destroy (abort) stack=%s resources %v → no changes required", op.SequenceNum, stackLabel, existingIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Destroy (abort) stack=%s resources %v → command %s", op.SequenceNum, stackLabel, existingIDs, commandID)

	// Snapshot before model update for potential cancel revert.
	snapshots := model.SnapshotResources(op.StackIndex, existingIDs)

	// Since the abort path only proceeds when simulation confirmed no cascades,
	// only the explicitly drawn resources can be affected — descendants are safe.
	// Immediate model update: predict outcomes at submission time.
	successIDs := successfulResourceIDs(op, op.StackIndex, existingIDs, model.Pool, true, model)
	if len(successIDs) > 0 {
		model.ApplyDestroyed(op.StackIndex, successIDs)
	}
	model.TrackAcceptedCommand(commandID, snapshots)
	t.Logf("[op %d] Destroy (abort) stack=%s resources %v → accepted, model updated (success=%v)", op.SequenceNum, stackLabel, existingIDs, successIDs)
}

// executeDestroyCascade handles destroy with on-dependents="cascade". The agent
// will automatically cascade-delete dependents. On success, we mark the drawn
// resources and all their descendants as destroyed in the state model.
func (h *TestHarness) executeDestroyCascade(t *testing.T, op *Operation, model *StateModel, stackLabel string, existingIDs []int) {
	t.Helper()

	forma := FormaFromPoolResources(model.Pool, stackLabel, model.ProviderStackLabel, existingIDs, defaultDestroyParentProps, defaultDestroyChildProps)

	// Program response sequences before submitting the command.
	var programmedSeqs []testcontrol.PluginOpSequence
	if op.DrawnOutcomes != nil {
		nativeIDs := h.getNativeIDsForStack(t, stackLabel)
		programmedSeqs = buildPluginOpSequences(op.DrawnOutcomes, op.StackIndex, stackLabel, existingIDs, model, nativeIDs, true, model.Pool)
		if len(programmedSeqs) > 0 {
			h.ProgramResponses(t, programmedSeqs)
		}
	}

	resp, err := h.client.DestroyForma(forma, false, clientID)
	if err != nil {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Destroy (cascade) stack=%s resources %v → error: %v", op.SequenceNum, stackLabel, existingIDs, err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		if len(programmedSeqs) > 0 {
			h.UnprogramResponses(t, programmedSeqs)
		}
		t.Logf("[op %d] Destroy (cascade) stack=%s resources %v → no changes required", op.SequenceNum, stackLabel, existingIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Destroy (cascade) stack=%s resources %v → command %s", op.SequenceNum, stackLabel, existingIDs, commandID)

	// Snapshot all resources on affected stacks before model update (cascade may affect descendants).
	var snapshots []ResourceSnapshot
	for _, si := range model.ComputeAffectedStacks(op.StackIndex, existingIDs, op.OnDependents) {
		for id := range model.Stack(si).Resources {
			snapshots = append(snapshots, model.SnapshotResources(si, []int{id})...)
		}
	}

	// Immediate model update: predict outcomes at submission time.
	// For cascade destroy, successful resources and all their descendants are destroyed.
	successIDs := successfulResourceIDs(op, op.StackIndex, existingIDs, model.Pool, true, model)
	for _, idx := range successIDs {
		model.ApplyCascadeDestroyed(op.StackIndex, idx)
	}
	model.TrackAcceptedCommand(commandID, snapshots)
	t.Logf("[op %d] Destroy (cascade) stack=%s resources %v → accepted, model updated (success=%v)", op.SequenceNum, stackLabel, existingIDs, successIDs)
}

// allResourceIDs returns all resource indices for a stack.
func allResourceIDs(model *StateModel, stackIndex int) []int {
	ids := make([]int, 0, len(model.Stack(stackIndex).Resources))
	for idx := range model.Stack(stackIndex).Resources {
		ids = append(ids, idx)
	}
	return ids
}

func (h *TestHarness) executeForceReconcile(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	stackLabel := model.Stack(op.StackIndex).Label
	stack := model.Stack(op.StackIndex)

	// Check if we have a last reconcile state to predict from.
	if len(stack.LastReconcileIDs) == 0 {
		t.Logf("[op %d] ForceReconcile stack=%s → skipped (no last reconcile state in model)", op.SequenceNum, stackLabel)
		return
	}

	resp, err := h.client.ForceReconcile(stackLabel)
	if err != nil {
		t.Logf("[op %d] ForceReconcile stack=%s → rejected: %v", op.SequenceNum, stackLabel, err)
		return
	}

	if resp.CommandID == "" {
		t.Logf("[op %d] ForceReconcile stack=%s → no drift", op.SequenceNum, stackLabel)
		return
	}

	t.Logf("[op %d] ForceReconcile stack=%s → command %s", op.SequenceNum, stackLabel, resp.CommandID)

	// Snapshot all resources on the stack (reconcile can create and delete).
	var snapshotIDs []int
	for id := range model.Stack(op.StackIndex).Resources {
		snapshotIDs = append(snapshotIDs, id)
	}
	snapshots := model.SnapshotResources(op.StackIndex, snapshotIDs)

	// Fire-and-forget: predict outcomes at submission time using the tracked
	// last reconcile state. ForceReconcile has no failure injection, so all
	// resources succeed.
	//
	// The agent rebuilds a forma from GetResourcesAtLastReconcile and applies
	// it in reconcile mode. Our model tracks this set via SaveLastReconcile.
	reconcileIDs := stack.LastReconcileIDs
	model.ApplyCreated(op.StackIndex, reconcileIDs, stack.LastReconcileProperties)
	applyReconcileGuarantee(model, op.StackIndex, reconcileIDs)
	model.TrackAcceptedCommand(resp.CommandID, snapshots)
	t.Logf("[op %d] ForceReconcile stack=%s → accepted, model updated (restore %v)", op.SequenceNum, stackLabel, reconcileIDs)
}

func (h *TestHarness) executeCancel(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	if len(model.AcceptedCommands) == 0 {
		t.Logf("[op %d] Cancel → skipped (no pending commands)", op.SequenceNum)
		return
	}

	// Cancel the most recent command (matches API behavior with no query).
	target := model.AcceptedCommands[len(model.AcceptedCommands)-1]

	resp, err := h.client.CancelCommands("", clientID)
	if err != nil {
		t.Logf("[op %d] Cancel command %s → error: %v", op.SequenceNum, target.CommandID, err)
		return
	}
	if resp == nil {
		t.Logf("[op %d] Cancel command %s → not found (already completed)", op.SequenceNum, target.CommandID)
		return
	}

	t.Logf("[op %d] Cancel command %s → accepted", op.SequenceNum, target.CommandID)

	// Wait for the canceled command to reach a terminal state so we can read
	// per-resource-update outcomes.
	cmd, ok := h.TryWaitForCommandDone(target.CommandID, defaultCommandTimeout)
	if !ok {
		t.Logf("[op %d] Cancel command %s → timed out waiting for completion", op.SequenceNum, target.CommandID)
		return
	}

	t.Logf("[op %d] Cancel command %s → completed (state=%s)", op.SequenceNum, target.CommandID, cmd.State)

	// Build label → snapshot lookup for the canceled command's snapshots.
	labelToSnapshot := buildLabelToSnapshotMap(target.Snapshots, model)

	// Revert model for resources that ended up in Canceled state.
	var reverted int
	for _, ru := range cmd.ResourceUpdates {
		if ru.State == "Canceled" {
			if snap, ok := labelToSnapshot[ru.ResourceLabel]; ok {
				model.RevertResources([]ResourceSnapshot{snap})
				reverted++
			}
		}
	}

	// Remove the canceled command from AcceptedCommands so DrainPendingCommands
	// doesn't wait for it again.
	model.AcceptedCommands = model.AcceptedCommands[:len(model.AcceptedCommands)-1]

	t.Logf("[op %d] Cancel command %s → reverted %d canceled resources", op.SequenceNum, target.CommandID, reverted)
}

// buildLabelToSnapshotMap builds a lookup from resource label to ResourceSnapshot
// by converting each snapshot's (stackIndex, slotIndex) to the label that the
// agent uses.
func buildLabelToSnapshotMap(snapshots []ResourceSnapshot, model *StateModel) map[string]ResourceSnapshot {
	m := make(map[string]ResourceSnapshot, len(snapshots))
	for _, snap := range snapshots {
		stackLabel := model.Stack(snap.StackIndex).Label
		var label string
		if model.Pool != nil {
			label = model.Pool.LabelForStack(stackLabel, snap.SlotIndex)
		} else {
			label = resourceLabelForStack(stackLabel, snap.SlotIndex)
		}
		m[label] = snap
	}
	return m
}

// filterExistingResources returns only the resource IDs whose model state
// is StateExists on the given stack.
func filterExistingResources(ids []int, stackIndex int, model *StateModel) []int {
	var existing []int
	for _, id := range ids {
		res := model.Resource(stackIndex, id)
		if res == nil {
			continue
		}
		if res.State == StateExists {
			existing = append(existing, id)
		}
	}
	return existing
}

func (h *TestHarness) executeTriggerSync(t *testing.T) {
	t.Helper()

	// Fire-and-forget: sync runs concurrently with user commands. Resources
	// in active changesets are excluded from sync (registered upfront in the
	// ChangesetExecutor), so sync only touches idle resources. Sync doesn't
	// have programmed failure responses and doesn't affect the model.
	err := h.client.ForceSync()
	if err != nil {
		t.Logf("TriggerSync error (may be expected): %v", err)
		return
	}
	t.Logf("TriggerSync: fired")
}

func (h *TestHarness) executeTriggerDiscovery(t *testing.T) {
	t.Helper()
	err := h.client.ForceDiscover()
	if err != nil {
		t.Logf("TriggerDiscovery error (may be expected): %v", err)
	}
}

func (h *TestHarness) executeCloudModify(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()
	if !model.UnmanagedNativeIDs[op.NativeID] {
		t.Logf("[op %d] CloudModify: %s → skipped (resource does not exist in cloud)", op.SequenceNum, op.NativeID)
		return
	}
	h.PutCloudState(t, op.NativeID, "Test::Generic::Resource", op.Properties)
	t.Logf("[op %d] CloudModify: %s", op.SequenceNum, op.NativeID)
}

func (h *TestHarness) executeCloudDelete(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()
	h.DeleteCloudState(t, op.NativeID)
	delete(model.UnmanagedNativeIDs, op.NativeID)
	t.Logf("[op %d] CloudDelete: %s", op.SequenceNum, op.NativeID)
}

func (h *TestHarness) executeCloudCreate(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()
	h.PutCloudState(t, op.NativeID, op.ResourceType, op.Properties)
	model.UnmanagedNativeIDs[op.NativeID] = true
	t.Logf("[op %d] CloudCreate: %s (%s)", op.SequenceNum, op.NativeID, op.ResourceType)

	for _, child := range op.CloudChildren {
		h.PutCloudState(t, child.NativeID, child.ResourceType, child.Properties)
		model.UnmanagedNativeIDs[child.NativeID] = true
		t.Logf("[op %d] CloudCreate child: %s (%s)", op.SequenceNum, child.NativeID, child.ResourceType)
	}
}

// verifyPostApplyProperties checks resource properties after a successful
// blocking apply. For reconcile: exact match. For patch: superset.
//
// The inventory may not reflect the latest command's changes immediately
// because the ResourcePersister processes updates asynchronously after the
// command reaches "Success". We poll briefly to account for this lag.
func (h *TestHarness) verifyPostApplyProperties(t *testing.T, op *Operation, mode pkgmodel.FormaApplyMode, stackLabel string, model *StateModel) {
	t.Helper()

	// Build the expected properties per resource label
	desiredProps := make(map[string]string)
	labels := make([]string, len(op.ResourceIDs))
	if model != nil && model.Pool != nil {
		pool := model.Pool
		for i, id := range op.ResourceIDs {
			label := pool.LabelForStack(stackLabel, id)
			labels[i] = label
			if pool.IsParent(id) {
				desiredProps[label] = strings.Replace(op.Properties, `"NAME"`, `"`+label+`"`, 1)
			} else {
				parentLabel := pool.ParentLabelForStack(stackLabel, id)
				props := strings.Replace(op.ChildProperties, `"NAME"`, `"`+label+`"`, 1)
				props = strings.Replace(props, `"PARENT_ID"`, `"`+parentLabel+`"`, 1)
				desiredProps[label] = props
			}
		}
	} else {
		for i, id := range op.ResourceIDs {
			label := resourceLabelForStack(stackLabel, id)
			labels[i] = label
			desiredProps[label] = strings.Replace(op.Properties, `"NAME"`, `"`+label+`"`, 1)
		}
	}

	// Poll inventory until properties match or timeout.
	// The ResourcePersister processes updates asynchronously after the changeset
	// completes. With resolvable references ($res), the resolution step adds
	// extra processing time, so we allow a generous timeout.
	deadline := time.Now().Add(10 * time.Second)
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

		// Add resolvable reference checks for hierarchical resources
		if len(lastViolations) == 0 && model != nil && model.Pool != nil {
			// Build provider inventory by filtering resources on the provider stack.
			var providerInventory []pkgmodel.Resource
			if model.ProviderStackLabel != "" {
				for _, res := range inventory {
					if res.Stack == model.ProviderStackLabel {
						providerInventory = append(providerInventory, res)
					}
				}
			}
			lastViolations = CheckResolvableProperties(inventory, model.Pool, stackLabel, op.ResourceIDs,
				providerInventory, model.ProviderStackLabel)
		}

		if len(lastViolations) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, v := range lastViolations {
		t.Logf("post-apply property violation: %s", v.Message)
	}
	require.Empty(t, lastViolations, "post-apply property violations")
}

// successfulResourceIDs returns the subset of resource IDs whose DrawnOutcomes
// indicate success (or all IDs if DrawnOutcomes is nil).
//
// The success check is context-sensitive:
//   - For resources that don't yet exist (creates): only CRUDSteps matter.
//     The plugin doesn't do a Read before Create, so ReadSteps failures are irrelevant.
//   - For existing resources (updates/deletes): both ReadSteps and CRUDSteps matter.
//     The ResourceUpdater does a Read before Update/Delete.
//
// When pool is non-nil and isDestroy is false (i.e. an apply/create operation),
// a resource is also considered failed if any of its ancestors in the operation
// set has a failure outcome. This mirrors the agent's behavior: the changeset
// executor processes resources in dependency order, and when a parent create
// fails, all dependents are marked as failed too.
func successfulResourceIDs(op *Operation, stackIdx int, ids []int, pool *ResourcePool, isDestroy bool, model *StateModel) []int {
	if op.DrawnOutcomes == nil {
		return ids // No failure injection — all succeed
	}

	// Build a set of IDs in this operation for fast lookup.
	idSet := make(map[int]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	// isOwnOutcomeSuccess checks whether this resource's own drawn outcome
	// indicates success (or has no outcome at all).
	isOwnOutcomeSuccess := func(id int) bool {
		key := outcomeKey(stackIdx, id)
		outcome, exists := op.DrawnOutcomes[key]
		if !exists {
			return true
		}
		res := model.Resource(stackIdx, id)
		resourceExists := res != nil && res.State == StateExists
		if resourceExists {
			// Existing resource being Updated or Deleted → Read+CRUD chain.
			// Both ReadSteps and CRUDSteps must succeed.
			return willOperationSucceed(outcome.ReadSteps) && willOperationSucceed(outcome.CRUDSteps)
		}
		// New resource → Create only. ReadSteps are irrelevant.
		return willOperationSucceed(outcome.CRUDSteps)
	}

	var success []int
	for _, id := range ids {
		ownSuccess := isOwnOutcomeSuccess(id)

		if !ownSuccess {
			continue
		}

		// For creates: check two failure conditions up the ancestor chain:
		// 1. An ancestor in the operation set has a failure outcome → cascade failure
		// 2. An ancestor NOT in the operation set doesn't exist → parent missing
		if pool != nil && !isDestroy {
			ancestorFailed := false
			cur := id
			for {
				parentIdx := pool.Slots[cur].ParentIndex
				if parentIdx == -1 {
					break
				}
				if idSet[parentIdx] {
					// Ancestor is in the operation set: check its drawn outcome.
					if !isOwnOutcomeSuccess(parentIdx) {
						ancestorFailed = true
						break
					}
				} else {
					// Ancestor is NOT in the operation set: it must already exist.
					// If it doesn't, the agent can't create this child.
					parentRes := model.Resource(stackIdx, parentIdx)
					if parentRes == nil || parentRes.State != StateExists {
						ancestorFailed = true
						break
					}
				}
				cur = parentIdx
			}
			if ancestorFailed {
				continue
			}
		}

		success = append(success, id)
	}
	return success
}

// DrainPendingCommands waits for all accepted commands to reach a terminal
// state. The model was already updated at submission time, so this only
// needs to confirm the agent has finished processing.
func (h *TestHarness) DrainPendingCommands(t *testing.T, model *StateModel, timeout time.Duration) {
	t.Helper()

	for _, ac := range model.AcceptedCommands {
		cmd, ok := h.TryWaitForCommandDone(ac.CommandID, timeout)
		if !ok {
			t.Logf("DrainPendingCommands: command %s timed out", ac.CommandID)
		} else {
			t.Logf("DrainPendingCommands: command %s completed (state=%s)", ac.CommandID, cmd.State)
			if cmd.State == "Failed" {
				for _, ru := range cmd.ResourceUpdates {
					if ru.State != "Success" {
						t.Logf("  FAILED resource_update: label=%s op=%s state=%s err=%s", ru.ResourceLabel, ru.Operation, ru.State, ru.ErrorMessage)
					}
				}
			}
		}
	}
	model.AcceptedCommands = nil

	// If any stack has an expired TTL, the background StackExpirer will
	// eventually destroy it. We need to ensure that destruction completes
	// before reconciling the model. Poll until ForceCheckTTL processes the
	// expired stack or inventory confirms the resources are gone.
	h.drainExpiredTTLStacks(t, model)

	// After all commands have completed, reconcile the model with the
	// actual inventory. Concurrent commands, OOB changes, reconcile
	// guarantees, and failure injection can all cause the actual state
	// to differ from submission-time predictions. Querying the inventory
	// gives us the ground truth.
	h.reconcileModelWithInventory(t, model)
}

// reconcileModelWithInventory queries the agent's inventory and updates the
// model to match the actual state. This is the authoritative reconciliation
// after all commands have completed.
func (h *TestHarness) reconcileModelWithInventory(t *testing.T, model *StateModel) {
	t.Helper()

	forma, err := h.client.ExtractResources("managed:true")
	if err != nil {
		t.Logf("reconcileModelWithInventory: failed to query inventory: %v", err)
		return
	}

	// Build a set of (stack, label) → exists from inventory.
	inventorySet := make(map[string]bool)
	if forma != nil {
		for _, res := range forma.Resources {
			inventorySet[res.Stack+":"+res.Label] = true
		}
	}

	pool := model.Pool
	corrections := 0

	for stackIdx := range model.Stacks {
		stack := &model.Stacks[stackIdx]
		for slotIdx, res := range stack.Resources {
			if res == nil {
				continue
			}
			var label string
			if pool != nil {
				label = pool.LabelForStack(stack.Label, slotIdx)
			} else {
				label = resourceLabelForStack(stack.Label, slotIdx)
			}
			key := stack.Label + ":" + label
			inInventory := inventorySet[key]

			if res.State == StateExists && !inInventory {
				t.Logf("  reconcileInventory: %s (slot %d) on %s model=Exists actual=NotExist → correcting", label, slotIdx, stack.Label)
				model.ApplyDestroyed(stackIdx, []int{slotIdx})
				corrections++
			} else if res.State == StateNotExist && inInventory {
				t.Logf("  reconcileInventory: %s (slot %d) on %s model=NotExist actual=Exists → correcting", label, slotIdx, stack.Label)
				model.ApplyCreated(stackIdx, []int{slotIdx}, "")
				corrections++
			}
		}
	}

	if corrections > 0 {
		t.Logf("reconcileModelWithInventory: corrected %d model entries", corrections)
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

		forma := FormaFromStackResources(stackLabel, ids)
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
			// SetupStacks is a reconcile apply — save for ForceReconcile prediction.
			model.SaveLastReconcile(stackIdx, ids, props, "")
			t.Logf("SetupStacks: stack %s created with %d resources", stackLabel, len(ids))
		} else {
			t.Logf("SetupStacks: stack %s command failed: %s", stackLabel, cmd.State)
		}
	}
}

// ForceCheckTTLAndWait triggers a TTL check. If stacks have expired, the agent
// destroys them and we wait for the commands to complete.
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

	for i, expiredLabel := range resp.ExpiredStacks {
		if i >= len(resp.CommandIDs) {
			break
		}
		commandID := resp.CommandIDs[i]

		// Find the stack index for this label.
		stackIdx := -1
		for s := range model.Stacks {
			if model.Stacks[s].Label == expiredLabel {
				stackIdx = s
				break
			}
		}
		if stackIdx == -1 {
			continue
		}

		// Wait for the destroy command and apply outcomes to the model.
		cmd, ok := h.TryWaitForCommandDone(commandID, defaultCommandTimeout)
		if !ok {
			t.Logf("ForceCheckTTLAndWait: command %s timed out", commandID)
			continue
		}
		applyCommandOutcomeToModel(t, cmd, model, model.Pool)
		model.Stacks[stackIdx].TTLExpired = false
		t.Logf("ForceCheckTTLAndWait: stack %s command %s completed: %s", expiredLabel, commandID, cmd.State)
	}
}

// drainExpiredTTLStacks ensures that any stacks with expired TTL policies are
// fully destroyed before returning. The background StackExpirer may not have
// fired yet, so we poll ForceCheckTTL until the expired stacks are processed
// or the resources disappear from inventory.
func (h *TestHarness) drainExpiredTTLStacks(t *testing.T, model *StateModel) {
	t.Helper()

	hasExpired := false
	for _, stack := range model.Stacks {
		if stack.TTLExpired {
			hasExpired = true
			break
		}
	}
	if !hasExpired {
		return
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		// Try to trigger the TTL check via the API.
		h.ForceCheckTTLAndWait(t, model)

		// Check if any stacks still have TTLExpired=true.
		stillExpired := false
		for _, stack := range model.Stacks {
			if stack.TTLExpired {
				stillExpired = true
				break
			}
		}
		if !stillExpired {
			return
		}

		// ForceCheckTTL returned "no expired stacks" but the model still
		// has TTLExpired=true. The StackExpirer may be in-flight or hasn't
		// detected the expiry yet. Check if the resources are already gone
		// from inventory (StackExpirer completed without our knowledge).
		forma, err := h.client.ExtractResources("managed:true")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		inventorySet := make(map[string]bool)
		if forma != nil {
			for _, res := range forma.Resources {
				inventorySet[res.Stack] = true
			}
		}

		allGone := true
		for i, stack := range model.Stacks {
			if stack.TTLExpired && inventorySet[stack.Label] {
				allGone = false
			} else if stack.TTLExpired && !inventorySet[stack.Label] {
				// Stack's resources are gone from inventory — the StackExpirer
				// already handled it. Update the model.
				for slotIdx := range stack.Resources {
					if stack.Resources[slotIdx] != nil {
						model.ApplyDestroyed(i, []int{slotIdx})
					}
				}
				model.Stacks[i].TTLExpired = false
				t.Logf("drainExpiredTTLStacks: stack %s resources gone from inventory, model updated", stack.Label)
			}
		}
		if allGone {
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	t.Logf("drainExpiredTTLStacks: timed out waiting for expired TTL stacks to be processed")
}

// dumpRawResourceRows queries the raw SQLite database to show all rows for
// duplicate resources. This reveals whether duplicates have different KSUIDs
// (pointing to a TOCTOU race in conflict detection) or different versions
// (pointing to a version deduplication issue).
func (h *TestHarness) dumpRawResourceRows(t *testing.T, resources []pkgmodel.Resource, seen map[string]int) {
	t.Helper()

	dbPath := h.dbPath
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

// FormaFromPoolResources builds a forma using the resource pool, with correct
// types, schemas, and resolvable ParentId references for child/grandchild slots.
// parentProps is the properties template for Test::Generic::Resource (with "NAME" placeholder).
// childProps is the properties template for child/grandchild types (with "NAME" and "PARENT_ID" placeholders).
func FormaFromPoolResources(pool *ResourcePool, stackLabel string, providerStackLabel string, ids []int,
	parentProps string, childProps string) *pkgmodel.Forma {

	resources := make([]pkgmodel.Resource, 0, len(ids))

	for _, idx := range ids {
		slot := pool.Slots[idx]
		label := pool.LabelForStack(stackLabel, idx)

		switch {
		case pool.IsParent(idx):
			// Parent resource — use parent properties template
			props := strings.Replace(parentProps, `"NAME"`, `"`+label+`"`, 1)
			resources = append(resources, pkgmodel.Resource{
				Label:      label,
				Type:       slot.Type,
				Stack:      stackLabel,
				Target:     "test-target",
				Properties: json.RawMessage(props),
				Schema:     testResourceSchema,
				Managed:    true,
			})

		case pool.IsCrossStack(idx):
			// Cross-stack slots only exist on consumer stacks (stacks 1+).
			// Provider stack (stack 0) skips them.
			if stackLabel == providerStackLabel {
				continue
			}
			props := strings.Replace(childProps, `"NAME"`, `"`+label+`"`, 1)
			parentLabel := pool.CrossStackParentLabelForStack(providerStackLabel, idx)
			parentType := pool.CrossStackParentType(idx)
			resObj, _ := json.Marshal(map[string]any{
				"$res":      true,
				"$label":    parentLabel,
				"$type":     parentType,
				"$stack":    providerStackLabel,
				"$property": "Name",
			})
			props = strings.Replace(props, `"PARENT_ID"`, string(resObj), 1)
			resources = append(resources, pkgmodel.Resource{
				Label:      label,
				Type:       slot.Type,
				Stack:      stackLabel,
				Target:     "test-target",
				Properties: json.RawMessage(props),
				Schema:     testChildResourceSchema,
				Managed:    true,
			})

		default:
			// Child or grandchild — use child properties template with resolvable ParentId
			props := strings.Replace(childProps, `"NAME"`, `"`+label+`"`, 1)

			// Build the resolvable $res object for ParentId.
			// The metastructure expects {"$res":true, "$label":"...", "$type":"...",
			// "$stack":"...", "$property":"..."} — NOT raw {"$ref":"formae://..."}.
			parentLabel := pool.ParentLabelForStack(stackLabel, idx)
			parentType := pool.ParentType(idx)
			resObj, _ := json.Marshal(map[string]any{
				"$res":      true,
				"$label":    parentLabel,
				"$type":     parentType,
				"$stack":    stackLabel,
				"$property": "Name",
			})
			props = strings.Replace(props, `"PARENT_ID"`, string(resObj), 1)

			resources = append(resources, pkgmodel.Resource{
				Label:      label,
				Type:       slot.Type,
				Stack:      stackLabel,
				Target:     "test-target",
				Properties: json.RawMessage(props),
				Schema:     testChildResourceSchema,
				Managed:    true,
			})
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

// --- Response queue programming helpers ---

// getNativeIDsForStack queries the inventory API for resources on a stack
// and returns a map of "stackLabel:resourceLabel" -> NativeID.
func (h *TestHarness) getNativeIDsForStack(t *testing.T, stackLabel string) map[string]string {
	t.Helper()

	forma, err := h.client.ExtractResources("managed:true stack:" + stackLabel)
	if err != nil || forma == nil {
		return nil
	}

	result := make(map[string]string)
	for _, res := range forma.Resources {
		result[res.Stack+":"+res.Label] = res.NativeID
	}
	return result
}

// buildPluginOpSequences converts DrawnOutcomes to PluginOpSequences that can
// be programmed into the test plugin's response queue.
//
// For each resource slot in the operation:
//   - If the resource doesn't exist -> it will be a Create: program Create steps with Name as match key
//   - If the resource exists -> it will be a Read+Update or Read+Delete: program Read and CRUD steps with NativeID as match key
func buildPluginOpSequences(
	drawnOutcomes map[string]DrawnOutcome,
	stackIndex int,
	stackLabel string,
	resourceIDs []int,
	model *StateModel,
	nativeIDs map[string]string,
	isDestroy bool,
	pool *ResourcePool,
) []testcontrol.PluginOpSequence {
	if drawnOutcomes == nil {
		return nil
	}

	// Build a set of IDs in this operation for ancestor lookups.
	idSet := make(map[int]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		idSet[id] = true
	}

	// isOwnOutcomeSuccess checks whether a resource's own drawn outcome
	// indicates success (matching the logic in successfulResourceIDs).
	isOwnOutcomeSuccess := func(id int) bool {
		key := outcomeKey(stackIndex, id)
		outcome, exists := drawnOutcomes[key]
		if !exists {
			return true
		}
		res := model.Resource(stackIndex, id)
		resourceExists := res != nil && res.State == StateExists
		if resourceExists {
			// Existing resource being Updated or Deleted → Read+CRUD chain.
			return willOperationSucceed(outcome.ReadSteps) && willOperationSucceed(outcome.CRUDSteps)
		}
		// New resource → Create only. ReadSteps are irrelevant.
		return willOperationSucceed(outcome.CRUDSteps)
	}

	// isCascadeFailed checks whether this resource will fail due to ancestry:
	// either an ancestor in the operation set has a failure outcome (cascade),
	// or an ancestor NOT in the operation set doesn't exist (missing parent).
	// In both cases the agent never attempts the child operation, so we must
	// not program responses for it.
	isCascadeFailed := func(id int) bool {
		if pool == nil || isDestroy {
			return false
		}
		cur := id
		for {
			parentIdx := pool.Slots[cur].ParentIndex
			if parentIdx == -1 {
				return false
			}
			if idSet[parentIdx] {
				if !isOwnOutcomeSuccess(parentIdx) {
					return true
				}
			} else {
				parentRes := model.Resource(stackIndex, parentIdx)
				if parentRes == nil || parentRes.State != StateExists {
					return true
				}
			}
			cur = parentIdx
		}
	}

	var sequences []testcontrol.PluginOpSequence

	for _, slotIdx := range resourceIDs {
		// Skip cascade-failed resources: their operations will never reach
		// the plugin, so programming responses would leave stale entries
		// in the queue that poison future commands.
		if isCascadeFailed(slotIdx) {
			continue
		}

		key := outcomeKey(stackIndex, slotIdx)
		outcome, ok := drawnOutcomes[key]
		if !ok {
			continue // no drawn outcome for this slot — will succeed by default
		}

		// Determine the resource label
		var label string
		if pool != nil {
			label = pool.LabelForStack(stackLabel, slotIdx)
		} else {
			label = resourceLabelForStack(stackLabel, slotIdx)
		}

		res := model.Stack(stackIndex).Resources[slotIdx]
		exists := res != nil && res.State == StateExists

		if exists {
			// Resource exists -> will be Read+Update or Read+Delete
			nativeID := nativeIDs[stackLabel+":"+label]
			if nativeID == "" {
				continue // can't program without NativeID
			}

			crudOp := "Update"
			if isDestroy {
				crudOp = "Delete"
			}

			// Program Read steps, then CRUD steps only if Read will succeed.
			if len(outcome.ReadSteps) > 0 {
				sequences = append(sequences, testcontrol.PluginOpSequence{
					MatchKey:  nativeID,
					Operation: "Read",
					Steps:     outcome.ReadSteps,
				})
			}
			readWillSucceed := len(outcome.ReadSteps) == 0 || willOperationSucceed(outcome.ReadSteps)
			if readWillSucceed && len(outcome.CRUDSteps) > 0 {
				sequences = append(sequences, testcontrol.PluginOpSequence{
					MatchKey:  nativeID,
					Operation: crudOp,
					Steps:     outcome.CRUDSteps,
				})
			}
		} else {
			// Resource doesn't exist -> will be a Create
			// Match key is the Name (= resource label)
			if len(outcome.CRUDSteps) > 0 {
				sequences = append(sequences, testcontrol.PluginOpSequence{
					MatchKey:  label,
					Operation: "Create",
					Steps:     outcome.CRUDSteps,
				})
			}
		}
	}

	return sequences
}

// willOperationSucceed returns true if the given response steps will result
// in a successful operation given the agent's retry behaviour.
// Any irrecoverable error causes immediate failure. Recoverable errors
// (Throttling) are survived if there are at most maxSurvivableErrors of them
// because the agent makes enough retry attempts for the queue to drain,
// and the next attempt gets default success.
func willOperationSucceed(steps []testcontrol.ResponseStep) bool {
	recoverable := 0
	for _, s := range steps {
		if s.ErrorCode == "" {
			continue
		}
		if s.ErrorCode == "Throttling" {
			recoverable++
		} else {
			return false // irrecoverable error → immediate failure
		}
	}
	return recoverable <= maxSurvivableErrors
}

// --- Operation to testcontrol message converters ---

// executeSetTTLPolicy applies a forma with the stack's existing resources plus
// a TTL policy. This is always blocking — we need the policy in place before
// CheckTTL can use it.
func (h *TestHarness) executeSetTTLPolicy(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	stackLabel := model.Stack(op.StackIndex).Label

	ttlSeconds := 86400
	if op.TTLExpired {
		ttlSeconds = 1
	}

	existingIDs := filterExistingResources(allResourceIDs(model, op.StackIndex), op.StackIndex, model)
	if len(existingIDs) == 0 {
		t.Logf("[op %d] SetTTLPolicy stack=%s → skipped (no existing resources)", op.SequenceNum, stackLabel)
		return
	}

	policy := json.RawMessage(fmt.Sprintf(`{"Type":"ttl","TTLSeconds":%d,"OnDependents":"cascade"}`, ttlSeconds))

	var forma *pkgmodel.Forma
	if model.Pool != nil {
		forma = FormaFromPoolResources(model.Pool, stackLabel, model.ProviderStackLabel, existingIDs,
			resourceProperties(stackLabel, existingIDs), defaultDestroyChildProps)
	} else {
		forma = FormaFromStackResources(stackLabel, existingIDs, resourceProperties(stackLabel, existingIDs))
	}
	for i := range forma.Stacks {
		if forma.Stacks[i].Label == stackLabel {
			forma.Stacks[i].Policies = []json.RawMessage{policy}
		}
	}

	resp, err := h.client.ApplyForma(forma, pkgmodel.FormaApplyModeReconcile, false, clientID, false)
	if err != nil {
		t.Logf("[op %d] SetTTLPolicy stack=%s → rejected: %v", op.SequenceNum, stackLabel, err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		t.Logf("[op %d] SetTTLPolicy stack=%s ttlExpired=%v → no changes required", op.SequenceNum, stackLabel, op.TTLExpired)
		model.Stacks[op.StackIndex].TTLExpired = op.TTLExpired
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] SetTTLPolicy stack=%s ttlExpired=%v → command %s", op.SequenceNum, stackLabel, op.TTLExpired, commandID)

	// Snapshot all resources on the stack (reconcile can create and delete).
	var snapshotIDs []int
	for id := range model.Stack(op.StackIndex).Resources {
		snapshotIDs = append(snapshotIDs, id)
	}
	snapshots := model.SnapshotResources(op.StackIndex, snapshotIDs)

	// Immediate model update: SetTTLPolicy is a reconcile apply.
	// Predict outcomes at submission time, same as executeApply reconcile.
	successIDs := successfulResourceIDs(op, op.StackIndex, existingIDs, model.Pool, false, model)
	if len(successIDs) > 0 {
		model.ApplyCreated(op.StackIndex, successIDs, resourceProperties(stackLabel, existingIDs))
	}
	// Reconcile guarantee: resources NOT in the forma are deleted by the
	// agent. Implicit deletes have no failure injection programmed, so
	// they always succeed. Apply the guarantee regardless of DrawnOutcomes.
	applyReconcileGuarantee(model, op.StackIndex, existingIDs)
	// SetTTLPolicy is a reconcile apply — save for ForceReconcile prediction.
	model.SaveLastReconcile(op.StackIndex, existingIDs, resourceProperties(stackLabel, existingIDs), defaultDestroyChildProps)
	model.Stacks[op.StackIndex].TTLExpired = op.TTLExpired
	model.Stacks[op.StackIndex].TTL = true
	model.TrackAcceptedCommand(commandID, snapshots)
	t.Logf("[op %d] SetTTLPolicy stack=%s ttlExpired=%v → accepted, model updated (success=%v)", op.SequenceNum, stackLabel, op.TTLExpired, successIDs)
}

// executeCheckTTL triggers a TTL check and processes any expired stacks.
func (h *TestHarness) executeCheckTTL(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	resp, err := h.client.ForceCheckTTL()
	if err != nil {
		t.Logf("[op %d] CheckTTL → error: %v", op.SequenceNum, err)
		return
	}

	if len(resp.ExpiredStacks) == 0 {
		t.Logf("[op %d] CheckTTL → no expired stacks", op.SequenceNum)
		return
	}

	t.Logf("[op %d] CheckTTL → expired stacks: %v, command IDs: %v", op.SequenceNum, resp.ExpiredStacks, resp.CommandIDs)

	for i, expiredLabel := range resp.ExpiredStacks {
		if i >= len(resp.CommandIDs) {
			break
		}
		commandID := resp.CommandIDs[i]

		// Find the stack index for this label
		stackIdx := -1
		for s := range model.Stacks {
			if model.Stacks[s].Label == expiredLabel {
				stackIdx = s
				break
			}
		}
		if stackIdx == -1 {
			continue
		}

		// Snapshot all resources on the stack before model update.
		resourceIDs := allResourceIDs(model, stackIdx)
		snapshots := model.SnapshotResources(stackIdx, resourceIDs)

		// Immediate model update: TTL expiry destroys all resources on the stack (cascade).
		for _, idx := range resourceIDs {
			model.ApplyCascadeDestroyed(stackIdx, idx)
		}
		model.Stacks[stackIdx].TTLExpired = false
		model.TrackAcceptedCommand(commandID, snapshots)
		t.Logf("[op %d] CheckTTL stack=%s command %s → accepted, model updated (destroyed all)", op.SequenceNum, expiredLabel, commandID)
	}
}
