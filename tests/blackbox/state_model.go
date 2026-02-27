// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

// ResourceState represents whether a resource is expected to exist.
type ResourceState int

const (
	StateNotExist ResourceState = iota
	StateExists
)

// ExpectedResource tracks the expected state of a single resource in the pool.
type ExpectedResource struct {
	Index        int
	Properties   string
	AcceptStates []ResourceState
}

// StateModel tracks what the system state should be after each operation.
// It supports set-of-acceptable-states for failure scenarios where a resource
// could be in multiple valid states.
type StateModel struct {
	resources       map[int]*ExpectedResource
	pendingCommands map[string]bool
}

// NewStateModel creates a state model for a pool of n resources.
// All resources start as not existing.
func NewStateModel(n int) *StateModel {
	resources := make(map[int]*ExpectedResource, n)
	for i := range n {
		resources[i] = &ExpectedResource{
			Index:        i,
			AcceptStates: []ResourceState{StateNotExist},
		}
	}
	return &StateModel{
		resources:       resources,
		pendingCommands: make(map[string]bool),
	}
}

// Resource returns the expected state for the resource at the given index.
func (m *StateModel) Resource(idx int) *ExpectedResource {
	return m.resources[idx]
}

// ApplyCreated marks the given resources as existing with the given properties.
func (m *StateModel) ApplyCreated(resourceIDs []int, properties string) {
	for _, id := range resourceIDs {
		if res, ok := m.resources[id]; ok {
			res.AcceptStates = []ResourceState{StateExists}
			res.Properties = properties
		}
	}
}

// ApplyDestroyed marks the given resources as not existing.
func (m *StateModel) ApplyDestroyed(resourceIDs []int) {
	for _, id := range resourceIDs {
		if res, ok := m.resources[id]; ok {
			res.AcceptStates = []ResourceState{StateNotExist}
			res.Properties = ""
		}
	}
}

// MarkUncertain widens the acceptable states for a resource to include both
// StateExists and StateNotExist. Used after failure injection when the outcome
// is unknown.
func (m *StateModel) MarkUncertain(idx int) {
	res, ok := m.resources[idx]
	if !ok {
		return
	}

	hasExists := false
	hasNotExist := false
	for _, s := range res.AcceptStates {
		if s == StateExists {
			hasExists = true
		}
		if s == StateNotExist {
			hasNotExist = true
		}
	}

	if !hasExists {
		res.AcceptStates = append(res.AcceptStates, StateExists)
	}
	if !hasNotExist {
		res.AcceptStates = append(res.AcceptStates, StateNotExist)
	}
}

// AddPendingCommand records a command as in-flight.
func (m *StateModel) AddPendingCommand(commandID string) {
	m.pendingCommands[commandID] = true
}

// RemovePendingCommand marks a command as no longer in-flight.
func (m *StateModel) RemovePendingCommand(commandID string) {
	delete(m.pendingCommands, commandID)
}

// HasPendingCommands returns true if there are any in-flight commands.
func (m *StateModel) HasPendingCommands() bool {
	return len(m.pendingCommands) > 0
}
