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

	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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

// ExpectedUnmanagedResource tracks the expected state of a discovered
// unmanaged resource across cloud state and inventory.
type ExpectedUnmanagedResource struct {
	NativeID            string
	ResourceType        string
	CloudProperties     string
	InventoryProperties string
	PresentInCloud      bool
	PresentInInventory  bool
}

// ExpectedManagedDrift tracks the cloud-side state of a managed resource after
// out-of-band changes and the inventory state observed after sync.
type ExpectedManagedDrift struct {
	StackLabel          string
	ResourceLabel       string
	ResourceType        string
	NativeID            string
	CloudProperties     string
	InventoryProperties string
	SnapshotProperties  string
	SnapshotState       ResourceState
	PresentInCloud      bool
	PresentInInventory  bool
	PendingSync         bool
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
	// UnmanagedResources tracks out-of-band cloud resources and whether they are
	// expected to have been ingested into the unmanaged inventory.
	UnmanagedResources map[string]*ExpectedUnmanagedResource
	// ManagedDriftedResources tracks managed resources that have been mutated or
	// deleted out-of-band so sync behavior can be asserted explicitly.
	ManagedDriftedResources map[string]*ExpectedManagedDrift
	AuthoritativeSlots      map[string]bool
	// NativeIDs maps "stackIdx:slotIdx" → cloud native ID (e.g. "test-42").
	// Populated from command response ResourceUpdate.NativeID on successful
	// creates/updates. Cleared on successful deletes.
	NativeIDs map[string]string
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
		Stacks:                  stacks,
		ResourcesPerStack:       resourcesPerStack,
		Pool:                    pool,
		ProviderStackLabel:      providerLabel,
		UnmanagedResources:      make(map[string]*ExpectedUnmanagedResource),
		ManagedDriftedResources: make(map[string]*ExpectedManagedDrift),
		AuthoritativeSlots:      make(map[string]bool),
		NativeIDs:               make(map[string]string),
	}
}

func slotKeyString(stackIdx, slotIdx int) string {
	return fmt.Sprintf("%d/%d", stackIdx, slotIdx)
}

func nativeIDKey(stackIdx, slotIdx int) string {
	return fmt.Sprintf("%d:%d", stackIdx, slotIdx)
}

func (m *StateModel) SetNativeID(stackIdx, slotIdx int, nativeID string) {
	if nativeID != "" {
		m.NativeIDs[nativeIDKey(stackIdx, slotIdx)] = nativeID
	}
}

func (m *StateModel) GetNativeID(stackIdx, slotIdx int) string {
	return m.NativeIDs[nativeIDKey(stackIdx, slotIdx)]
}

func (m *StateModel) ClearNativeID(stackIdx, slotIdx int) {
	delete(m.NativeIDs, nativeIDKey(stackIdx, slotIdx))
}

// FindExistingResourceWithNativeID finds a managed resource that exists in the
// model and has a tracked NativeID. Used for selecting OOB drift targets.
// The sequenceNum is used for deterministic selection (modulo eligible count).
func (m *StateModel) FindExistingResourceWithNativeID(sequenceNum int) (stackIdx, slotIdx int, stackLabel, resourceLabel, resourceType, nativeID string, ok bool) {
	type candidate struct {
		stackIdx, slotIdx         int
		stackLabel, label, rType  string
		nativeID                  string
	}
	var eligible []candidate
	for si, stack := range m.Stacks {
		for idx, res := range stack.Resources {
			if res == nil || res.State != StateExists {
				continue
			}
			nid := m.GetNativeID(si, idx)
			if nid == "" {
				continue
			}
			var label string
			if m.Pool != nil {
				label = m.Pool.LabelForStack(stack.Label, idx)
			} else {
				label = resourceLabelForStack(stack.Label, idx)
			}
			var rType string
			if m.Pool != nil {
				rType = m.Pool.Slots[idx].Type
			} else {
				rType = "Test::Generic::Resource"
			}
			eligible = append(eligible, candidate{si, idx, stack.Label, label, rType, nid})
		}
	}
	if len(eligible) == 0 {
		return 0, 0, "", "", "", "", false
	}
	c := eligible[sequenceNum%len(eligible)]
	return c.stackIdx, c.slotIdx, c.stackLabel, c.label, c.rType, c.nativeID, true
}

// NativeIDsByLabel returns a map of "stackLabel:resourceLabel" → NativeID for
// all tracked native IDs. This matches the format expected by
// buildPluginOpSequences and resolveReadMatchKey.
func (m *StateModel) NativeIDsByLabel() map[string]string {
	result := make(map[string]string, len(m.NativeIDs))
	for key, nativeID := range m.NativeIDs {
		var stackIdx, slotIdx int
		fmt.Sscanf(key, "%d:%d", &stackIdx, &slotIdx)
		stackLabel := m.Stacks[stackIdx].Label
		var label string
		if m.Pool != nil {
			label = m.Pool.LabelForStack(stackLabel, slotIdx)
		} else {
			label = resourceLabelForStack(stackLabel, slotIdx)
		}
		result[stackLabel+":"+label] = nativeID
	}
	return result
}

func (m *StateModel) MarkAuthoritativeSlot(stackIdx, slotIdx int) {
	m.AuthoritativeSlots[slotKeyString(stackIdx, slotIdx)] = true
}

func (m *StateModel) ClearAuthoritativeSlot(stackIdx, slotIdx int) {
	delete(m.AuthoritativeSlots, slotKeyString(stackIdx, slotIdx))
}

func (m *StateModel) IsAuthoritativeSlot(stackIdx, slotIdx int) bool {
	return m.AuthoritativeSlots[slotKeyString(stackIdx, slotIdx)]
}

func (m *StateModel) ClearAuthorityForResourceIDs(stackIdx int, ids []int) {
	for _, id := range ids {
		m.ClearAuthoritativeSlot(stackIdx, id)
	}
}

func (m *StateModel) ClearAuthorityForStack(stackIdx int) {
	for id := range m.Stack(stackIdx).Resources {
		m.ClearAuthoritativeSlot(stackIdx, id)
	}
}

func (m *StateModel) ApplyManagedCloudModify(stackLabel, resourceLabel, resourceType, nativeID, properties string) {
	if nativeID == "" {
		return
	}
	res := m.ManagedDriftedResources[nativeID]
	if res == nil {
		res = &ExpectedManagedDrift{NativeID: nativeID}
		if stackIdx, slotIdx, ok := m.findResourceSlot(stackLabel, resourceLabel); ok {
			if existing := m.Resource(stackIdx, slotIdx); existing != nil {
				res.SnapshotProperties = existing.Properties
				res.SnapshotState = existing.State
			}
		}
		m.ManagedDriftedResources[nativeID] = res
	}
	res.StackLabel = stackLabel
	res.ResourceLabel = resourceLabel
	res.ResourceType = resourceType
	res.CloudProperties = properties
	res.PresentInCloud = true
	res.PendingSync = true
}

func (m *StateModel) ApplyManagedCloudDelete(stackLabel, resourceLabel, resourceType, nativeID string) {
	if nativeID == "" {
		return
	}
	res := m.ManagedDriftedResources[nativeID]
	if res == nil {
		res = &ExpectedManagedDrift{NativeID: nativeID}
		if stackIdx, slotIdx, ok := m.findResourceSlot(stackLabel, resourceLabel); ok {
			if existing := m.Resource(stackIdx, slotIdx); existing != nil {
				res.SnapshotProperties = existing.Properties
				res.SnapshotState = existing.State
			}
		}
		m.ManagedDriftedResources[nativeID] = res
	}
	res.StackLabel = stackLabel
	res.ResourceLabel = resourceLabel
	res.ResourceType = resourceType
	res.CloudProperties = ""
	res.PresentInCloud = false
	res.PendingSync = true
}

func (m *StateModel) ApplySyncToManagedDrift() {
	for nativeID, expected := range m.ManagedDriftedResources {
		expected.PendingSync = false
		if expected.PresentInCloud {
			actual := pkgmodel.Resource{
				Stack:      expected.StackLabel,
				Label:      expected.ResourceLabel,
				Type:       expected.ResourceType,
				NativeID:   expected.NativeID,
				Managed:    true,
				Properties: []byte(expected.CloudProperties),
			}
			m.applyManagedDriftToResource(expected, &actual)
		} else {
			m.applyManagedDriftToResource(expected, nil)
		}
		delete(m.ManagedDriftedResources, nativeID)
	}
}

func (m *StateModel) ApplySyncCommand(cmd *apimodel.Command) {
	if cmd == nil {
		return
	}
	for _, ru := range cmd.ResourceUpdates {
		if ru.State != "Success" {
			continue
		}
		if driftNativeID, drift, ok := m.managedDriftForResource(ru.StackName, ru.ResourceLabel); ok {
			if ru.Operation == "delete" {
				drift.PresentInCloud = false
				m.applyManagedDriftToResource(drift, nil)
			} else if ru.Properties != nil {
				drift.PresentInCloud = true
				drift.CloudProperties = string(ru.Properties)
				actual := pkgmodel.Resource{Stack: ru.StackName, Label: ru.ResourceLabel, Type: ru.ResourceType, Managed: true, Properties: ru.Properties}
				m.applyManagedDriftToResource(drift, &actual)
			}
			drift.PendingSync = false
			delete(m.ManagedDriftedResources, driftNativeID)
		}
	}
}

func (m *StateModel) ApplyDiscoveryCommand(cmd *apimodel.Command) {
	m.ApplyDiscoveryToUnmanaged()
}

func (m *StateModel) managedDriftForResource(stackLabel, resourceLabel string) (string, *ExpectedManagedDrift, bool) {
	for nativeID, drift := range m.ManagedDriftedResources {
		if drift.StackLabel == stackLabel && drift.ResourceLabel == resourceLabel {
			return nativeID, drift, true
		}
	}
	return "", nil, false
}

func (m *StateModel) PendingManagedDriftNativeIDs() map[string]bool {
	ignore := make(map[string]bool)
	for nativeID, res := range m.ManagedDriftedResources {
		if res.PendingSync {
			ignore[nativeID] = true
		}
	}
	return ignore
}

func (m *StateModel) ManagedDriftNativeIDs() map[string]bool {
	ignore := make(map[string]bool)
	for nativeID := range m.ManagedDriftedResources {
		ignore[nativeID] = true
	}
	return ignore
}

func (m *StateModel) PendingManagedDriftCloudState() (map[string]string, map[string]bool) {
	propsByNativeID := make(map[string]string)
	deleted := make(map[string]bool)
	for nativeID, res := range m.ManagedDriftedResources {
		if !res.PendingSync {
			continue
		}
		if res.PresentInCloud {
			propsByNativeID[nativeID] = res.CloudProperties
		} else {
			deleted[nativeID] = true
		}
	}
	return propsByNativeID, deleted
}

func (m *StateModel) HasPendingManagedDriftForResource(stackLabel, resourceLabel string) bool {
	for _, res := range m.ManagedDriftedResources {
		if res.PendingSync && res.StackLabel == stackLabel && res.ResourceLabel == resourceLabel {
			return true
		}
	}
	return false
}

func (m *StateModel) HasPendingManagedDriftAffectingSlot(stackIdx, slotIdx int) bool {
	stack := m.Stack(stackIdx)
	if stack == nil {
		return false
	}
	label := m.LabelForResource(stackIdx, slotIdx)
	if m.HasPendingManagedDriftForResource(stack.Label, label) {
		return true
	}
	if m.Pool == nil {
		return false
	}
	current := slotIdx
	for current >= 0 {
		slot := m.Pool.Slots[current]
		if slot.ParentIndex >= 0 {
			parentLabel := m.LabelForResource(stackIdx, slot.ParentIndex)
			if m.HasPendingManagedDriftForResource(stack.Label, parentLabel) {
				return true
			}
		}
		current = slot.ParentIndex
	}
	if m.Pool.IsCrossStack(slotIdx) && m.ProviderStackLabel != "" {
		parentSlot := m.Pool.Slots[slotIdx].CrossStackParentSlot
		if parentSlot >= 0 && m.HasPendingManagedDriftForResource(m.ProviderStackLabel, m.LabelForResource(0, parentSlot)) {
			return true
		}
	}
	return false
}

func (m *StateModel) ClearManagedDriftForResource(stackLabel, resourceLabel string) {
	for nativeID, res := range m.ManagedDriftedResources {
		if res.StackLabel == stackLabel && res.ResourceLabel == resourceLabel {
			delete(m.ManagedDriftedResources, nativeID)
		}
	}
}

func (m *StateModel) applyManagedDriftToResource(expected *ExpectedManagedDrift, actual *pkgmodel.Resource) {
	stackIdx, slotIdx, ok := m.findResourceSlot(expected.StackLabel, expected.ResourceLabel)
	if !ok {
		return
	}

	if actual == nil {
		m.ApplyDestroyed(stackIdx, []int{slotIdx})
		m.MarkAuthoritativeSlot(stackIdx, slotIdx)
		m.refreshDependentParentReferences(stackIdx, slotIdx)
		return
	}
	props := m.NormalizePropertiesForResource(stackIdx, slotIdx, string(actual.Properties))
	m.ApplyCreated(stackIdx, []int{slotIdx}, props)
	m.MarkAuthoritativeSlot(stackIdx, slotIdx)
	m.refreshDependentParentReferences(stackIdx, slotIdx)
}

func (m *StateModel) findResourceSlot(stackLabel, resourceLabel string) (int, int, bool) {
	stackIdx := -1
	for i, stack := range m.Stacks {
		if stack.Label == stackLabel {
			stackIdx = i
			break
		}
	}
	if stackIdx == -1 {
		return -1, -1, false
	}
	for idx := range m.Stack(stackIdx).Resources {
		if m.LabelForResource(stackIdx, idx) == resourceLabel {
			return stackIdx, idx, true
		}
	}
	return -1, -1, false
}

func (m *StateModel) refreshDependentParentReferences(stackIdx, slotIdx int) {
	if m.Pool == nil {
		return
	}
	for dependent := range m.Stack(stackIdx).Resources {
		if dependent == slotIdx {
			continue
		}
		if m.Pool.Slots[dependent].ParentIndex != slotIdx {
			continue
		}
		res := m.Resource(stackIdx, dependent)
		if res == nil || res.State != StateExists || res.Properties == "" {
			continue
		}
		updated := m.NormalizePropertiesForResource(stackIdx, dependent, res.Properties)
		res.Properties = updated
		m.refreshDependentParentReferences(stackIdx, dependent)
	}

	if m.ProviderStackLabel == "" || m.Stack(stackIdx).Label != m.ProviderStackLabel {
		return
	}
	for consumerStackIdx := range m.Stacks {
		if consumerStackIdx == stackIdx {
			continue
		}
		for dependent := range m.Stack(consumerStackIdx).Resources {
			if !m.Pool.IsCrossStack(dependent) {
				continue
			}
			if m.Pool.Slots[dependent].CrossStackParentSlot != slotIdx {
				continue
			}
			res := m.Resource(consumerStackIdx, dependent)
			if res == nil || res.State != StateExists || res.Properties == "" {
				continue
			}
			res.Properties = m.NormalizePropertiesForResource(consumerStackIdx, dependent, res.Properties)
		}
	}
}

func (m *StateModel) EnsureUnmanagedResource(nativeID, resourceType, properties string) *ExpectedUnmanagedResource {
	if existing, ok := m.UnmanagedResources[nativeID]; ok {
		if resourceType != "" {
			existing.ResourceType = resourceType
		}
		if properties != "" {
			existing.CloudProperties = properties
		}
		return existing
	}
	res := &ExpectedUnmanagedResource{
		NativeID:        nativeID,
		ResourceType:    resourceType,
		CloudProperties: properties,
	}
	m.UnmanagedResources[nativeID] = res
	return res
}

func (m *StateModel) ApplyUnmanagedCloudCreate(nativeID, resourceType, properties string) {
	res := m.EnsureUnmanagedResource(nativeID, resourceType, properties)
	res.PresentInCloud = true
	res.CloudProperties = properties
	if res.ResourceType == "" {
		res.ResourceType = resourceType
	}
}

func (m *StateModel) ApplyUnmanagedCloudModify(nativeID, properties string) {
	res := m.EnsureUnmanagedResource(nativeID, "Test::Generic::Resource", properties)
	res.PresentInCloud = true
	res.CloudProperties = properties
}

func (m *StateModel) ApplyUnmanagedCloudDelete(nativeID string) {
	res := m.EnsureUnmanagedResource(nativeID, "", "")
	res.PresentInCloud = false
	res.CloudProperties = ""
}

func (m *StateModel) ApplyDiscoveryToUnmanaged() {
	for _, res := range m.UnmanagedResources {
		if !res.PresentInCloud {
			continue
		}
		res.PresentInInventory = true
		res.InventoryProperties = res.CloudProperties
	}
}

func (m *StateModel) ApplySyncToUnmanaged() {
	for _, res := range m.UnmanagedResources {
		if !res.PresentInInventory {
			continue
		}
		if res.PresentInCloud {
			res.InventoryProperties = res.CloudProperties
		} else {
			res.PresentInInventory = false
			res.InventoryProperties = ""
		}
	}
}

func (m *StateModel) UnmanagedPresentInCloudNativeIDs() map[string]bool {
	ignore := make(map[string]bool)
	for nativeID, res := range m.UnmanagedResources {
		if res.PresentInCloud {
			ignore[nativeID] = true
		}
	}
	return ignore
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
			// Creating a resource supersedes any prior authoritative destroy.
			m.ClearAuthoritativeSlot(stackIndex, id)
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
			// Creating a resource supersedes any prior authoritative destroy.
			m.ClearAuthoritativeSlot(stackIndex, id)
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
func (m *StateModel) TrackAcceptedCommand(commandID string, snapshots []ResourceSnapshot, requestedSlots []ResourceSlotRef, opLogSize int, isReconcile bool) {
	m.AcceptedCommands = append(m.AcceptedCommands, AcceptedCommand{
		CommandID:      commandID,
		Snapshots:      snapshots,
		RequestedSlots: requestedSlots,
		OpLogSize:      opLogSize,
		IsReconcile:    isReconcile,
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
	return normalizeResolvablePropertiesWithFallback(properties, m.parentIdentifierForResource(stackIndex, id))
}

func normalizeResolvableProperties(properties string) string {
	return normalizeResolvablePropertiesWithFallback(properties, "")
}

func normalizeResolvablePropertiesWithFallback(properties, fallbackParentID string) string {
	if properties == "" {
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
				props["ParentId"] = fallbackParentID
			}
		}
	} else if fallbackParentID != "" {
		props["ParentId"] = fallbackParentID
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

func jsonEqualStrings(a, b string) bool {
	if a == b {
		return true
	}
	var left any
	if err := json.Unmarshal([]byte(a), &left); err != nil {
		return false
	}
	var right any
	if err := json.Unmarshal([]byte(b), &right); err != nil {
		return false
	}
	leftBytes, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightBytes, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return string(leftBytes) == string(rightBytes)
}

func jsonContainsSubset(actualJSON, expectedJSON string) bool {
	if expectedJSON == "" {
		return actualJSON == ""
	}
	var actual any
	if err := json.Unmarshal([]byte(actualJSON), &actual); err != nil {
		return false
	}
	var expected any
	if err := json.Unmarshal([]byte(expectedJSON), &expected); err != nil {
		return false
	}
	return jsonValueContains(actual, expected)
}

func jsonValueContains(actual, expected any) bool {
	switch expectedTyped := expected.(type) {
	case map[string]any:
		actualTyped, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, expectedVal := range expectedTyped {
			actualVal, ok := actualTyped[key]
			if !ok || !jsonValueContains(actualVal, expectedVal) {
				return false
			}
		}
		return true
	case []any:
		actualTyped, ok := actual.([]any)
		if !ok || len(actualTyped) != len(expectedTyped) {
			return false
		}
		for i := range expectedTyped {
			if !jsonValueContains(actualTyped[i], expectedTyped[i]) {
				return false
			}
		}
		return true
	default:
		return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
	}
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
