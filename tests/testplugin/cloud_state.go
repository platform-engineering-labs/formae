// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"maps"
	"sync"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// CloudState is a thread-safe in-memory store representing what "exists in the cloud."
// All CRUD operations in the test plugin read from and write to this store.
// The test harness manipulates it directly to simulate out-of-band changes.
type CloudState struct {
	mu      sync.RWMutex
	entries map[string]testcontrol.CloudStateEntry
}

// NewCloudState creates a new, empty CloudState.
func NewCloudState() *CloudState {
	return &CloudState{
		entries: make(map[string]testcontrol.CloudStateEntry),
	}
}

// Put creates or updates an entry in the cloud state.
func (cs *CloudState) Put(nativeID, resourceType, properties string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.entries[nativeID] = testcontrol.CloudStateEntry{
		NativeID:     nativeID,
		ResourceType: resourceType,
		Properties:   properties,
	}
}

// Get retrieves an entry by its NativeID. Returns the entry and true if found,
// or a zero-value entry and false if not.
func (cs *CloudState) Get(nativeID string) (testcontrol.CloudStateEntry, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	entry, ok := cs.entries[nativeID]
	return entry, ok
}

// Delete removes an entry by its NativeID. It is a no-op if the entry does not exist.
func (cs *CloudState) Delete(nativeID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	delete(cs.entries, nativeID)
}

// ListNativeIDs returns the NativeIDs of all entries matching the given resource type.
func (cs *CloudState) ListNativeIDs(resourceType string) []string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var ids []string
	for _, entry := range cs.entries {
		if entry.ResourceType == resourceType {
			ids = append(ids, entry.NativeID)
		}
	}
	return ids
}

// Snapshot returns a deep copy of all entries. Mutating the returned map
// does not affect the CloudState.
func (cs *CloudState) Snapshot() map[string]testcontrol.CloudStateEntry {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	snap := make(map[string]testcontrol.CloudStateEntry, len(cs.entries))
	maps.Copy(snap, cs.entries)
	return snap
}