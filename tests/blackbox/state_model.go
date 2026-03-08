// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"fmt"
	"sort"
)

// ResourceState represents whether a resource is expected to exist.
type ResourceState int

const (
	StateNotExist ResourceState = iota
	StateExists
)

// ExpectedResource tracks the expected state of a single resource in the pool.
type ExpectedResource struct {
	Index      int
	Properties string
	State      ResourceState
}

// StackState holds the per-stack state: resources.
type StackState struct {
	Label         string
	Resources     map[int]*ExpectedResource
	AutoReconcile bool
	TTL           bool
	TTLExpired    bool
}

// StateModel tracks what the system state should be after each operation.
// It supports multiple independent stacks, each with their own resource pool.
type StateModel struct {
	Stacks             []StackState
	// ResourcesPerStack is the base count passed to NewStateModel; does not
	// include cross-stack slots appended by NewResourcePoolWithCrossStack.
	ResourcesPerStack  int
	Pool               *ResourcePool
	ProviderStackLabel string // label of stack 0; empty if stackCount < 2
	// AcceptedCommands tracks commands accepted by the agent during the chaos phase.
	AcceptedCommands   []AcceptedCommand
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
				Index: i,
				State: StateNotExist,
			}
		}
		stacks[s] = StackState{
			Label:     fmt.Sprintf("stack-%d", s),
			Resources: resources,
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
			res.State = StateExists
			res.Properties = properties
		}
	}
}

// ApplyDestroyed marks the given resources on the given stack as not existing.
func (m *StateModel) ApplyDestroyed(stackIndex int, resourceIDs []int) {
	stack := &m.Stacks[stackIndex]
	for _, id := range resourceIDs {
		if res, ok := stack.Resources[id]; ok {
			res.State = StateNotExist
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

	// Also destroy cross-stack dependents on other stacks, but ONLY when
	// the cascade originates on the provider stack (stack 0). Cross-stack
	// children reference the provider stack's parent slot, so destroying
	// the same slot index on a consumer stack does not affect them.
	if m.ProviderStackLabel != "" && stackIndex == 0 {
		for _, destroyedIdx := range ids {
			crossStackDeps := m.Pool.CrossStackDependents(destroyedIdx)
			if len(crossStackDeps) == 0 {
				continue
			}
			for s := range m.Stacks {
				if s == stackIndex {
					continue
				}
				m.ApplyDestroyed(s, crossStackDeps)
			}
		}
	}
}

// HasExistingDescendants returns true if any descendant of the resource at idx
// currently has StateExists on the given stack.
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
		if res.State == StateExists {
			return true
		}
	}
	return false
}

// TrackAcceptedCommand records a command that was accepted by the agent.
func (m *StateModel) TrackAcceptedCommand(commandID string) {
	m.AcceptedCommands = append(m.AcceptedCommands, AcceptedCommand{CommandID: commandID})
}

// ComputeAffectedStacks returns all stack indices that a command on the given
// stack would affect. For cascade destroys, this includes stacks with
// cross-stack dependents of any destroyed resource or its descendants.
func (m *StateModel) ComputeAffectedStacks(stackIndex int, resourceIDs []int, onDependents string) []int {
	affected := map[int]bool{stackIndex: true}

	if onDependents != "cascade" || m.Pool == nil {
		return []int{stackIndex}
	}

	// Cross-stack cascade only applies when the command targets the provider
	// stack (stack 0). Consumer stacks share the same slot indices but their
	// resources don't parent cross-stack children.
	if m.ProviderStackLabel != "" && stackIndex == 0 {
		for _, idx := range resourceIDs {
			allIDs := append([]int{idx}, m.Pool.AllDescendants(idx)...)
			for _, id := range allIDs {
				for _, xIdx := range m.Pool.CrossStackDependents(id) {
					for s := range m.Stacks {
						if s == stackIndex {
							continue
						}
						if _, ok := m.Stacks[s].Resources[xIdx]; ok {
							affected[s] = true
						}
					}
				}
			}
		}
	}

	result := make([]int, 0, len(affected))
	for s := range affected {
		result = append(result, s)
	}
	sort.Ints(result)
	return result
}

