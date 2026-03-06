// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func RunCreateStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CreateStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := &pkgmodel.Stack{
			Label:       "my-stack",
			Description: "A test stack",
		}

		version, err := ds.CreateStack(stack, "cmd-1")
		assert.NoError(t, err)
		assert.NotEmpty(t, version) // Version is a ksuid now

		// Verify we can retrieve it
		retrieved, err := ds.GetStackByLabel("my-stack")
		assert.NoError(t, err)
		assert.NotNil(t, retrieved)
		assert.NotEmpty(t, retrieved.ID) // Should have a ksuid ID
		assert.Equal(t, "my-stack", retrieved.Label)
		assert.Equal(t, "A test stack", retrieved.Description)
	})
}

func RunCreateStackAlreadyExists(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CreateStack_AlreadyExists", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		stack := &pkgmodel.Stack{
			Label:       "duplicate-stack",
			Description: "First stack",
		}

		_, err := ds.CreateStack(stack, "cmd-1")
		assert.NoError(t, err)

		// Try to create another stack with the same label
		stack2 := &pkgmodel.Stack{
			Label:       "duplicate-stack",
			Description: "Second stack",
		}
		_, err = ds.CreateStack(stack2, "cmd-2")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stack already exists")
	})
}

func RunGetStackByLabelNotFound(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetStackByLabel_NotFound", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Try to get a non-existent stack
		retrieved, err := ds.GetStackByLabel("non-existent")
		assert.NoError(t, err)
		assert.Nil(t, retrieved)
	})
}

func RunUpdateStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("UpdateStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create initial stack
		stack := &pkgmodel.Stack{
			Label:       "update-test",
			Description: "Initial description",
		}
		_, err := ds.CreateStack(stack, "cmd-1")
		assert.NoError(t, err)

		// Get the original ID
		original, _ := ds.GetStackByLabel("update-test")
		originalID := original.ID

		// Sleep to ensure KSUID is in a different second (KSUID has 1-second precision)
		time.Sleep(1100 * time.Millisecond)

		// Update the description
		stack.Description = "Updated description"
		version, err := ds.UpdateStack(stack, "cmd-2")
		assert.NoError(t, err)
		assert.NotEmpty(t, version)

		// Verify the update
		retrieved, err := ds.GetStackByLabel("update-test")
		assert.NoError(t, err)
		assert.Equal(t, "Updated description", retrieved.Description)
		assert.Equal(t, originalID, retrieved.ID) // ID should remain the same
	})
}

func RunDeleteStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Check for pre-existing stacks
		existingStacks, _ := ds.ListAllStacks()
		t.Logf("DEBUG: Pre-existing stacks count: %d", len(existingStacks))
		for _, s := range existingStacks {
			t.Logf("DEBUG: Pre-existing stack: label=%s, id=%s", s.Label, s.ID)
		}

		// Create a stack
		stack := &pkgmodel.Stack{
			Label:       "delete-test",
			Description: "To be deleted",
		}
		createVersion, err := ds.CreateStack(stack, "cmd-1")
		assert.NoError(t, err)
		t.Logf("DEBUG: Created stack with version: %s", createVersion)

		// Sleep to ensure KSUID is in a different second (KSUID has 1-second precision)
		time.Sleep(1100 * time.Millisecond)

		// Delete it (tombstone)
		deleteVersion, err := ds.DeleteStack("delete-test", "cmd-2")
		assert.NoError(t, err)
		assert.NotEmpty(t, deleteVersion)
		t.Logf("DEBUG: Deleted stack with version: %s", deleteVersion)
		t.Logf("DEBUG: Version comparison - deleteVersion > createVersion: %v", deleteVersion > createVersion)

		// Verify it's no longer retrievable
		retrieved, err := ds.GetStackByLabel("delete-test")
		assert.NoError(t, err)
		if retrieved != nil {
			t.Logf("DEBUG: GetStackByLabel returned non-nil! ID=%s, Description=%s", retrieved.ID, retrieved.Description)
		}
		assert.Nil(t, retrieved)
	})
}

func RunDeleteStackThenRecreate(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("DeleteStack_ThenRecreate", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create a stack
		stack := &pkgmodel.Stack{
			Label:       "recreate-test",
			Description: "Original stack",
		}
		_, err := ds.CreateStack(stack, "cmd-1")
		assert.NoError(t, err)

		original, _ := ds.GetStackByLabel("recreate-test")
		originalID := original.ID

		// Sleep to ensure KSUID is in a different second (KSUID has 1-second precision)
		time.Sleep(1100 * time.Millisecond)

		// Delete it
		_, err = ds.DeleteStack("recreate-test", "cmd-2")
		assert.NoError(t, err)

		// Sleep to ensure KSUID is in a different second
		time.Sleep(1100 * time.Millisecond)

		// Recreate with same label
		stack2 := &pkgmodel.Stack{
			Label:       "recreate-test",
			Description: "New stack with same label",
		}
		_, err = ds.CreateStack(stack2, "cmd-3")
		assert.NoError(t, err)

		// Verify it's a new stack with different ID
		retrieved, err := ds.GetStackByLabel("recreate-test")
		assert.NoError(t, err)
		assert.NotNil(t, retrieved)
		assert.NotEqual(t, originalID, retrieved.ID) // Should have a new ID
		assert.Equal(t, "New stack with same label", retrieved.Description)
	})
}

func RunCountResourcesInStack(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("CountResourcesInStack", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		// Create stack
		stack := &pkgmodel.Stack{
			Label:       "count-test",
			Description: "Stack for counting",
		}
		_, err := ds.CreateStack(stack, "cmd-1")
		assert.NoError(t, err)

		// Initially no resources
		count, err := ds.CountResourcesInStack("count-test")
		assert.NoError(t, err)
		assert.Equal(t, 0, count)

		// Add a resource to the stack
		resource := &pkgmodel.Resource{
			Stack:  "count-test",
			Label:  "test-resource",
			Type:   "AWS::S3::Bucket",
			Target: "formae://target1",
			Ksuid:  mksuid.New().String(),
		}
		_, err = ds.StoreResource(resource, "cmd-1")
		assert.NoError(t, err)

		// Now count should be 1
		count, err = ds.CountResourcesInStack("count-test")
		assert.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}
