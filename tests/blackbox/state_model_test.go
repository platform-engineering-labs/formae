// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// A failed create must leave the model slot NotExist even when the command's
// pre-command snapshot optimistically recorded it as Exists. In reverse-order
// draining a stale snapshot (still holding an optimistic Exists from an earlier
// prediction) would otherwise resurrect a resource whose creation actually
// failed, producing a model=Exists / inventory=NotExist mismatch under chaos.
func TestCorrectModelFromCommandOutcome_FailedCreateForcesNotExist(t *testing.T) {
	model := NewStateModel(1, 3)
	model.ApplyCreated(0, []int{1}, "") // optimistic prediction: slot 1 exists
	require.Equal(t, StateExists, model.Resource(0, 1).State)

	snapshots := []ResourceSnapshot{{StackIndex: 0, SlotIndex: 1, State: StateExists}}
	cmd := &apimodel.Command{
		CommandID: "cmd-failed-create",
		State:     "FinishedWithErrors",
		ResourceUpdates: []apimodel.ResourceUpdate{{
			StackName:     "stack-0",
			ResourceLabel: "res-stack-0-b", // slot 1
			Operation:     "create",
			State:         "Failed",
		}},
	}

	corrected := map[struct{ stackIdx, slotIdx int }]bool{}
	correctModelFromCommandOutcome(t, cmd, model, nil, snapshots, corrected, true)

	require.Equal(t, StateNotExist, model.Resource(0, 1).State,
		"a failed create must leave the slot NotExist, not revert to a stale Exists snapshot")
}

// A failed delete must still revert to the pre-command snapshot (the resource
// was never removed, so it still exists) — the failed-create fix must not
// change delete/update handling.
func TestCorrectModelFromCommandOutcome_FailedDeleteRevertsToSnapshot(t *testing.T) {
	model := NewStateModel(1, 3)
	model.ApplyDestroyed(0, []int{1}) // optimistic prediction: delete succeeded

	snapshots := []ResourceSnapshot{{StackIndex: 0, SlotIndex: 1, State: StateExists}}
	cmd := &apimodel.Command{
		CommandID: "cmd-failed-delete",
		State:     "FinishedWithErrors",
		ResourceUpdates: []apimodel.ResourceUpdate{{
			StackName:     "stack-0",
			ResourceLabel: "res-stack-0-b", // slot 1
			Operation:     "delete",
			State:         "Failed",
		}},
	}

	corrected := map[struct{ stackIdx, slotIdx int }]bool{}
	correctModelFromCommandOutcome(t, cmd, model, nil, snapshots, corrected, true)

	require.Equal(t, StateExists, model.Resource(0, 1).State,
		"a failed delete leaves the resource in place, reverting to its Exists snapshot")
}

// Faithful reproduction of the chaos flake: two failed-create commands touch the
// same slot during one reverse-order drain. The older command's snapshot still
// holds a stale optimistic Exists. Processing must leave the slot NotExist
// regardless of order — the older command must not resurrect it.
func TestCorrectModelFromCommandOutcome_ReverseOrderFailedCreatesStayNotExist(t *testing.T) {
	model := NewStateModel(1, 3)
	model.ApplyCreated(0, []int{1}, "") // optimistic prediction: slot 1 exists

	ru := apimodel.ResourceUpdate{
		StackName:     "stack-0",
		ResourceLabel: "res-stack-0-b", // slot 1
		Operation:     "create",
		State:         "Failed",
	}
	newerCmd := &apimodel.Command{CommandID: "newer", State: "FinishedWithErrors", ResourceUpdates: []apimodel.ResourceUpdate{ru}}
	olderCmd := &apimodel.Command{CommandID: "older", State: "FinishedWithErrors", ResourceUpdates: []apimodel.ResourceUpdate{ru}}

	// DrainPendingCommands processes most-recent first and shares `corrected`.
	corrected := map[struct{ stackIdx, slotIdx int }]bool{}
	newerSnap := []ResourceSnapshot{{StackIndex: 0, SlotIndex: 1, State: StateNotExist}}
	olderSnap := []ResourceSnapshot{{StackIndex: 0, SlotIndex: 1, State: StateExists}} // stale
	correctModelFromCommandOutcome(t, newerCmd, model, nil, newerSnap, corrected, true)
	correctModelFromCommandOutcome(t, olderCmd, model, nil, olderSnap, corrected, true)

	require.Equal(t, StateNotExist, model.Resource(0, 1).State,
		"an older failed-create command must not resurrect a slot from its stale Exists snapshot")
}

func TestStateModel_NewModel(t *testing.T) {
	model := NewStateModel(1, 3)

	// All resources start as not existing
	for i := range 3 {
		res := model.Resource(0, i)
		require.NotNil(t, res)
		assert.Equal(t, StateNotExist, res.State)
	}
}

func TestStateModel_ApplyCreated(t *testing.T) {
	model := NewStateModel(1, 3)

	model.ApplyCreated(0, []int{0, 2}, `{"Name":"a","Value":"v1"}`)

	// Resources 0 and 2 should now be StateExists
	assert.Equal(t, StateExists, model.Resource(0, 0).State)
	assert.Equal(t, `{"Name":"a","Value":"v1"}`, model.Resource(0, 0).Properties)

	assert.Equal(t, StateExists, model.Resource(0, 2).State)

	// Resource 1 should remain StateNotExist
	assert.Equal(t, StateNotExist, model.Resource(0, 1).State)
}

func TestStateModel_ApplyDestroyed(t *testing.T) {
	model := NewStateModel(1, 2)

	// Create resources first
	model.ApplyCreated(0, []int{0, 1}, `{"Name":"a"}`)

	// Destroy resource 0
	model.ApplyDestroyed(0, []int{0})

	assert.Equal(t, StateNotExist, model.Resource(0, 0).State)
	assert.Equal(t, StateExists, model.Resource(0, 1).State)
}

func TestStateModel_Verify_HappyPath(t *testing.T) {
	// Simulate actual inventory with 2 resources
	inventory := []pkgmodel.Resource{
		{Label: "res-a", Type: "Test::Generic::Resource", NativeID: "native-0"},
		{Label: "res-b", Type: "Test::Generic::Resource", NativeID: "native-1"},
	}
	cloudState := map[string]testcontrol.CloudStateEntry{
		"native-0": {NativeID: "native-0", ResourceType: "Test::Generic::Resource", Properties: `{"Name":"a","Value":"v1"}`},
		"native-1": {NativeID: "native-1", ResourceType: "Test::Generic::Resource", Properties: `{"Name":"a","Value":"v1"}`},
	}

	violations := CheckInvariants(inventory, cloudState, nil)
	assert.Empty(t, violations)
}

func TestStateModel_Verify_PhantomResource(t *testing.T) {
	// Resource in inventory but not in cloud state
	inventory := []pkgmodel.Resource{
		{Label: "res-a", Type: "Test::Generic::Resource", NativeID: "native-0"},
	}
	cloudState := map[string]testcontrol.CloudStateEntry{}

	violations := CheckInvariants(inventory, cloudState, nil)
	assert.NotEmpty(t, violations)

	hasPhantom := false
	for _, v := range violations {
		if v.Kind == ViolationPhantomResource {
			hasPhantom = true
		}
	}
	assert.True(t, hasPhantom, "should detect phantom resource")
}

func TestStateModel_Verify_OrphanedResource(t *testing.T) {
	// Resource in cloud state but not in inventory
	inventory := []pkgmodel.Resource{}
	cloudState := map[string]testcontrol.CloudStateEntry{
		"native-0": {NativeID: "native-0", ResourceType: "Test::Generic::Resource"},
	}

	violations := CheckInvariants(inventory, cloudState, nil)
	assert.NotEmpty(t, violations)

	hasOrphan := false
	for _, v := range violations {
		if v.Kind == ViolationOrphanedResource {
			hasOrphan = true
		}
	}
	assert.True(t, hasOrphan, "should detect orphaned resource")
}

func TestStateModel_Verify_PropertyMismatch(t *testing.T) {
	inventory := []pkgmodel.Resource{
		{Label: "res-a", Type: "Test::Generic::Resource", NativeID: "native-0",
			Properties: []byte(`{"Name":"a","Value":"v1"}`)},
	}
	cloudState := map[string]testcontrol.CloudStateEntry{
		"native-0": {NativeID: "native-0", ResourceType: "Test::Generic::Resource",
			Properties: `{"Name":"a","Value":"v2"}`},
	}

	violations := CheckInvariants(inventory, cloudState, nil)
	assert.NotEmpty(t, violations)

	hasMismatch := false
	for _, v := range violations {
		if v.Kind == ViolationPropertyMismatch {
			hasMismatch = true
		}
	}
	assert.True(t, hasMismatch, "should detect property mismatch")
}

func TestStateModel_Verify_PropertyMatch(t *testing.T) {
	inventory := []pkgmodel.Resource{
		{Label: "res-a", Type: "Test::Generic::Resource", NativeID: "native-0",
			Properties: []byte(`{"Name":"a","Value":"v1"}`)},
	}
	cloudState := map[string]testcontrol.CloudStateEntry{
		"native-0": {NativeID: "native-0", ResourceType: "Test::Generic::Resource",
			Properties: `{"Name":"a","Value":"v1"}`},
	}

	violations := CheckInvariants(inventory, cloudState, nil)
	assert.Empty(t, violations)
}

func TestStateModel_CommandsTerminal(t *testing.T) {
	commands := []CommandState{
		{ID: "cmd-1", State: "Success"},
		{ID: "cmd-2", State: "Failed"},
	}
	violations := CheckCommandCompleteness(commands)
	assert.Empty(t, violations)

	commands = append(commands, CommandState{ID: "cmd-3", State: "InProgress"})
	violations = CheckCommandCompleteness(commands)
	assert.NotEmpty(t, violations)

	hasIncomplete := false
	for _, v := range violations {
		if v.Kind == ViolationCommandNotTerminal {
			hasIncomplete = true
		}
	}
	assert.True(t, hasIncomplete, "should detect non-terminal command")
}

// --- Multi-stack tests ---

func TestStateModel_MultiStack_ResourcesIndependent(t *testing.T) {
	model := NewStateModel(2, 3)

	// Create resources on stack 0 only
	model.ApplyCreated(0, []int{0, 1}, `{"Name":"a"}`)

	// Stack 0 resources should be StateExists
	assert.Equal(t, StateExists, model.Resource(0, 0).State)
	assert.Equal(t, StateExists, model.Resource(0, 1).State)

	// Stack 1 resources should still be StateNotExist
	assert.Equal(t, StateNotExist, model.Resource(1, 0).State)
	assert.Equal(t, StateNotExist, model.Resource(1, 1).State)
}

func TestStateModel_MultiStack_StackLabels(t *testing.T) {
	model := NewStateModel(3, 1)

	assert.Equal(t, "stack-0", model.Stacks[0].Label)
	assert.Equal(t, "stack-1", model.Stacks[1].Label)
	assert.Equal(t, "stack-2", model.Stacks[2].Label)
}

func TestStateModel_MultiStack_StackCount(t *testing.T) {
	model := NewStateModel(3, 2)
	assert.Equal(t, 3, len(model.Stacks))
	assert.Equal(t, 2, model.ResourcesPerStack)
}

func TestStateModel_ComputeAffectedStacks(t *testing.T) {
	// 2 stacks, 5 resources per stack (1 tree), cross-stack enabled
	model := NewStateModel(2, 5)

	// Non-cascade destroy on stack 0 — only affects stack 0
	affected := model.ComputeAffectedStacks(0, []int{0}, "")
	assert.Equal(t, []int{0}, affected)

	// Cascade destroy of parent (slot 0) on stack 0 — slot 0 has cross-stack
	// dependents (slots 5, 6) that live on consumer stacks (stack 1)
	affected = model.ComputeAffectedStacks(0, []int{0}, "cascade")
	assert.Contains(t, affected, 0)
	assert.Contains(t, affected, 1)

	// Cascade destroy of a child (slot 1) on stack 0 — no cross-stack deps
	affected = model.ComputeAffectedStacks(0, []int{1}, "cascade")
	assert.Equal(t, []int{0}, affected)

	// Non-cascade destroy on stack 1 — only affects stack 1
	affected = model.ComputeAffectedStacks(1, []int{0}, "abort")
	assert.Equal(t, []int{1}, affected)
}

func TestStateModel_TrackAcceptedCommand(t *testing.T) {
	model := NewStateModel(1, 3)

	assert.Empty(t, model.AcceptedCommands)

	model.TrackAcceptedCommand("cmd-1", nil, nil, 3, false)
	model.TrackAcceptedCommand("cmd-2", nil, nil, 7, false)

	assert.Len(t, model.AcceptedCommands, 2)
	assert.Equal(t, "cmd-1", model.AcceptedCommands[0].CommandID)
	assert.Equal(t, "cmd-2", model.AcceptedCommands[1].CommandID)
	assert.Equal(t, 3, model.AcceptedCommands[0].OpLogSize)
	assert.Equal(t, 7, model.AcceptedCommands[1].OpLogSize)
}

func TestStateModel_CheckModelVsInventory_PropertiesAndType(t *testing.T) {
	model := NewStateModel(1, 1)
	model.ApplyCreated(0, []int{0}, `{"Name":"res-stack-0-a","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`)

	inventory := []pkgmodel.Resource{
		{
			Stack:      "stack-0",
			Label:      "res-stack-0-a",
			Type:       "Test::Generic::Resource",
			Properties: []byte(`{"Name":"res-stack-0-a","Value":"v2","SetTags":[],"EntityTags":[],"OrderedItems":[]}`),
		},
	}

	violations := CheckModelVsInventory(model, inventory)
	assert.NotEmpty(t, violations)

	hasPropertyMismatch := false
	for _, v := range violations {
		if v.Kind == ViolationModelInventoryMismatch {
			hasPropertyMismatch = true
		}
	}
	assert.True(t, hasPropertyMismatch, "should detect model/inventory property mismatch")

	inventory[0].Properties = []byte(`{"Name":"res-stack-0-a","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`)
	inventory[0].Type = "Test::Generic::WrongType"
	violations = CheckModelVsInventory(model, inventory)
	assert.NotEmpty(t, violations)
}

func TestStateModel_ResolvePropertiesForResources_WithPool(t *testing.T) {
	model := NewStateModel(2, 5)
	resolved := model.ResolvePropertiesForResources(
		1,
		[]int{0, 1, 5},
		`{"Name":"NAME","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`,
		`{"Name":"NAME","ParentId":"PARENT_ID","Value":"v2"}`,
	)

	assert.JSONEq(t, `{"Name":"res-stack-1-a","Value":"v1","SetTags":[],"EntityTags":[],"OrderedItems":[]}`,
		resolved[0])
	assert.JSONEq(t, `{"Name":"child-stack-1-a-0","ParentId":"res-stack-1-a","Value":"v2"}`,
		resolved[1])
	assert.JSONEq(t, `{"Name":"child-stack-1-xstack-0","ParentId":"res-stack-0-a","Value":"v2"}`,
		resolved[5])
}

func TestStateModel_UnmanagedLifecycle(t *testing.T) {
	model := NewStateModel(1, 1)
	model.ApplyUnmanagedCloudCreate("cloud-1", "Test::Generic::Resource", `{"Name":"u1","Value":"v1"}`)

	res := model.UnmanagedResources["cloud-1"]
	assert.True(t, res.PresentInCloud)
	assert.False(t, res.PresentInInventory)

	model.ApplyDiscoveryToUnmanaged()
	assert.True(t, res.PresentInInventory)
	assert.JSONEq(t, `{"Name":"u1","Value":"v1"}`, res.InventoryProperties)

	model.ApplyUnmanagedCloudModify("cloud-1", `{"Name":"u1","Value":"v2"}`)
	assert.JSONEq(t, `{"Name":"u1","Value":"v1"}`, res.InventoryProperties)

	model.ApplySyncToUnmanaged()
	assert.JSONEq(t, `{"Name":"u1","Value":"v2"}`, res.InventoryProperties)

	model.ApplyUnmanagedCloudDelete("cloud-1")
	model.ApplySyncToUnmanaged()
	assert.False(t, res.PresentInInventory)
	assert.False(t, res.PresentInCloud)
}

// An out-of-band modify of a managed resource is absorbed by sync: inventory
// converges on the cloud properties. The model computes that end state
// directly at the drift operation.
func TestStateModel_ManagedCloudModifyComputesAbsorbedState(t *testing.T) {
	model := NewStateModel(1, 1)
	model.ApplyCreated(0, []int{0}, `{"Name":"res-stack-0-a","Value":"v1"}`)
	model.SetNativeID(0, 0, "test-1")

	model.ApplyManagedCloudModify(0, 0, `{"Name":"cloud-res-7","Value":"drift"}`)

	res := model.Resource(0, 0)
	assert.Equal(t, StateExists, res.State)
	assert.JSONEq(t, `{"Name":"cloud-res-7","Value":"drift"}`, res.Properties)
	assert.Equal(t, "test-1", model.GetNativeID(0, 0), "modify keeps the native id")
}

// An out-of-band delete of a managed resource is absorbed by sync: the agent
// drops the resource from inventory. The model computes that end state
// directly at the drift operation.
func TestStateModel_ManagedCloudDeleteComputesAbsorbedState(t *testing.T) {
	model := NewStateModel(1, 1)
	model.ApplyCreated(0, []int{0}, `{"Name":"res-stack-0-a","Value":"v1"}`)
	model.SetNativeID(0, 0, "test-1")

	model.ApplyManagedCloudDelete(0, 0)

	res := model.Resource(0, 0)
	assert.Equal(t, StateNotExist, res.State)
	assert.Empty(t, res.Properties)
	assert.Empty(t, model.GetNativeID(0, 0), "delete clears the native id")
}

// Drift targets must not be touched by in-flight commands: a command on a
// stack can create, update, or (via the reconcile guarantee) delete any slot
// on that stack, so drift on such a slot would race the command.
func TestStateModel_DriftEligibilitySkipsStacksWithInFlightCommands(t *testing.T) {
	model := NewStateModel(2, 3)
	model.ApplyCreated(0, []int{0}, `{"Name":"res-stack-0-a","Value":"v1"}`)
	model.SetNativeID(0, 0, "test-1")
	model.ApplyCreated(1, []int{0}, `{"Name":"res-stack-1-a","Value":"v1"}`)
	model.SetNativeID(1, 0, "test-2")

	model.TrackAcceptedCommand("cmd-1", nil, []ResourceSlotRef{{StackIndex: 1, SlotIndex: 0}}, 0, false)

	for seq := range 4 {
		stackIdx, _, _, _, _, nativeID, ok := model.FindDriftEligibleResource(seq)
		require.True(t, ok)
		assert.Equal(t, 0, stackIdx, "stack 1 has an in-flight command and must be excluded")
		assert.Equal(t, "test-1", nativeID)
	}

	model.AcceptedCommands = nil
	seen := make(map[int]bool)
	for seq := range 4 {
		stackIdx, _, _, _, _, _, ok := model.FindDriftEligibleResource(seq)
		require.True(t, ok)
		seen[stackIdx] = true
	}
	assert.True(t, seen[0] && seen[1], "with no in-flight commands both stacks are eligible")
}

// Cross-stack slots reference a parent on the provider stack (stack 0), so an
// in-flight command on the provider stack can cascade onto them. They are only
// eligible for drift while the provider stack is quiescent too.
func TestStateModel_DriftEligibilityCrossStackNeedsQuiescentProvider(t *testing.T) {
	model := NewStateModel(2, 10)
	require.NotNil(t, model.Pool)

	crossIdx := -1
	for i := range model.Pool.Slots {
		if model.Pool.IsCrossStack(i) {
			crossIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, crossIdx, 0)

	model.ApplyCreated(1, []int{crossIdx}, `{"Name":"x","ParentId":"p","Value":"v1"}`)
	model.SetNativeID(1, crossIdx, "test-9")

	// A command on the provider stack (slot 0 there) excludes cross-stack slots.
	model.TrackAcceptedCommand("cmd-1", nil, []ResourceSlotRef{{StackIndex: 0, SlotIndex: 0}}, 0, false)
	_, _, _, _, _, _, ok := model.FindDriftEligibleResource(0)
	assert.False(t, ok, "the only existing resource is cross-stack and the provider stack is busy")

	model.AcceptedCommands = nil
	stackIdx, slotIdx, _, _, _, nativeID, ok := model.FindDriftEligibleResource(0)
	require.True(t, ok)
	assert.Equal(t, 1, stackIdx)
	assert.Equal(t, crossIdx, slotIdx)
	assert.Equal(t, "test-9", nativeID)
}

// A canceled changeset can leave its resources registered as in-progress with
// the synchronizer for an unobservable time, during which sync skips them and
// drift on them cannot absorb. Stacks touched by a canceled command are
// excluded from drift targeting for the rest of the iteration.
func TestStateModel_DriftEligibilitySkipsCancelPoisonedStacks(t *testing.T) {
	model := NewStateModel(2, 3)
	model.ApplyCreated(0, []int{0}, `{"Name":"res-stack-0-a","Value":"v1"}`)
	model.SetNativeID(0, 0, "test-1")
	model.ApplyCreated(1, []int{0}, `{"Name":"res-stack-1-a","Value":"v1"}`)
	model.SetNativeID(1, 0, "test-2")

	model.MarkDriftExcludedStack(1)

	for seq := range 4 {
		stackIdx, _, _, _, _, _, ok := model.FindDriftEligibleResource(seq)
		require.True(t, ok)
		assert.Equal(t, 0, stackIdx, "stack 1 was touched by a canceled command and must be excluded")
	}
}

// Eligible drift targets must be enumerated in a stable order: selection is
// keyed off the generated sequence number, and map-iteration order would make
// the picked resource nondeterministic across runs.
func TestStateModel_DriftEligibilityOrderIsDeterministic(t *testing.T) {
	model := NewStateModel(2, 3)
	for s := range 2 {
		for i := range 3 {
			model.ApplyCreated(s, []int{i}, `{"Name":"n","Value":"v1"}`)
			model.SetNativeID(s, i, fmt.Sprintf("test-%d%d", s, i))
		}
	}

	for seq := range 6 {
		_, _, _, _, _, first, ok := model.FindDriftEligibleResource(seq)
		require.True(t, ok)
		for range 20 {
			_, _, _, _, _, again, ok := model.FindDriftEligibleResource(seq)
			require.True(t, ok)
			require.Equal(t, first, again)
		}
	}
}

func TestStateModel_Stack(t *testing.T) {
	model := NewStateModel(2, 3)

	stack := model.Stack(0)
	require.NotNil(t, stack)
	assert.Equal(t, "stack-0", stack.Label)
	assert.Len(t, stack.Resources, 3)

	stack1 := model.Stack(1)
	require.NotNil(t, stack1)
	assert.Equal(t, "stack-1", stack1.Label)
}
