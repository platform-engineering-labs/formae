// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	Label      string
	Resources  map[int]*ExpectedResource
	TTL        bool
	TTLExpired bool

	// LastReconcileIDs and LastReconcileResourceProperties track the resource set
	// and exact expected properties from the most recent reconcile-mode apply on
	// this stack. Used by OpForceReconcile to predict outcomes at submission time.
	LastReconcileIDs                []int
	LastReconcileResourceProperties map[int]string
}

// StateModel tracks what the system state should be after each operation.
// It supports multiple independent stacks, each with their own resource pool.
type StateModel struct {
	Stacks []StackState
	// ResourcesPerStack is the base count passed to NewStateModel; does not
	// include cross-stack slots appended by NewResourcePoolWithCrossStack.
	ResourcesPerStack  int
	Pool               *ResourcePool
	ProviderStackLabel string // label of stack 0; empty if stackCount < 2
	// AcceptedCommands tracks commands accepted by the agent during the chaos phase.
	AcceptedCommands []AcceptedCommand
	// UnmanagedNativeIDs tracks cloud resources created out-of-band (via
	// OpCloudCreate). These are expected to exist in the cloud but NOT in
	// the agent's inventory. Used by the invariant checker to distinguish
	// expected orphans from real bugs.
	UnmanagedNativeIDs map[string]bool
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
		UnmanagedNativeIDs: make(map[string]bool),
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

// LabelForResource returns the expected label for the resource slot on the stack.
func (m *StateModel) LabelForResource(stackIndex, idx int) string {
	stackLabel := m.Stacks[stackIndex].Label
	if m.Pool != nil {
		return m.Pool.LabelForStack(stackLabel, idx)
	}
	return resourceLabelForStack(stackLabel, idx)
}

// TypeForResource returns the expected type for the resource slot.
func (m *StateModel) TypeForResource(idx int) string {
	if m.Pool != nil {
		return m.Pool.Slots[idx].Type
	}
	return "Test::Generic::Resource"
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

// ApplyCreatedResolved marks resources as existing using already-resolved
// per-resource properties.
func (m *StateModel) ApplyCreatedResolved(stackIndex int, propertiesByID map[int]string) {
	stack := &m.Stacks[stackIndex]
	for id, properties := range propertiesByID {
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

// TrackAcceptedCommand records a command that was accepted by the agent,
// along with pre-command resource snapshots for potential cancel revert.
func (m *StateModel) TrackAcceptedCommand(commandID string, snapshots []ResourceSnapshot) {
	m.AcceptedCommands = append(m.AcceptedCommands, AcceptedCommand{
		CommandID: commandID,
		Snapshots: snapshots,
	})
}

// SnapshotResources captures the current state of the given resources for later revert.
func (m *StateModel) SnapshotResources(stackIndex int, resourceIDs []int) []ResourceSnapshot {
	snapshots := make([]ResourceSnapshot, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		res := m.Resource(stackIndex, id)
		if res != nil {
			snapshots = append(snapshots, ResourceSnapshot{
				StackIndex: stackIndex,
				SlotIndex:  id,
				State:      res.State,
				Properties: res.Properties,
			})
		}
	}
	return snapshots
}

// RevertResources restores resources to their snapshotted state.
func (m *StateModel) RevertResources(snapshots []ResourceSnapshot) {
	for _, snap := range snapshots {
		res := m.Resource(snap.StackIndex, snap.SlotIndex)
		if res != nil {
			res.State = snap.State
			res.Properties = snap.Properties
		}
	}
}

// SaveLastReconcile records the resource set and exact expected properties from a
// reconcile-mode apply. This is used by OpForceReconcile to predict what the
// agent will restore.
func (m *StateModel) SaveLastReconcile(stackIndex int, resourceIDs []int, propertiesByID map[int]string) {
	stack := &m.Stacks[stackIndex]
	stack.LastReconcileIDs = make([]int, len(resourceIDs))
	copy(stack.LastReconcileIDs, resourceIDs)
	stack.LastReconcileResourceProperties = make(map[int]string, len(propertiesByID))
	for id, properties := range propertiesByID {
		stack.LastReconcileResourceProperties[id] = properties
	}
}

// CurrentProperties returns a copy of the model's current exact properties for
// the given resource IDs.
func (m *StateModel) CurrentProperties(stackIndex int, resourceIDs []int) map[int]string {
	propertiesByID := make(map[int]string, len(resourceIDs))
	for _, id := range resourceIDs {
		if res := m.Resource(stackIndex, id); res != nil && res.State == StateExists {
			propertiesByID[id] = res.Properties
		}
	}
	return propertiesByID
}

// ResolvePropertiesForResources expands per-operation property templates into
// exact expected properties for the given resource IDs.
func (m *StateModel) ResolvePropertiesForResources(stackIndex int, resourceIDs []int, properties, childProperties string) map[int]string {
	propertiesByID := make(map[int]string, len(resourceIDs))
	for _, id := range resourceIDs {
		propertiesByID[id] = m.ResolvePropertiesForResource(stackIndex, id, properties, childProperties)
	}
	return propertiesByID
}

// ResolvePropertiesForResource expands an operation's property templates into the
// exact expected properties for a single resource slot.
func (m *StateModel) ResolvePropertiesForResource(stackIndex, id int, properties, childProperties string) string {
	label := m.LabelForResource(stackIndex, id)
	if m.Pool == nil {
		return strings.Replace(properties, `"NAME"`, `"`+label+`"`, 1)
	}

	if m.Pool.IsParent(id) {
		return strings.Replace(properties, `"NAME"`, `"`+label+`"`, 1)
	}

	resolved := strings.Replace(childProperties, `"NAME"`, `"`+label+`"`, 1)
	parentLabel := m.parentIdentifierForResource(stackIndex, id)
	return strings.Replace(resolved, `"PARENT_ID"`, `"`+parentLabel+`"`, 1)
}

// NormalizePropertiesForResource rewrites a resource properties blob into the
// exact normalized form expected by the model for that slot.
func (m *StateModel) NormalizePropertiesForResource(stackIndex, id int, properties string) string {
	if properties == "" || m.Pool == nil || m.Pool.IsParent(id) {
		return properties
	}

	var props map[string]any
	if err := json.Unmarshal([]byte(properties), &props); err != nil {
		return properties
	}

	if parentID, ok := props["ParentId"]; ok {
		if wrapper, ok := parentID.(map[string]any); ok && isResolvableWrapperValue(wrapper) {
			if value, hasValue := wrapper["$value"]; hasValue {
				props["ParentId"] = value
			} else {
				props["ParentId"] = m.parentIdentifierForResource(stackIndex, id)
			}
		}
	} else {
		props["ParentId"] = m.parentIdentifierForResource(stackIndex, id)
	}
	bytes, err := json.Marshal(props)
	if err != nil {
		return properties
	}
	return string(bytes)
}

func isResolvableWrapperValue(m map[string]any) bool {
	if _, hasRef := m["$ref"]; hasRef {
		return true
	}
	if res, hasRes := m["$res"]; hasRes {
		if b, ok := res.(bool); ok && b {
			return true
		}
	}
	return false
}

func (m *StateModel) parentIdentifierForResource(stackIndex, id int) string {
	stackLabel := m.Stacks[stackIndex].Label
	if m.Pool.IsCrossStack(id) {
		parentID := m.Pool.Slots[id].CrossStackParentSlot
		return m.resourceIdentifierForSlot(0, parentID)
	}
	parentID := m.Pool.Slots[id].ParentIndex
	if parentID == -1 {
		return stackLabel
	}
	return m.resourceIdentifierForSlot(stackIndex, parentID)
}

func (m *StateModel) resourceIdentifierForSlot(stackIndex, id int) string {
	label := m.LabelForResource(stackIndex, id)
	res := m.Resource(stackIndex, id)
	if res == nil || res.Properties == "" {
		return label
	}

	var props map[string]any
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		return label
	}
	name, ok := props["Name"].(string)
	if !ok || name == "" {
		return label
	}
	return name
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
