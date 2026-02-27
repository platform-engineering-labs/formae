// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

const defaultCommandTimeout = 10 * time.Second

// ResetAgentState destroys all managed resources in the default stack, ensuring
// each rapid iteration starts from a clean slate.
func (h *TestHarness) ResetAgentState(t *testing.T) {
	t.Helper()

	// Check if there are any resources to destroy
	forma, err := h.client.ExtractResources("stack:default managed:true")
	if err != nil || forma == nil || len(forma.Resources) == 0 {
		t.Logf("ResetAgentState: no resources to clean up")
		return
	}

	// Destroy all existing resources via a forma destroy
	resp, err := h.client.DestroyForma(forma, false, clientID)
	if err != nil {
		t.Logf("ResetAgentState: destroy returned: %v", err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		t.Logf("ResetAgentState: no changes required")
		return
	}

	cmd, ok := h.TryWaitForCommandDone(resp.CommandID, defaultCommandTimeout)
	if ok && cmd != nil {
		t.Logf("ResetAgentState: cleanup command %s completed: %s (destroyed %d resources)",
			resp.CommandID, cmd.State, len(forma.Resources))
	} else {
		t.Logf("ResetAgentState: cleanup command %s timed out", resp.CommandID)
	}
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
		h.AssertAllInvariants(t)
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
// all correctness invariants.
func (h *TestHarness) AssertAllInvariants(t *testing.T) {
	t.Helper()

	// Query inventory via REST API
	forma, err := h.client.ExtractResources("stack:default")
	require.NoError(t, err, "ExtractResources should not error")

	var inventory []pkgmodel.Resource
	if forma != nil {
		inventory = forma.Resources
	}

	// Query cloud state via TestController
	cloudState := h.GetCloudStateSnapshot(t)

	// Check invariants
	violations := CheckInvariants(inventory, cloudState)
	for _, v := range violations {
		t.Errorf("invariant violation: %s", v.Message)
	}
	assert.Empty(t, violations, "no invariant violations expected")
}

func (h *TestHarness) executeApply(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	mode := pkgmodel.FormaApplyModePatch
	if op.ApplyMode == "reconcile" {
		mode = pkgmodel.FormaApplyModeReconcile
	}

	forma := FormaFromResourceIDs(op.ResourceIDs)
	resp, err := h.client.ApplyForma(forma, mode, false, clientID, false)
	if err != nil {
		// Patch may be rejected if resources don't exist, conflicts, etc.
		// This is valid agent behavior, not a test failure.
		t.Logf("[op %d] Apply (%s) resources %v → rejected: %v", op.SequenceNum, op.ApplyMode, op.ResourceIDs, err)
		return
	}

	// When the agent determines no changes are needed (e.g. reconcile where
	// desired state already matches), it returns a response with
	// ChangesRequired=false and does NOT persist a command to the datastore.
	// Polling for such a command would time out, so we treat it as a
	// successful no-op.
	if !resp.Simulation.ChangesRequired {
		t.Logf("[op %d] Apply (%s) resources %v → no changes required", op.SequenceNum, op.ApplyMode, op.ResourceIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Apply (%s) resources %v → command %s", op.SequenceNum, op.ApplyMode, op.ResourceIDs, commandID)

	if op.Blocking {
		cmd, ok := h.TryWaitForCommandDone(commandID, defaultCommandTimeout)
		if !ok {
			// Timed out — all resources become uncertain. This is expected
			// when failure injection is active (e.g. Create errors cause
			// retries). The final invariant check validates consistency.
			for _, id := range op.ResourceIDs {
				model.MarkUncertain(id)
			}
			t.Logf("[op %d] Apply (%s) command %s timed out (resources marked uncertain)", op.SequenceNum, op.ApplyMode, commandID)
			return
		}

		if cmd.State == "Success" {
			// Update model: applied resources now exist
			props := resourceProperties(op.ResourceIDs)
			model.ApplyCreated(op.ResourceIDs, props)

			// For reconcile mode, resources NOT in the forma are destroyed
			if mode == pkgmodel.FormaApplyModeReconcile {
				for idx := range model.resources {
					if !containsInt(op.ResourceIDs, idx) {
						model.ApplyDestroyed([]int{idx})
					}
				}
			}
		} else {
			// Command failed — resources may have been partially created.
			// Mark them as uncertain since we don't know which succeeded.
			for _, id := range op.ResourceIDs {
				model.MarkUncertain(id)
			}
		}
		t.Logf("[op %d] Apply completed: %s", op.SequenceNum, cmd.State)
	} else {
		model.AddPendingCommand(commandID)
	}
}

func (h *TestHarness) executeDestroy(t *testing.T, op *Operation, model *StateModel) {
	t.Helper()

	// Only attempt to destroy resources that exist according to the model.
	// Destroying nonexistent resources can cause the agent to hang.
	existingIDs := filterExistingResources(op.ResourceIDs, model)
	if len(existingIDs) == 0 {
		t.Logf("[op %d] Destroy resources %v → skipped (none exist)", op.SequenceNum, op.ResourceIDs)
		return
	}

	forma := FormaFromResourceIDs(existingIDs)
	resp, err := h.client.DestroyForma(forma, false, clientID)
	if err != nil {
		t.Logf("[op %d] Destroy resources %v → error: %v", op.SequenceNum, existingIDs, err)
		return
	}

	if !resp.Simulation.ChangesRequired {
		t.Logf("[op %d] Destroy resources %v → no changes required", op.SequenceNum, existingIDs)
		return
	}

	commandID := resp.CommandID
	t.Logf("[op %d] Destroy resources %v → command %s", op.SequenceNum, existingIDs, commandID)

	if op.Blocking {
		cmd, ok := h.TryWaitForCommandDone(commandID, defaultCommandTimeout)
		if !ok {
			// Timed out — all resources become uncertain
			for _, id := range existingIDs {
				model.MarkUncertain(id)
			}
			t.Logf("[op %d] Destroy command %s timed out (resources marked uncertain)", op.SequenceNum, commandID)
			return
		}

		if cmd.State == "Success" {
			model.ApplyDestroyed(existingIDs)
		} else {
			// Command failed — resources may have been partially destroyed.
			// Mark them as uncertain since we don't know which succeeded.
			for _, id := range existingIDs {
				model.MarkUncertain(id)
			}
		}
		t.Logf("[op %d] Destroy completed: %s", op.SequenceNum, cmd.State)
	} else {
		model.AddPendingCommand(commandID)
	}
}

// filterExistingResources returns only the resource IDs whose model state
// includes StateExists.
func filterExistingResources(ids []int, model *StateModel) []int {
	var existing []int
	for _, id := range ids {
		res := model.Resource(id)
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

// --- Forma builders ---

// FormaFromResourceIDs builds a forma containing the resources at the given
// pool indices. Resources use the same naming convention as SimpleForma.
func FormaFromResourceIDs(ids []int) *pkgmodel.Forma {
	resources := make([]pkgmodel.Resource, len(ids))
	for i, id := range ids {
		name := resourceLabel(id)
		resources[i] = pkgmodel.Resource{
			Label:      name,
			Type:       "Test::Generic::Resource",
			Stack:      "default",
			Target:     "test-target",
			Properties: json.RawMessage(`{"Name":"` + name + `","Value":"v1"}`),
			Managed:    true,
		}
	}

	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: "default"},
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

// resourceLabel returns the label for a resource at the given pool index.
func resourceLabel(idx int) string {
	return "res-" + string(rune('a'+idx))
}

// resourceProperties returns the default properties JSON for the given resource IDs.
func resourceProperties(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	// All resources in a single apply get the same properties format
	name := resourceLabel(ids[0])
	return fmt.Sprintf(`{"Name":"%s","Value":"v1"}`, name)
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
