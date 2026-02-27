// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

func TestStateModel_NewModel(t *testing.T) {
	model := NewStateModel(3)

	// All resources start as not existing
	for i := range 3 {
		res := model.Resource(i)
		require.NotNil(t, res)
		assert.Equal(t, []ResourceState{StateNotExist}, res.AcceptStates)
	}
}

func TestStateModel_ApplyCreated(t *testing.T) {
	model := NewStateModel(3)

	model.ApplyCreated([]int{0, 2}, `{"Name":"a","Value":"v1"}`)

	// Resources 0 and 2 should now be StateExists
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(0).AcceptStates)
	assert.Equal(t, `{"Name":"a","Value":"v1"}`, model.Resource(0).Properties)

	assert.Equal(t, []ResourceState{StateExists}, model.Resource(2).AcceptStates)

	// Resource 1 should remain StateNotExist
	assert.Equal(t, []ResourceState{StateNotExist}, model.Resource(1).AcceptStates)
}

func TestStateModel_ApplyDestroyed(t *testing.T) {
	model := NewStateModel(2)

	// Create resources first
	model.ApplyCreated([]int{0, 1}, `{"Name":"a"}`)

	// Destroy resource 0
	model.ApplyDestroyed([]int{0})

	assert.Equal(t, []ResourceState{StateNotExist}, model.Resource(0).AcceptStates)
	assert.Equal(t, []ResourceState{StateExists}, model.Resource(1).AcceptStates)
}

func TestStateModel_MarkUncertain(t *testing.T) {
	model := NewStateModel(2)

	// Create a resource
	model.ApplyCreated([]int{0}, `{"Name":"a"}`)

	// Mark it as uncertain (e.g., after failure injection)
	model.MarkUncertain(0)

	// Should accept both states
	states := model.Resource(0).AcceptStates
	assert.Contains(t, states, StateExists)
	assert.Contains(t, states, StateNotExist)
}

func TestStateModel_MarkUncertain_Idempotent(t *testing.T) {
	model := NewStateModel(1)
	model.ApplyCreated([]int{0}, `{"Name":"a"}`)

	model.MarkUncertain(0)
	model.MarkUncertain(0) // second call should not add duplicates

	assert.Len(t, model.Resource(0).AcceptStates, 2)
}

func TestStateModel_Verify_HappyPath(t *testing.T) {
	model := NewStateModel(2)
	model.ApplyCreated([]int{0, 1}, `{"Name":"a","Value":"v1"}`)

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
	model := NewStateModel(1)

	model.AddPendingCommand("cmd-1")
	assert.True(t, model.HasPendingCommands())

	model.RemovePendingCommand("cmd-1")
	assert.False(t, model.HasPendingCommands())
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
