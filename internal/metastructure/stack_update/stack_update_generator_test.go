// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package stack_update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestNewStackUpdateGenerator(t *testing.T) {
	mockDS := &mockStackDatastore{}
	generator := NewStackUpdateGenerator(mockDS)

	assert.NotNil(t, generator)
	assert.Equal(t, mockDS, generator.datastore)
}

func TestGenerateStackUpdates_DestroyCommand_ReturnsNil(t *testing.T) {
	generator := NewStackUpdateGenerator(&mockStackDatastore{})

	stacks := []pkgmodel.Stack{
		{Label: "test-stack", Description: "Test stack"},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandDestroy)

	assert.NoError(t, err)
	assert.Nil(t, updates)
}

func TestGenerateStackUpdates_CreateNewStack(t *testing.T) {
	mockDS := &mockStackDatastore{
		stacks: make(map[string]*pkgmodel.Stack),
	}
	generator := NewStackUpdateGenerator(mockDS)

	stacks := []pkgmodel.Stack{
		{
			Label:       "new-stack",
			Description: "A new stack",
		},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandApply)

	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, "new-stack", update.Stack.Label)
	assert.Equal(t, StackOperationCreate, update.Operation)
	assert.Equal(t, StackUpdateStateNotStarted, update.State)
	assert.Nil(t, update.ExistingStack)
	assert.True(t, update.HasChange())
}

func TestGenerateStackUpdates_UpdateExistingStack_DescriptionChanged(t *testing.T) {
	existing := &pkgmodel.Stack{
		ID:          "existing-id",
		Label:       "existing-stack",
		Description: "Original description",
	}

	mockDS := &mockStackDatastore{
		stacks: map[string]*pkgmodel.Stack{
			"existing-stack": existing,
		},
	}
	generator := NewStackUpdateGenerator(mockDS)

	stacks := []pkgmodel.Stack{
		{
			Label:       "existing-stack",
			Description: "Updated description", // Changed
		},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandApply)

	require.NoError(t, err)
	require.Len(t, updates, 1)

	update := updates[0]
	assert.Equal(t, "existing-stack", update.Stack.Label)
	assert.Equal(t, StackOperationUpdate, update.Operation)
	assert.Equal(t, StackUpdateStateNotStarted, update.State)
	assert.NotNil(t, update.ExistingStack)
	assert.Equal(t, existing, update.ExistingStack)
	assert.True(t, update.HasChange())
}

func TestGenerateStackUpdates_NoChange_DescriptionSame(t *testing.T) {
	existing := &pkgmodel.Stack{
		ID:          "existing-id",
		Label:       "existing-stack",
		Description: "Same description",
	}

	mockDS := &mockStackDatastore{
		stacks: map[string]*pkgmodel.Stack{
			"existing-stack": existing,
		},
	}
	generator := NewStackUpdateGenerator(mockDS)

	stacks := []pkgmodel.Stack{
		{
			Label:       "existing-stack",
			Description: "Same description", // Same as existing
		},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandApply)

	require.NoError(t, err)
	assert.Empty(t, updates) // No updates should be generated
}

func TestGenerateStackUpdates_DatastoreError(t *testing.T) {
	mockDS := &mockStackDatastore{
		shouldError: true,
	}
	generator := NewStackUpdateGenerator(mockDS)

	stacks := []pkgmodel.Stack{
		{Label: "error-stack", Description: "Test"},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandApply)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to determine stack update")
	assert.Nil(t, updates)
}

func TestGenerateStackUpdates_MultipleStacks_MixedScenarios(t *testing.T) {
	existingStack := &pkgmodel.Stack{
		ID:          "existing-id",
		Label:       "existing-stack",
		Description: "Original",
	}

	unchangedStack := &pkgmodel.Stack{
		ID:          "unchanged-id",
		Label:       "unchanged-stack",
		Description: "Unchanged",
	}

	mockDS := &mockStackDatastore{
		stacks: map[string]*pkgmodel.Stack{
			"existing-stack":  existingStack,
			"unchanged-stack": unchangedStack,
		},
	}
	generator := NewStackUpdateGenerator(mockDS)

	stacks := []pkgmodel.Stack{
		// New stack - should create
		{
			Label:       "new-stack",
			Description: "Brand new",
		},
		// Existing stack with change - should update
		{
			Label:       "existing-stack",
			Description: "Modified", // Changed
		},
		// Existing stack without change - should not generate update
		{
			Label:       "unchanged-stack",
			Description: "Unchanged", // Same
		},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandApply)

	require.NoError(t, err)
	require.Len(t, updates, 2) // Only new-stack and existing-stack

	// Find updates by stack label
	var newStackUpdate, existingStackUpdate *StackUpdate
	for i := range updates {
		if updates[i].Stack.Label == "new-stack" {
			newStackUpdate = &updates[i]
		} else if updates[i].Stack.Label == "existing-stack" {
			existingStackUpdate = &updates[i]
		}
	}

	require.NotNil(t, newStackUpdate)
	assert.Equal(t, StackOperationCreate, newStackUpdate.Operation)
	assert.Nil(t, newStackUpdate.ExistingStack)

	require.NotNil(t, existingStackUpdate)
	assert.Equal(t, StackOperationUpdate, existingStackUpdate.Operation)
	assert.NotNil(t, existingStackUpdate.ExistingStack)
}

func TestGenerateStackUpdates_SyncCommand_ProcessesStacks(t *testing.T) {
	mockDS := &mockStackDatastore{
		stacks: make(map[string]*pkgmodel.Stack),
	}
	generator := NewStackUpdateGenerator(mockDS)

	stacks := []pkgmodel.Stack{
		{Label: "sync-stack", Description: "Synced stack"},
	}

	updates, err := generator.GenerateStackUpdates(stacks, pkgmodel.CommandSync)

	require.NoError(t, err)
	require.Len(t, updates, 1)
	assert.Equal(t, StackOperationCreate, updates[0].Operation)
}

type mockStackDatastore struct {
	stacks      map[string]*pkgmodel.Stack
	shouldError bool
}

func (m *mockStackDatastore) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	if m.shouldError {
		return nil, assert.AnError
	}

	if m.stacks == nil {
		return nil, nil
	}

	stack, exists := m.stacks[label]
	if !exists {
		return nil, nil
	}

	return stack, nil
}
