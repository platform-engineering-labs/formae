// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"sync"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// mockDatastore is a simple in-memory implementation of ResourceDataLookup for testing.
// This avoids importing the datastore package which would create a circular dependency.
type mockDatastore struct {
	mu      sync.RWMutex
	stacks  map[string]*pkgmodel.Forma
	triplet map[pkgmodel.TripletKey]string
}

func newMockDatastore() *mockDatastore {
	return &mockDatastore{
		stacks:  make(map[string]*pkgmodel.Forma),
		triplet: make(map[pkgmodel.TripletKey]string),
	}
}

func (m *mockDatastore) LoadStack(stackLabel string) (*pkgmodel.Forma, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stack, ok := m.stacks[stackLabel]
	if !ok {
		return nil, nil
	}
	return stack, nil
}

func (m *mockDatastore) LoadAllStacks() ([]*pkgmodel.Forma, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stacks := make([]*pkgmodel.Forma, 0, len(m.stacks))
	for _, stack := range m.stacks {
		stacks = append(stacks, stack)
	}
	return stacks, nil
}

func (m *mockDatastore) BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[pkgmodel.TripletKey]string)
	for _, triplet := range triplets {
		if ksuid, ok := m.triplet[triplet]; ok {
			result[triplet] = ksuid
		}
	}
	return result, nil
}

func (m *mockDatastore) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	triplet := pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}
	ksuid, ok := m.triplet[triplet]
	if !ok {
		return "", nil
	}
	return ksuid, nil
}

func (m *mockDatastore) LatestLabelForResource(label string) (string, error) {
	return label, nil
}

// StoreStack is a helper for tests to populate the mock datastore
func (m *mockDatastore) StoreStack(stack *pkgmodel.Forma, commandID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range stack.Stacks {
		m.stacks[s.Label] = stack

		// Also populate triplet map for KSUID lookups
		for _, r := range stack.Resources {
			if r.Stack == s.Label {
				triplet := pkgmodel.TripletKey{Stack: r.Stack, Label: r.Label, Type: r.Type}
				m.triplet[triplet] = r.Ksuid
			}
		}
	}
	return commandID, nil
}

// StoreResource is a helper for tests to store individual resources
func (m *mockDatastore) StoreResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	triplet := pkgmodel.TripletKey{Stack: resource.Stack, Label: resource.Label, Type: resource.Type}
	m.triplet[triplet] = resource.Ksuid
	return commandID, nil
}

// GetDeps creates a test datastore for internal tests.
// This is separate from the GetDeps in resource_update_generator_test.go
// to avoid circular import dependencies.
func GetDeps(t *testing.T) (*mockDatastore, *pkgmodel.Config) {
	t.Helper()

	ds := newMockDatastore()
	cfg := &pkgmodel.Config{}
	return ds, cfg
}
