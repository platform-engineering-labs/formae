// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import "fmt"

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

// StackState holds the per-stack state: resources and pending commands.
type StackState struct {
	Label           string
	Resources       map[int]*ExpectedResource
	PendingCommands map[string]*PendingCommand
	AutoReconcile   bool
	TTL             bool
}

// StateModel tracks what the system state should be after each operation.
// It supports multiple independent stacks, each with their own resource pool
// and pending command tracking.
type StateModel struct {
	Stacks             []StackState
	// ResourcesPerStack is the base count passed to NewStateModel; does not
	// include cross-stack slots appended by NewResourcePoolWithCrossStack.
	ResourcesPerStack  int
	Pool               *ResourcePool
	ProviderStackLabel string // label of stack 0; empty if stackCount < 2
}

// NewStateModel creates a state model with the given number of stacks,
// each containing resourcesPerStack resources. All resources start as
// not existing. Stacks are labeled "stack-0", "stack-1", etc.
//
// If resourcesPerStack is a multiple of SlotsPerTree, a ResourcePool is
// created to track parent-child relationships. Otherwise the pool is nil
// (backward compatible with flat resource tests).
func NewStateModel(stackCount, resourcesPerStack int) *StateModel {
	var pool *ResourcePool
	if resourcesPerStack%SlotsPerTree == 0 {
		if stackCount > 1 {
			pool = NewResourcePoolWithCrossStack(resourcesPerStack)
		} else {
			pool = NewResourcePool(resourcesPerStack)
		}
	}

	stacks := make([]StackState, stackCount)
	for s := range stacks {
		slotCount := resourcesPerStack
		if pool != nil {
			slotCount = len(pool.Slots)
		}
		resources := make(map[int]*ExpectedResource, slotCount)
		for i := range slotCount {
			if pool != nil && s == 0 && pool.IsCrossStack(i) {
				// Provider stack (stack 0) does not own cross-stack slots;
				// skip them so the map accurately reflects what can exist here.
				continue
			}
			resources[i] = &ExpectedResource{
				Index:        i,
				AcceptStates: []ResourceState{StateNotExist},
			}
		}
		stacks[s] = StackState{
			Label:           fmt.Sprintf("stack-%d", s),
			Resources:       resources,
			PendingCommands: make(map[string]*PendingCommand),
		}
	}

	var providerLabel string
	if stackCount > 1 {
		providerLabel = stacks[0].Label
	}
	return &StateModel{
		Stacks:             stacks,
		ResourcesPerStack:  resourcesPerStack,
		Pool:               pool,
		ProviderStackLabel: providerLabel,
	}
}

// Stack returns the StackState at the given index.
func (m *StateModel) Stack(stackIndex int) *StackState {
	return &m.Stacks[stackIndex]
}

// Resource returns the expected state for the resource at the given index
// on the given stack.
func (m *StateModel) Resource(stackIndex, idx int) *ExpectedResource {
	return m.Stacks[stackIndex].Resources[idx]
}

// ApplyCreated marks the given resources on the given stack as existing
// with the given properties.
func (m *StateModel) ApplyCreated(stackIndex int, resourceIDs []int, properties string) {
	stack := &m.Stacks[stackIndex]
	for _, id := range resourceIDs {
		if res, ok := stack.Resources[id]; ok {
			res.AcceptStates = []ResourceState{StateExists}
			res.Properties = properties
		}
	}
}

// ApplyDestroyed marks the given resources on the given stack as not existing.
func (m *StateModel) ApplyDestroyed(stackIndex int, resourceIDs []int) {
	stack := &m.Stacks[stackIndex]
	for _, id := range resourceIDs {
		if res, ok := stack.Resources[id]; ok {
			res.AcceptStates = []ResourceState{StateNotExist}
			res.Properties = ""
		}
	}
}

// ApplyCascadeDestroyed marks the given resource and all its descendants
// as not existing on the given stack. This models the "on-dependents: cascade"
// destroy behavior where deleting a parent also destroys all children and
// grandchildren. Requires a non-nil Pool on the model; panics otherwise.
func (m *StateModel) ApplyCascadeDestroyed(stackIndex int, rootIdx int) {
	if m.Pool == nil {
		panic("ApplyCascadeDestroyed requires a ResourcePool on the StateModel")
	}
	ids := append([]int{rootIdx}, m.Pool.AllDescendants(rootIdx)...)
	m.ApplyDestroyed(stackIndex, ids)
}

// HasExistingDescendants returns true if any descendant of the resource at idx
// currently has StateExists in its accept states on the given stack.
// Requires a non-nil Pool on the model; panics otherwise.
func (m *StateModel) HasExistingDescendants(stackIndex int, idx int) bool {
	if m.Pool == nil {
		panic("HasExistingDescendants requires a ResourcePool on the StateModel")
	}
	for _, descIdx := range m.Pool.AllDescendants(idx) {
		res := m.Resource(stackIndex, descIdx)
		if res == nil {
			continue
		}
		for _, s := range res.AcceptStates {
			if s == StateExists {
				return true
			}
		}
	}
	return false
}

// MarkUncertain widens the acceptable states for a resource on the given stack
// to include both StateExists and StateNotExist. Used after failure injection
// when the outcome is unknown.
func (m *StateModel) MarkUncertain(stackIndex, idx int) {
	res, ok := m.Stacks[stackIndex].Resources[idx]
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

// AddPendingCommand records a command as in-flight on the given stack.
func (m *StateModel) AddPendingCommand(stackIndex int, cmd *PendingCommand) {
	m.Stacks[stackIndex].PendingCommands[cmd.CommandID] = cmd
}

// RemovePendingCommand marks a command as no longer in-flight on the given stack.
func (m *StateModel) RemovePendingCommand(stackIndex int, commandID string) {
	delete(m.Stacks[stackIndex].PendingCommands, commandID)
}

// HasPendingCommands returns true if there are any in-flight commands on
// the given stack.
func (m *StateModel) HasPendingCommands(stackIndex int) bool {
	return len(m.Stacks[stackIndex].PendingCommands) > 0
}

// PendingCommandsForStack returns all pending commands for the given stack.
func (m *StateModel) PendingCommandsForStack(stackIndex int) map[string]*PendingCommand {
	return m.Stacks[stackIndex].PendingCommands
}
