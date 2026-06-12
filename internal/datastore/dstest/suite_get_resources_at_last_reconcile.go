// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

// reconcileBuilder constructs a reconcile-mode apply forma_command with the
// given resource updates, suitable for exercising GetResourcesAtLastReconcile.
// startOffset is added to the current time to give the caller control over
// ordering across multiple commands.
func reconcileBuilder(
	state forma_command.CommandState,
	mode pkgmodel.FormaApplyMode,
	startOffset time.Duration,
	updates []resource_update.ResourceUpdate,
) *forma_command.FormaCommand {
	return &forma_command.FormaCommand{
		ID:              util.NewID(),
		Command:         pkgmodel.CommandApply,
		Config:          config.FormaCommandConfig{Mode: mode},
		State:           state,
		StartTs:         util.TimeNow().Add(startOffset),
		ModifiedTs:      util.TimeNow().Add(startOffset),
		ResourceUpdates: updates,
	}
}

// destroyBuilder constructs a destroy-command forma_command with the given
// delete-operation resource updates. Destroy commands are persisted with
// command='destroy' and config_mode='patch' (the default when
// FormaCommandConfig.Mode is empty, as set by the API server's DestroyForma
// path).
func destroyBuilder(
	state forma_command.CommandState,
	startOffset time.Duration,
	updates []resource_update.ResourceUpdate,
) *forma_command.FormaCommand {
	return &forma_command.FormaCommand{
		ID:              util.NewID(),
		Command:         pkgmodel.CommandDestroy,
		Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		State:           state,
		StartTs:         util.TimeNow().Add(startOffset),
		ModifiedTs:      util.TimeNow().Add(startOffset),
		ResourceUpdates: updates,
	}
}

// resourceUpdate constructs a ResourceUpdate where the DesiredState carries
// realistic top-level fields so that GetResourcesAtLastReconcile's
// JSON-extraction queries (Type/Label/Target/Properties/Schema/NativeID)
// return the values the test expects.
func resourceUpdate(
	stack, ksuid, label, props string,
	op types.OperationType,
	source resource_update.FormaCommandSource,
) resource_update.ResourceUpdate {
	return resource_update.ResourceUpdate{
		Operation:  op,
		State:      resource_update.ResourceUpdateStateSuccess,
		Source:     source,
		StackLabel: stack,
		DesiredState: pkgmodel.Resource{
			Ksuid:      ksuid,
			Label:      label,
			Type:       "AWS::S3::Bucket",
			Stack:      stack,
			Target:     "default-target",
			NativeID:   "native-" + label,
			Properties: json.RawMessage(props),
			Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
		},
	}
}

// RunGetResourcesAtLastReconcile_Empty verifies the contract for a fresh
// datastore: no rows, no error.
func RunGetResourcesAtLastReconcile_Empty(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_Empty", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		snaps, err := td.GetResourcesAtLastReconcile("any-stack")
		assert.NoError(t, err)
		assert.Empty(t, snaps)
	})
}

// RunGetResourcesAtLastReconcile_SuccessReturnsDesiredState verifies the
// happy path: a single successful user reconcile produces a snapshot of its
// DesiredStates.
func RunGetResourcesAtLastReconcile_SuccessReturnsDesiredState(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_SuccessReturnsDesiredState", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		cmd := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
				resourceUpdate("stack-a", "ksuid-2", "bucket-2", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(cmd, cmd.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 2)

		labels := map[string]bool{}
		for _, s := range snaps {
			labels[s.Label] = true
			assert.Equal(t, "AWS::S3::Bucket", s.Type)
			assert.Equal(t, "default-target", s.Target)
			assert.Equal(t, "native-"+s.Label, s.NativeID)
			assert.JSONEq(t, `{"foo":"v1"}`, string(s.Properties))
		}
		assert.True(t, labels["bucket-1"])
		assert.True(t, labels["bucket-2"])
	})
}

// RunGetResourcesAtLastReconcile_FailedReconcileIncluded verifies that a
// failed user reconcile still contributes its DesiredStates to the
// desired-state baseline, so auto-reconcile can retry the failed updates
// on subsequent ticks.
func RunGetResourcesAtLastReconcile_FailedReconcileIncluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_FailedReconcileIncluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		cmd := reconcileBuilder(
			forma_command.CommandStateFailed,
			pkgmodel.FormaApplyModeReconcile,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
				resourceUpdate("stack-a", "ksuid-2", "bucket-2", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(cmd, cmd.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 2, "Failed user reconcile must still contribute to desired state for retry")
	})
}

// RunGetResourcesAtLastReconcile_CanceledReconcileExcluded verifies a
// canceled user reconcile is NOT treated as accepted user intent.
func RunGetResourcesAtLastReconcile_CanceledReconcileExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_CanceledReconcileExcluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		// Older success that should remain the baseline.
		oldSuccess := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(oldSuccess, oldSuccess.ID))

		// Newer canceled reconcile — must not shift the baseline.
		canceled := reconcileBuilder(
			forma_command.CommandStateCanceled,
			pkgmodel.FormaApplyModeReconcile,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-2", "bucket-2", `{"foo":"v2"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(canceled, canceled.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 1, "Canceled reconcile should not become the baseline")
		assert.Equal(t, "bucket-1", snaps[0].Label, "Older successful reconcile should still be the baseline")
	})
}

// RunGetResourcesAtLastReconcile_InProgressReconcileExcluded verifies an
// in-progress reconcile is NOT considered. Its updates may still be
// rolling forward and shouldn't shift the baseline mid-flight.
func RunGetResourcesAtLastReconcile_InProgressReconcileExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_InProgressReconcileExcluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		oldSuccess := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(oldSuccess, oldSuccess.ID))

		inProgress := reconcileBuilder(
			forma_command.CommandStateInProgress,
			pkgmodel.FormaApplyModeReconcile,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-2", "bucket-2", `{"foo":"v2"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(inProgress, inProgress.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 1, "InProgress reconcile should not become the baseline")
		assert.Equal(t, "bucket-1", snaps[0].Label)
	})
}

// RunGetResourcesAtLastReconcile_DeleteRowsExcluded verifies that
// delete-side rows in a reconcile (implicit deletes or replace pairs) do
// not contribute to the desired-state baseline — the user's intent for
// deleted resources is "not exist".
func RunGetResourcesAtLastReconcile_DeleteRowsExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_DeleteRowsExcluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		cmd := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				// Replace pair: in production NewResourceUpdateForReplace
				// emits a delete and a create for the SAME ksuid (assigned
				// by BatchGetKSUIDsByTriplets matching the existing row).
				// Both rows share the command's timestamp. The query must
				// deterministically return the create side, not drop the
				// resource because the delete row won the row-number tie.
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationDelete, resource_update.FormaCommandSourceUser),
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v2"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
				// Implicit delete of a different resource that was removed
				// from the forma — must also be excluded from the baseline.
				resourceUpdate("stack-a", "ksuid-3", "bucket-removed", `{"foo":"old"}`, types.OperationDelete, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(cmd, cmd.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		if assert.Len(t, snaps, 1, "Only the create side of the replace pair should be returned") {
			assert.Equal(t, "ksuid-1", snaps[0].KSUID)
			assert.JSONEq(t, `{"foo":"v2"}`, string(snaps[0].Properties))
		}
	})
}

// RunGetResourcesAtLastReconcile_NonUserSourceExcluded verifies that a
// later auto-reconciler or sync command does not shift the baseline away
// from the most recent user-source reconcile.
func RunGetResourcesAtLastReconcile_NonUserSourceExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_NonUserSourceExcluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		userReconcile := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(userReconcile, userReconcile.ID))

		// A more recent auto-reconcile command — same stack, same mode, but
		// its resource_updates are source=auto-reconcile. Must not become
		// the baseline.
		autoReconcile := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"reverted"}`, types.OperationUpdate, resource_update.FormaCommandSourcePolicyAutoReconcile),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(autoReconcile, autoReconcile.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 1)
		assert.JSONEq(t, `{"foo":"v1"}`, string(snaps[0].Properties),
			"Auto-reconcile rows must not shift the user baseline")
	})
}

// RunGetResourcesAtLastReconcile_PatchModeExcluded verifies that a later
// patch-mode apply does not shift the reconcile baseline. Patches are
// intentional drift the next reconcile may overwrite.
func RunGetResourcesAtLastReconcile_PatchModeExcluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_PatchModeExcluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		reconcile := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(reconcile, reconcile.ID))

		patch := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModePatch,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"patched"}`, types.OperationUpdate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(patch, patch.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 1)
		assert.JSONEq(t, `{"foo":"v1"}`, string(snaps[0].Properties),
			"Patch-mode applies must not become the reconcile baseline")
	})
}

// RunGetResourcesAtLastReconcile_MostRecentReconcileWins verifies that
// when multiple reconciles for the same stack exist, the most recent one
// is returned, including when the most recent is Failed.
func RunGetResourcesAtLastReconcile_MostRecentReconcileWins(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_MostRecentReconcileWins", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		olderSuccess := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(olderSuccess, olderSuccess.ID))

		// Latest reconcile is Failed but represents the user's latest
		// intent — its DesiredState is what auto-reconcile should drive
		// toward, not the older Success.
		latestFailed := reconcileBuilder(
			forma_command.CommandStateFailed,
			pkgmodel.FormaApplyModeReconcile,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v2"}`, types.OperationUpdate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(latestFailed, latestFailed.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snaps, 1)
		assert.JSONEq(t, `{"foo":"v2"}`, string(snaps[0].Properties),
			"Latest Failed reconcile must win over older Success")
	})
}

// RunGetResourcesAtLastReconcile_StackScoped verifies the query is
// scoped to the requested stack and does not leak rows from other stacks.
func RunGetResourcesAtLastReconcile_StackScoped(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_StackScoped", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		cmd := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-a", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
				resourceUpdate("stack-b", "ksuid-2", "bucket-b", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(cmd, cmd.ID))

		snapsA, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Len(t, snapsA, 1)
		assert.Equal(t, "bucket-a", snapsA[0].Label)

		snapsB, err := td.GetResourcesAtLastReconcile("stack-b")
		assert.NoError(t, err)
		assert.Len(t, snapsB, 1)
		assert.Equal(t, "bucket-b", snapsB[0].Label)
	})
}

// RunGetResourcesAtLastReconcile_DestroyAfterApplyEmptiesBaseline verifies
// that a successful destroy of a stack supersedes the prior apply: the
// baseline becomes empty even though the apply's create rows are still
// present in resource_updates.
//
// Without the destroy-included filter, the apply's create rows would be the
// only candidates and the resource would remain in the baseline forever —
// driving auto-reconcile to resurrect it.
func RunGetResourcesAtLastReconcile_DestroyAfterApplyEmptiesBaseline(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_DestroyAfterApplyEmptiesBaseline", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		// Older successful apply.
		apply := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(apply, apply.ID))

		// Later successful destroy of the same resource.
		destroy := destroyBuilder(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationDelete, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(destroy, destroy.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Empty(t, snaps, "destroy must supersede the prior apply — baseline should be empty")
	})
}

// RunGetResourcesAtLastReconcile_FailedDestroyIncluded verifies that a
// failed destroy still contributes its delete rows to the latest-per-ksuid
// computation. The user's intent is "delete these"; the auto-reconciler
// must treat them as desired-state-empty even if the delete didn't succeed.
//
// Combined with the outer operation != 'delete' filter, a failed destroy
// yields an empty baseline for destroyed resources (correct: don't resurrect
// them; the user wanted them gone). A subsequent retry by the user (or by
// auto-reconcile itself, once it can do destroys) completes the work.
func RunGetResourcesAtLastReconcile_FailedDestroyIncluded(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_FailedDestroyIncluded", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		apply := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(apply, apply.ID))

		// Failed destroy — user intent still recorded.
		destroy := destroyBuilder(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "bucket-1", `{"foo":"v1"}`, types.OperationDelete, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(destroy, destroy.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		assert.Empty(t, snaps, "failed destroy still records the user's intent — baseline should be empty")
	})
}

// RunGetResourcesAtLastReconcile_PartialDestroyLeavesUntouchedInBaseline
// verifies that destroying one resource while another is left alone yields
// a baseline containing only the un-destroyed resource. The latest-per-ksuid
// logic picks the apply's create row for the surviving resource (its only
// touch) and the destroy's delete row for the removed one (which the outer
// filter drops).
func RunGetResourcesAtLastReconcile_PartialDestroyLeavesUntouchedInBaseline(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("GetResourcesAtLastReconcile_PartialDestroyLeavesUntouchedInBaseline", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		// Apply creates two resources.
		apply := reconcileBuilder(
			forma_command.CommandStateSuccess,
			pkgmodel.FormaApplyModeReconcile,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-1", "keeper", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
				resourceUpdate("stack-a", "ksuid-2", "doomed", `{"foo":"v1"}`, types.OperationCreate, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(apply, apply.ID))

		// Destroy targets only the second resource.
		destroy := destroyBuilder(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				resourceUpdate("stack-a", "ksuid-2", "doomed", `{"foo":"v1"}`, types.OperationDelete, resource_update.FormaCommandSourceUser),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(destroy, destroy.ID))

		snaps, err := td.GetResourcesAtLastReconcile("stack-a")
		assert.NoError(t, err)
		if assert.Len(t, snaps, 1, "only the untouched resource should remain in the baseline") {
			assert.Equal(t, "ksuid-1", snaps[0].KSUID)
			assert.Equal(t, "keeper", snaps[0].Label)
		}
	})
}
