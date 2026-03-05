// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

func TestStateModel_NewModel(t *testing.T) {
	model := NewStateModel(1, 3)

	// All resources start as not existing
	for i := range 3 {
		res := model.Resource(0, i)
		require.NotNil(t, res)
		assert.Equal(t, []ResourceState{StateNotExist}, res.AcceptStates)
	}
}

func TestStateModel_ApplyCreated(t *testing.T) {
	model := NewStateModel(1, 3)

	model.ApplyCreated(0, []int{0, 2}, `{"Name":"a","Value":"v1"}`)

	// Resources 0 and 2 should now be StateExists
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(0, 0).AcceptStates)
	assert.Equal(t, `{"Name":"a","Value":"v1"}`, model.Resource(0, 0).Properties)

	assert.Equal(t, []ResourceState{StateExists}, model.Resource(0, 2).AcceptStates)

	// Resource 1 should remain StateNotExist
	assert.Equal(t, []ResourceState{StateNotExist}, model.Resource(0, 1).AcceptStates)
}

func TestStateModel_ApplyDestroyed(t *testing.T) {
	model := NewStateModel(1, 2)

	// Create resources first
	model.ApplyCreated(0, []int{0, 1}, `{"Name":"a"}`)

	// Destroy resource 0
	model.ApplyDestroyed(0, []int{0})

	assert.Equal(t, []ResourceState{StateNotExist}, model.Resource(0, 0).AcceptStates)
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(0, 1).AcceptStates)
}

func TestStateModel_MarkUncertain(t *testing.T) {
	model := NewStateModel(1, 2)

	// Create a resource
	model.ApplyCreated(0, []int{0}, `{"Name":"a"}`)

	// Mark it as uncertain (e.g., after failure injection)
	model.MarkUncertain(0, 0)

	// Should accept both states
	states := model.Resource(0, 0).AcceptStates
	assert.Contains(t, states, StateExists)
	assert.Contains(t, states, StateNotExist)
}

func TestStateModel_MarkUncertain_Idempotent(t *testing.T) {
	model := NewStateModel(1, 1)
	model.ApplyCreated(0, []int{0}, `{"Name":"a"}`)

	model.MarkUncertain(0, 0)
	model.MarkUncertain(0, 0) // second call should not add duplicates

	assert.Len(t, model.Resource(0, 0).AcceptStates, 2)
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

	violations := CheckInvariants(inventory, cloudState)
	assert.Empty(t, violations)
}

func TestStateModel_Verify_PhantomResource(t *testing.T) {
	// Resource in inventory but not in cloud state
	inventory := []pkgmodel.Resource{
		{Label: "res-a", Type: "Test::Generic::Resource", NativeID: "native-0"},
	}
	cloudState := map[string]testcontrol.CloudStateEntry{}

	violations := CheckInvariants(inventory, cloudState)
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

	violations := CheckInvariants(inventory, cloudState)
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

	violations := CheckInvariants(inventory, cloudState)
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

	violations := CheckInvariants(inventory, cloudState)
	assert.Empty(t, violations)
}

func TestStateModel_PendingCommands(t *testing.T) {
	model := NewStateModel(1, 1)

	cmd := &PendingCommand{CommandID: "cmd-1", Kind: CommandKindApply, StackLabel: "stack-0", ResourceIDs: []int{0}}
	model.AddPendingCommand(0, cmd)
	assert.True(t, model.HasPendingCommands(0))

	model.RemovePendingCommand(0, "cmd-1")
	assert.False(t, model.HasPendingCommands(0))
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
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(0, 0).AcceptStates)
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(0, 1).AcceptStates)

	// Stack 1 resources should still be StateNotExist
	assert.Equal(t, []ResourceState{StateNotExist}, model.Resource(1, 0).AcceptStates)
	assert.Equal(t, []ResourceState{StateNotExist}, model.Resource(1, 1).AcceptStates)
}

func TestStateModel_MultiStack_PendingCommandsPerStack(t *testing.T) {
	model := NewStateModel(2, 2)

	cmd := &PendingCommand{CommandID: "cmd-1", Kind: CommandKindApply, StackLabel: "stack-0", ResourceIDs: []int{0}}
	model.AddPendingCommand(0, cmd)

	assert.True(t, model.HasPendingCommands(0))
	assert.False(t, model.HasPendingCommands(1))
}

func TestStateModel_MultiStack_MarkUncertainIsolated(t *testing.T) {
	model := NewStateModel(2, 2)

	// Create resource 0 on both stacks
	model.ApplyCreated(0, []int{0}, `{"Name":"a"}`)
	model.ApplyCreated(1, []int{0}, `{"Name":"b"}`)

	// Mark uncertain only on stack 0
	model.MarkUncertain(0, 0)

	// Stack 0 resource should be uncertain
	states0 := model.Resource(0, 0).AcceptStates
	assert.Contains(t, states0, StateExists)
	assert.Contains(t, states0, StateNotExist)

	// Stack 1 resource should still be deterministic
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(1, 0).AcceptStates)
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
