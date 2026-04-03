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

func TestGenerateTargetUpdates_DestroyCommand_SkipsPlainTargetWhenFormaHasResources(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:        "plain-target",
		Namespace:    "default",
		Config:       json.RawMessage(`{"region": "us-east-1"}`),
		Discoverable: false,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"plain-target": existingTarget,
		},
		resourceCounts: map[string]int{
			"plain-target": 0,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "plain-target", Namespace: "default", Discoverable: false},
	}

	// formaHasResources=true: plain targets survive destroy of application formae
	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, true)

	require.NoError(t, err)
	assert.Empty(t, updates, "plain targets without $ref dependencies should survive destroy of application formae")
}

func TestGenerateTargetUpdates_DestroyCommand_DeletesDependentTarget(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:     "grafana",
		Namespace: "GRAFANA",
		Config:    json.RawMessage(`{"Endpoints": {"$ref": "formae://abc123#/endpoints"}}`),
		Version:   1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"grafana": existingTarget,
		},
		resourceCounts: map[string]int{},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "grafana", Namespace: "GRAFANA"},
	}

	// formaHasResources=true: targets with $ref are deleted even in application formae
	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, true)

	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, "grafana", update.Target.Label)
	assert.Equal(t, TargetOperationDelete, update.Operation)
	assert.Equal(t, TargetUpdateStateNotStarted, update.State)
	assert.NotNil(t, update.ExistingTarget)
	assert.Empty(t, update.RemainingResolvables, "delete target updates should not set RemainingResolvables")
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, false)

	assert.NoError(t, err)
	assert.Empty(t, updates) // No update for non-existent target
}

// TestGenerateTargetUpdates_DestroyCommand_SkipsPlainTargetWithResourcesWhenFormaHasResources verifies
// that a plain target (no $ref) with resources in the DB is NOT marked for deletion
// when the forma also has resources (application forma).
func TestGenerateTargetUpdates_DestroyCommand_SkipsPlainTargetWithResourcesWhenFormaHasResources(t *testing.T) {
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
			"target-with-resources": 3,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "target-with-resources", Namespace: "default", Discoverable: false},
	}

	// formaHasResources=true: plain targets survive destroy of application formae
	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, true)

	assert.NoError(t, err)
	assert.Empty(t, updates, "plain targets without $ref dependencies should survive destroy of application formae")
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)

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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)

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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)

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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)

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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)

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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)

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

// TestGenerateTargetUpdates_DestroyDeletesDependentTargetWithResources verifies that
// a target with $ref dependencies is deleted during destroy even when it has resources.
// The DAG handles cascade-deleting the resources before the target.
func TestGenerateTargetUpdates_DestroyDeletesDependentTargetWithResources(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:        "test-target",
		Namespace:    "default",
		Config:       json.RawMessage(`{"endpoint": {"$ref": "formae://abc123#/Endpoint"}}`),
		Discoverable: false,
		Version:      1,
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"test-target": existingTarget,
		},
		resourceCounts: map[string]int{
			"test-target": 2,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{Label: "test-target", Namespace: "default", Discoverable: false},
	}

	// formaHasResources=true: targets with $ref are deleted even in application formae
	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandDestroy, true)

	require.NoError(t, err)
	require.Len(t, updates, 1, "should generate a delete for targets with $ref dependencies")

	update := updates[0]
	assert.Equal(t, "test-target", update.Target.Label)
	assert.Equal(t, TargetOperationDelete, update.Operation)
	assert.Equal(t, TargetUpdateStateNotStarted, update.State)
	assert.NotNil(t, update.ExistingTarget)
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationCreate, updates[0].Operation)
	assert.Len(t, updates[0].RemainingResolvables, 1)
	assert.Equal(t, pkgmodel.FormaeURI("formae://abc123#/Endpoint"), updates[0].RemainingResolvables[0])
}

func TestGenerateTargetUpdates_ValueToRef_SameResolved_GeneratesUpdate(t *testing.T) {
	// Existing config has a plain value. New config wraps it in a $ref that
	// resolves to the same value. The resolved values are equal, but the raw
	// config format changed — this should produce an Update (to persist the
	// new $ref format), never a Replace.
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation, "value→$ref format change should produce Update, not Replace")
}

func TestGenerateTargetUpdates_RefToValue_SameResolved_GeneratesUpdate(t *testing.T) {
	// Existing config has a $ref wrapper (stored with metadata). New config
	// has the same value as a plain literal. Should produce Update to persist
	// the simplified format, never Replace.
	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": {
				Label:     "k8s-target",
				Namespace: "k8s",
				Config:    json.RawMessage(`{"endpoint": {"$ref": "formae://abc123#/Endpoint", "$value": "https://my-cluster.eks.amazonaws.com"}}`),
			},
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:     "k8s-target",
			Namespace: "k8s",
			Config:    json.RawMessage(`{"endpoint": "https://my-cluster.eks.amazonaws.com"}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation, "$ref→value format change should produce Update, not Replace")
}

func TestGenerateTargetUpdates_ValueToRef_SameResolved_ImmutableField_GeneratesUpdate(t *testing.T) {
	// Even when the field is immutable (createOnly=true), switching from a plain
	// value to a $ref that resolves to the same value should produce Update (format
	// change), never Replace. The resolved value didn't change.
	existingTarget := &pkgmodel.Target{
		Label:     "k8s-target",
		Namespace: "k8s",
		Config:    json.RawMessage(`{"endpoint": "https://my-cluster.eks.amazonaws.com"}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"endpoint": {CreateOnly: true},
			},
		},
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": existingTarget,
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation, "value→$ref on immutable field with same resolved value should produce Update, not Replace")
}

func TestGenerateTargetUpdates_RefToValue_SameResolved_ImmutableField_GeneratesUpdate(t *testing.T) {
	// Same as above but in reverse: $ref→value on an immutable field with same
	// resolved value should produce Update, not Replace.
	existingTarget := &pkgmodel.Target{
		Label:     "k8s-target",
		Namespace: "k8s",
		Config:    json.RawMessage(`{"endpoint": {"$ref": "formae://abc123#/Endpoint", "$value": "https://my-cluster.eks.amazonaws.com"}}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"endpoint": {CreateOnly: true},
			},
		},
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": existingTarget,
		},
	}
	generator := NewTargetUpdateGenerator(mockDS)

	targets := []pkgmodel.Target{
		{
			Label:     "k8s-target",
			Namespace: "k8s",
			Config:    json.RawMessage(`{"endpoint": "https://my-cluster.eks.amazonaws.com"}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation, "$ref→value on immutable field with same resolved value should produce Update, not Replace")
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation)
	assert.Len(t, updates[0].RemainingResolvables, 1)
}

// Tests for $ref configs with ConfigSchema (per-field mutability must work even when resolvables are present)

func TestGenerateTargetUpdates_RefConfig_MutableFieldChange_GeneratesUpdate(t *testing.T) {
	// Existing target has a resolved $ref for endpoint and a plain profile field.
	// Changing only profile (mutable) should produce Update, not Replace.
	existingTarget := &pkgmodel.Target{
		Label:     "k8s-target",
		Namespace: "k8s",
		Config:    json.RawMessage(`{"endpoint": "https://my-cluster.eks.amazonaws.com", "profile": "dev"}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"endpoint": {CreateOnly: true},
				"profile":  {CreateOnly: false},
			},
		},
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": existingTarget,
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
				"endpoint": {"$ref": "formae://abc123#/Endpoint"},
				"profile": "staging"
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation, "changing only a mutable field should produce Update even when config has $ref")
}

func TestGenerateTargetUpdates_RefConfig_ImmutableRefChange_GeneratesReplace(t *testing.T) {
	// Existing target has a resolved $ref for endpoint. The $ref now resolves
	// to a different value (endpoint changed). Since endpoint is immutable,
	// this should produce Replace.
	existingTarget := &pkgmodel.Target{
		Label:     "k8s-target",
		Namespace: "k8s",
		Config:    json.RawMessage(`{"endpoint": "https://old-cluster.eks.amazonaws.com", "profile": "dev"}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"endpoint": {CreateOnly: true},
				"profile":  {CreateOnly: false},
			},
		},
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": existingTarget,
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
				"endpoint": {"$ref": "formae://abc123#/Endpoint"},
				"profile": "dev"
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation, "changing an immutable $ref field should produce Replace")
}

func TestGenerateTargetUpdates_RefConfig_SameResolvedValue_FormatChange_GeneratesUpdate(t *testing.T) {
	// Resolved values are identical but the raw config format changed
	// (existing has plain value, new has $ref). This should produce Update
	// to persist the new format, never Replace.
	existingTarget := &pkgmodel.Target{
		Label:     "k8s-target",
		Namespace: "k8s",
		Config:    json.RawMessage(`{"endpoint": "https://my-cluster.eks.amazonaws.com", "profile": "dev"}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"endpoint": {CreateOnly: true},
				"profile":  {CreateOnly: false},
			},
		},
	}

	mockDS := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"k8s-target": existingTarget,
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
				"endpoint": {"$ref": "formae://abc123#/Endpoint"},
				"profile": "dev"
			}`),
		},
	}

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation, "value→$ref format change should produce Update, not Replace")
}

func TestDetermineTargetUpdate_MutableConfigChange_GeneratesUpdate(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:     "my-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"Region":  {CreateOnly: true},
				"Profile": {CreateOnly: false},
			},
		},
	}

	ds := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"my-target": existingTarget,
		},
	}
	gen := NewTargetUpdateGenerator(ds)
	updates, err := gen.GenerateTargetUpdates(
		[]pkgmodel.Target{{
			Label:     "my-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-east-1","Profile":"staging"}`),
		}},
		pkgmodel.CommandApply,
		true,
	)

	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationUpdate, updates[0].Operation)
}

func TestDetermineTargetUpdate_ImmutableConfigChange_WithSchema_GeneratesReplace(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:     "my-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1","Profile":"prod"}`),
		ConfigSchema: pkgmodel.ConfigSchema{
			Hints: map[string]pkgmodel.ConfigFieldHint{
				"Region":  {CreateOnly: true},
				"Profile": {CreateOnly: false},
			},
		},
	}

	ds := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"my-target": existingTarget,
		},
	}
	gen := NewTargetUpdateGenerator(ds)
	updates, err := gen.GenerateTargetUpdates(
		[]pkgmodel.Target{{
			Label:     "my-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-west-2","Profile":"prod"}`),
		}},
		pkgmodel.CommandApply,
		true,
	)

	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation)
}

func TestDetermineTargetUpdate_ConfigChange_NoSchema_GeneratesReplace(t *testing.T) {
	existingTarget := &pkgmodel.Target{
		Label:     "my-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		// No ConfigSchema — backwards compatible: all changes are immutable
	}

	ds := &mockTargetDatastore{
		targets: map[string]*pkgmodel.Target{
			"my-target": existingTarget,
		},
	}
	gen := NewTargetUpdateGenerator(ds)
	updates, err := gen.GenerateTargetUpdates(
		[]pkgmodel.Target{{
			Label:     "my-target",
			Namespace: "AWS",
			Config:    json.RawMessage(`{"Region":"us-west-2"}`),
		}},
		pkgmodel.CommandApply,
		true,
	)

	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation)
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

	updates, err := generator.GenerateTargetUpdates(targets, pkgmodel.CommandApply, false)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, TargetOperationReplace, updates[0].Operation)
}
