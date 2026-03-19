// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package target_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestNewTargetUpdateGenerator(t *testing.T) {
	mockDS := &mockTargetDatastore{}
	generator := NewTargetUpdateGenerator(mockDS)

	assert.NotNil(t, generator)
	assert.Equal(t, mockDS, generator.datastore)
}

func TestGenerateTargetUpdates_DestroyCommand_DeletesEmptyTarget(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:        "empty-target",
		Namespace:    "default",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: false,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"empty-target": existingTarget,
		},
		resourceCounts: map[string]int{
			"empty-target": 0, // No resources
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "empty-target", Namespace: "default", Discoverable: false},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, nil)

	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, "empty-target", update.Target.Label)
	assert.Equal(t, TargetOperationDelete, update.Operation)
	assert.Equal(t, TargetUpdateStateNotStarted, update.State)
	assert.NotNil(t, update.ExistingTarget)
}

func TestGenerateTargetUpdates_DestroyCommand_TargetNotFound(t *testing.T) {
	mockDS := &mockTargetDatastore{
		targets:        make(map[string]*pkgmodel.Target),
		resourceCounts: make(map[string]int),
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "non-existent-target", Namespace: "default", Discoverable: true},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, nil)

	assert.NoError(t, err)
	assert.Empty(t, updates) // No update for non-existent target
}

// TestGenerateTargetUpdates_DestroyCommand_TargetHasResources verifies that a target
// with resources in the DB is still marked for deletion when the forma has no resources
// in that target. The DAG handles cascade-deleting the resources before the target.
func TestGenerateTargetUpdates_DestroyCommand_TargetHasResources(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:        "target-with-resources",
		Namespace:    "default",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: false,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"target-with-resources": existingTarget,
		},
		resourceCounts: map[string]int{
			"target-with-resources": 3, // Has resources in DB
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "target-with-resources", Namespace: "default", Discoverable: false},
	}

	// No resources in the forma for this target → target should be deleted
	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, nil)

	assert.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationDelete, updates[0].Operation)
	assert.Equal(t, "target-with-resources", updates[0].Target.Label)
}

func TestGenerateTargetUpdates_CreateNewTarget(t *testing.T) {
	mockDS := &mockTargetDatastore{
		targets: make(map[string]*pkgmodel.Target),
	}
	generator := NewTargetUpdateGenerator(mockDS)

	config := json.RawMessage(`{"region": "us-east-1"}`)
	targets := []pkgmodel.Target{
		{
			Label:        "new-target",
			Namespace:    "default",
			Config:       config,
			Discoverable: true,
			Version:      1,
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)

	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, "new-target", update.Target.Label)
	assert.Equal(t, TargetOperationCreate, update.Operation)
	assert.Equal(t, TargetUpdateStateNotStarted, update.State)
	assert.Nil(t, update.ExistingTarget)
	assert.True(t, update.HasChange())
}

func TestGenerateTargetUpdates_UpdateExistingTarget_DiscoverableChanged(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:        "existing-target",
		Namespace:    "default",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: false,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"existing-target": existing,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:        "existing-target",
			Namespace:    "default",
			Config:       json.RawMessage(`{"region": "us-east-1"}`),
			Discoverable: true, // Changed from false to true
			Version:      2,
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)

	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, "existing-target", update.Target.Label)
	assert.Equal(t, TargetOperationUpdate, update.Operation)
	assert.Equal(t, TargetUpdateStateNotStarted, update.State)
	assert.NotNil(t, update.ExistingTarget)
	assert.Equal(t, existing, update.ExistingTarget)
	assert.True(t, update.HasChange())
}

func TestGenerateTargetUpdates_NoChange_DiscoverableSame(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:        "existing-target",
		Namespace:    "default",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: true,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"existing-target": existing,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:        "existing-target",
			Namespace:    "default",
			Config:       json.RawMessage(`{"region": "us-east-1"}`),
			Discoverable: true, // Same as existing
			Version:      2,
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)

	require.NoError(t, err)
	assert.Empty(t, updates) // No updates should be generated
}

func TestGenerateTargetUpdates_ValidationError_NamespaceMismatch(t *testing.T) {
	existing := &pkgmodel.Target{
		Label:        "target-with-error",
		Namespace:    "prod",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: true,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"target-with-error": existing,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:        "target-with-error",
			Namespace:    "dev", // Different namespace
			Config:       json.RawMessage(`{"region": "us-east-1"}`),
			Discoverable: false,
			Version:      2,
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "target-with-error")
	assert.Nil(t, updates)
}

func TestGenerateTargetUpdates_DatastoreError(t *testing.T) {
	mockDS := &mockTargetDatastore{
		shouldError: true,
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "error-target", Namespace: "default", Discoverable: true},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to determine target update")
	assert.Nil(t, updates)
}

func TestGenerateTargetUpdates_MultipleTargets_MixedScenarios(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:        "existing-target",
		Namespace:    "default",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: false,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"existing-target": existingTarget,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		// New target - should create
		{
			Label:        "new-target",
			Namespace:    "default",
			Config:       json.RawMessage(`{"region": "us-west-1"}`),
			Discoverable: true,
		},
		// Existing target with change - should update
		{
			Label:        "existing-target",
			Namespace:    "default",
			Config:       json.RawMessage(`{"region": "us-east-1"}`),
			Discoverable: true, // Changed from false
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)

	require.NoError(t, err)
	require.Len(t, updates, 2)

	// Find updates by target label
	var newTargetUpdate, existingTargetUpdate *TargetUpdate
	for i := range updates {
		if updates[i].Target.Label == "new-target" {
			newTargetUpdate = &updates[i]
		} else if updates[i].Target.Label == "existing-target" {
			existingTargetUpdate = &updates[i]
		}
	}

	require.NotNil(t, newTargetUpdate)
	assert.Equal(t, TargetOperationCreate, newTargetUpdate.Operation)
	assert.Nil(t, newTargetUpdate.ExistingTarget)

	require.NotNil(t, existingTargetUpdate)
	assert.Equal(t, TargetOperationUpdate, existingTargetUpdate.Operation)
	assert.NotNil(t, existingTargetUpdate.ExistingTarget)
}

type mockTargetDatastore struct {
	targets        map[string]*pkgmodel.Target
	resources      map[string]*pkgmodel.Resource
	resourceCounts map[string]int
	shouldError    bool
}

func (m *mockTargetDatastore) LoadTarget(label string) (*pkgmodel.Target, error) {
	if m.shouldError {
		return nil, assert.AnError
	}

	if m.targets == nil {
		return nil, nil
	}

	target, exists := m.targets[label]
	if !exists {
		return nil, nil
	}

	return target, nil
}

func (m *mockTargetDatastore) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	if m.shouldError {
		return nil, assert.AnError
	}

	if m.resources == nil {
		return nil, nil
	}

	resource, exists := m.resources[ksuid]
	if !exists {
		return nil, nil
	}

	return resource, nil
}

func (m *mockTargetDatastore) CountResourcesInTarget(targetLabel string) (int, error) {
	if m.shouldError {
		return 0, assert.AnError
	}

	if m.resourceCounts == nil {
		return 0, nil
	}

	count, exists := m.resourceCounts[targetLabel]
	if !exists {
		return 0, nil
	}

	return count, nil
}

func TestGenerateTargetUpdates_ExtractsResolvablesForCreate(t *testing.T) {
	mockDS := &mockTargetDatastore{}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:     "k8s-target",
			Namespace: "k8s",
			Config: json.RawMessage(`{
				"endpoint": {"$ref": "formae://abc123#/Endpoint"}
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationCreate, updates[0].Operation)
	assert.Len(t, updates[0].RemainingResolvables, 1)
	assert.Equal(t, pkgmodel.FormaeURI("formae://abc123#/Endpoint"), updates[0].RemainingResolvables[0])
}

func TestGenerateTargetUpdates_ReapplyWithSameResolvedValue_NoReplace(t *testing.T) {
	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": {
				Label:     "k8s-target",
				Namespace: "k8s",
				Config:    json.RawMessage(`{"endpoint": "https://my-cluster.eks.amazonaws.com"}`),
			},
		},
		resources: map[string]*pkgmodel.Resource{
			"abc123": {
				Ksuid:      "abc123",
				Properties: json.RawMessage(`{"Endpoint": "https://my-cluster.eks.amazonaws.com"}`),
			},
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:     "k8s-target",
			Namespace: "k8s",
			Config: json.RawMessage(`{
				"endpoint": {"$ref": "formae://abc123#/Endpoint"}
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)
	require.NoError(t, err)
	// Same resolved value → no change needed, but still need resolvables for execution
	assert.Empty(t, updates)
}

func TestGenerateTargetUpdates_ReapplyWithDifferentResolvedValue_Replace(t *testing.T) {
	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": {
				Label:     "k8s-target",
				Namespace: "k8s",
				Config:    json.RawMessage(`{"endpoint": "https://old-cluster.eks.amazonaws.com"}`),
			},
		},
		resources: map[string]*pkgmodel.Resource{
			"abc123": {
				Ksuid:      "abc123",
				Properties: json.RawMessage(`{"Endpoint": "https://new-cluster.eks.amazonaws.com"}`),
			},
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:     "k8s-target",
			Namespace: "k8s",
			Config: json.RawMessage(`{
				"endpoint": {"$ref": "formae://abc123#/Endpoint"}
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation)
	assert.Len(t, updates[0].RemainingResolvables, 1)
}

func TestGenerateTargetUpdates_UnresolvableRef_TreatedAsChange(t *testing.T) {
	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": {
				Label:     "k8s-target",
				Namespace: "k8s",
				Config:    json.RawMessage(`{"endpoint": "https://old.eks.amazonaws.com"}`),
			},
		},
		// No resources — ref can't be resolved
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:     "k8s-target",
			Namespace: "k8s",
			Config: json.RawMessage(`{
				"endpoint": {"$ref": "formae://nonexistent#/Endpoint"}
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, nil)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation)
}
