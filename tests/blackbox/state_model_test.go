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

	model.TrackAcceptedCommand("cmd-1")
	model.TrackAcceptedCommand("cmd-2")

	assert.Len(t, model.AcceptedCommands, 2)
	assert.Equal(t, "cmd-1", model.AcceptedCommands[0].CommandID)
	assert.Equal(t, "cmd-2", model.AcceptedCommands[1].CommandID)
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
