// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outcomeUpdate constructs a ResourceUpdate whose completion state and
// modified timestamp the caller controls, so a test can stage a sequence of
// per-resource outcomes without sleeping. modifiedOffset is added to the
// current time; resourceType is written to the DesiredState's Type (pass "" to
// model an update whose DesiredState carries no type).
func outcomeUpdate(
	stack, ksuid, label, resourceType string,
	op types.OperationType,
	state resource_update.ResourceUpdateState,
	modifiedOffset time.Duration,
) resource_update.ResourceUpdate {
	return resource_update.ResourceUpdate{
		Operation:  op,
		State:      state,
		Source:     resource_update.FormaCommandSourceUser,
		StackLabel: stack,
		StartTs:    util.TimeNow().Add(modifiedOffset),
		ModifiedTs: util.TimeNow().Add(modifiedOffset),
		DesiredState: pkgmodel.Resource{
			Ksuid:      ksuid,
			Label:      label,
			Type:       resourceType,
			Stack:      stack,
			Target:     "default-target",
			NativeID:   "native-" + label,
			Properties: json.RawMessage(`{"foo":"v1"}`),
			Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
		},
	}
}

// outcomeCommand constructs a patch-mode apply forma_command carrying the
// given resource updates. Only the resource_updates rows matter to
// Stats().ResourceErrors, so the command itself is minimal; startOffset keeps
// commands distinguishable in time.
func outcomeCommand(
	state forma_command.CommandState,
	startOffset time.Duration,
	updates []resource_update.ResourceUpdate,
) *forma_command.FormaCommand {
	return &forma_command.FormaCommand{
		ID:              util.NewID(),
		Command:         pkgmodel.CommandApply,
		Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		State:           state,
		StartTs:         util.TimeNow().Add(startOffset),
		ModifiedTs:      util.TimeNow().Add(startOffset),
		ResourceUpdates: updates,
	}
}

// liveResource builds the resource a successful create writes to the live
// inventory. Its Type is the resources-row type column — the label
// Stats().ResourceErrors reports the resource under — which a test can set
// independently of the type its resource_updates rows carry.
func liveResource(stack, ksuid, label, resourceType string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Ksuid:      ksuid,
		Label:      label,
		Type:       resourceType,
		Stack:      stack,
		Target:     "default-target",
		NativeID:   "native-" + label,
		Managed:    true,
		Properties: json.RawMessage(`{"foo":"v1"}`),
		Schema:     pkgmodel.Schema{Fields: []string{"foo"}},
	}
}

// resourceIsLive reports whether the ksuid's current inventory row is neither a
// delete tombstone nor a reaped one — the liveness the error count is
// restricted to. It reads through LoadAllResources, which resolves the current
// row per uri before applying the tombstone filters.
func resourceIsLive(t *testing.T, td TestDatastore, ksuid string) bool {
	t.Helper()

	all, err := td.LoadAllResources()
	require.NoError(t, err)
	for _, res := range all {
		if res.Ksuid == ksuid {
			return true
		}
	}
	return false
}

// storeLiveResource writes the resources row a successful create leaves behind
// and proves the resource is live, so a case that expects it counted cannot
// pass for the wrong reason. The command the write is attributed to is the
// caller's, so a case staging two inventory versions of one ksuid can record
// them under the two commands that actually produced them. It returns the
// resource so a caller can tombstone it later.
func storeLiveResource(t *testing.T, td TestDatastore, res *pkgmodel.Resource, commandID string) *pkgmodel.Resource {
	t.Helper()

	_, err := td.StoreResource(res, commandID)
	require.NoError(t, err)
	require.True(t, resourceIsLive(t, td, res.Ksuid),
		"setup: %s must be in the live inventory", res.Ksuid)

	return res
}

// tombstoneResource records the delete a successful destroy leaves behind and
// proves the resource is no longer live, so a case that expects it dropped
// cannot pass because the tombstone never landed.
func tombstoneResource(t *testing.T, td TestDatastore, res *pkgmodel.Resource) {
	t.Helper()

	_, err := td.DeleteResource(res, "cmd-delete-"+res.Ksuid)
	require.NoError(t, err)
	require.False(t, resourceIsLive(t, td, res.Ksuid),
		"setup: %s must no longer be in the live inventory", res.Ksuid)
}

// RunStatsResourceErrors_Empty verifies the contract for a fresh datastore:
// no resource updates, no errors reported.
func RunStatsResourceErrors_Empty(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_Empty", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_StillFailedCounted verifies a live resource whose only
// completed outcome is a failure is reported once under its type.
func RunStatsResourceErrors_StillFailedCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_StillFailedCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_FailedThenSucceededNotCounted verifies a later
// success supersedes the earlier failure, so the resource is no longer
// reported as an error even though it is still live.
func RunStatsResourceErrors_FailedThenSucceededNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_FailedThenSucceededNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors, "a later success must clear the earlier failure")
	})
}

// RunStatsResourceErrors_RepeatedFailuresCountOnce verifies repeated failures
// of the same live resource are reported as one erroring resource, not one per
// attempt.
func RunStatsResourceErrors_RepeatedFailuresCountOnce(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_RepeatedFailuresCountOnce", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		// The create succeeded and left an inventory row; every later update
		// attempt against that row failed.
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		for i, offset := range []time.Duration{-15 * time.Minute, -10 * time.Minute, -5 * time.Minute} {
			cmd := outcomeCommand(
				forma_command.CommandStateFailed,
				offset,
				[]resource_update.ResourceUpdate{
					outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
						types.OperationUpdate, resource_update.ResourceUpdateStateFailed, offset),
				},
			)
			assert.NoError(t, td.StoreFormaCommand(cmd, cmd.ID), "attempt %d", i)
		}

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"three failed attempts on one resource are one erroring resource")
	})
}

// RunStatsResourceErrors_SucceededThenFailedCounted verifies the report
// follows recency, not state precedence: a failure after a success counts.
func RunStatsResourceErrors_SucceededThenFailedCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_SucceededThenFailedCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_InFlightRetryDoesNotClear verifies an in-flight
// retry does not clear a standing failure — only a completed outcome does.
func RunStatsResourceErrors_InFlightRetryDoesNotClear(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_InFlightRetryDoesNotClear", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		inFlight := outcomeCommand(
			forma_command.CommandStateInProgress,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateInProgress, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(inFlight, inFlight.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"an in-flight retry has no outcome yet and must not clear the failure")
	})
}

// RunStatsResourceErrors_CanceledDoesNotClear verifies a later canceled
// update leaves the standing failure reported: cancellation is not a
// successful repair.
func RunStatsResourceErrors_CanceledDoesNotClear(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_CanceledDoesNotClear", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		canceled := outcomeCommand(
			forma_command.CommandStateCanceled,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateCanceled, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(canceled, canceled.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"a canceled update must not clear the failure")
	})
}

// RunStatsResourceErrors_RejectedDoesNotClear verifies a later rejected
// update leaves the standing failure reported: the work never ran.
func RunStatsResourceErrors_RejectedDoesNotClear(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_RejectedDoesNotClear", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		// A command whose resource updates end up rejected reports overall as
		// failed; there is no separate rejected command state.
		rejected := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateRejected, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(rejected, rejected.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"a rejected update must not clear the failure")
	})
}

// RunStatsResourceErrors_LatestSuccessWithEmptyTypeClearsFailure verifies a
// later success whose DesiredState carries no type still supersedes the
// earlier failure. Such a row carries nothing to order it out of the picture:
// dropping it from the latest-outcome computation would leave the earlier
// failure reported for as long as the resource stays live.
func RunStatsResourceErrors_LatestSuccessWithEmptyTypeClearsFailure(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_LatestSuccessWithEmptyTypeClearsFailure", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a typeless later success is still the latest outcome and must clear the failure")
	})
}

// RunStatsResourceErrors_ReplaceDeleteFailedThenCreateSucceeds verifies that
// when a replace's delete side fails and its create side later succeeds, the
// resource is not reported: the newest completed outcome wins. The delete
// failed, so the inventory row was never tombstoned and the resource is live
// throughout.
func RunStatsResourceErrors_ReplaceDeleteFailedThenCreateSucceeds(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceDeleteFailedThenCreateSucceeds", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		// A replace emits a delete and a create row for the same ksuid; they
		// complete at different times within the one command.
		replace := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationDelete, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(replace, replace.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors, "the later successful create side is the latest outcome")
	})
}

// RunStatsResourceErrors_ReplaceDeleteSucceedsThenCreateFailed verifies the
// opposite completion order: given the resource is still in the live
// inventory, a replace whose create side failed last leaves it reported as an
// error. The inventory shape where the delete side's tombstone survives is
// pinned separately by
// RunStatsResourceErrors_ReplaceDeleteSucceededCreateFailedNotCounted.
func RunStatsResourceErrors_ReplaceDeleteSucceedsThenCreateFailed(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceDeleteSucceedsThenCreateFailed", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		replace := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationDelete, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(replace, replace.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_ReplaceSameTimestampFailedWins verifies the
// conservative tiebreak: when a replace's delete and create rows share a
// timestamp, the failure wins over the success, so an operator still sees
// the error.
func RunStatsResourceErrors_ReplaceSameTimestampFailedWins(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceSameTimestampFailedWins", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		// The failure is on the delete side, which sorts after the create side
		// by operation, so only the state tiebreak can select it.
		sameTs := -10 * time.Minute
		deleteSide := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationDelete, resource_update.ResourceUpdateStateFailed, sameTs)
		createSide := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationCreate, resource_update.ResourceUpdateStateSuccess, sameTs)
		createSide.ModifiedTs = deleteSide.ModifiedTs

		replace := outcomeCommand(
			forma_command.CommandStateFailed,
			sameTs,
			[]resource_update.ResourceUpdate{deleteSide, createSide},
		)
		assert.NoError(t, td.StoreFormaCommand(replace, replace.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"an unresolvable tie must report the failure rather than hide it")
	})
}

// RunStatsResourceErrors_ReplaceSameTimestampBothFailedCountsOnce verifies the
// count-once contract when a tie cannot be broken by state: a replace whose
// delete and create sides both failed at the same instant in the same command
// is one broken resource, not two.
func RunStatsResourceErrors_ReplaceSameTimestampBothFailedCountsOnce(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceSameTimestampBothFailedCountsOnce", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		sameTs := -10 * time.Minute
		deleteSide := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationDelete, resource_update.ResourceUpdateStateFailed, sameTs)
		createSide := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationCreate, resource_update.ResourceUpdateStateFailed, sameTs)
		createSide.ModifiedTs = deleteSide.ModifiedTs

		replace := outcomeCommand(
			forma_command.CommandStateFailed,
			sameTs,
			[]resource_update.ResourceUpdate{deleteSide, createSide},
		)
		assert.NoError(t, td.StoreFormaCommand(replace, replace.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"two failed rows for one resource are one error, not two")
	})
}

// RunStatsResourceErrors_NullTimestampFailureClearedBySuccess verifies that a
// failure carrying no modified_ts — the shape the normalizing migration leaves
// behind for commands that had none — is still superseded by a later success.
// A row with no timestamp is the oldest thing there is, not the newest; if it
// outranked real outcomes it would latch its resource type on the gauge for as
// long as the resource stays live.
func RunStatsResourceErrors_NullTimestampFailureClearedBySuccess(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_NullTimestampFailureClearedBySuccess", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.NullResourceUpdateModifiedTsForTest == nil {
			t.Skip("backend does not provide NullResourceUpdateModifiedTsForTest")
		}

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-20*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -20*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))
		assert.NoError(t, td.NullResourceUpdateModifiedTsForTest("ksuid-1"))

		recovered := outcomeCommand(
			forma_command.CommandStateSuccess,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(recovered, recovered.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a timestamped success supersedes a failure that carries no timestamp")
	})
}

// RunStatsResourceErrors_NullTimestampRepeatedFailuresCountOnce verifies the
// count-once contract still holds when none of a resource's failures carries a
// modified_ts, so recency cannot separate them.
func RunStatsResourceErrors_NullTimestampRepeatedFailuresCountOnce(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_NullTimestampRepeatedFailuresCountOnce", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.NullResourceUpdateModifiedTsForTest == nil {
			t.Skip("backend does not provide NullResourceUpdateModifiedTsForTest")
		}

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		for _, offset := range []time.Duration{-20 * time.Minute, -10 * time.Minute} {
			cmd := outcomeCommand(
				forma_command.CommandStateFailed,
				offset,
				[]resource_update.ResourceUpdate{
					outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
						types.OperationUpdate, resource_update.ResourceUpdateStateFailed, offset),
				},
			)
			assert.NoError(t, td.StoreFormaCommand(cmd, cmd.ID))
		}
		assert.NoError(t, td.NullResourceUpdateModifiedTsForTest("ksuid-1"))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"one resource is one error however many untimestamped failures it has")
	})
}

// RunStatsResourceErrors_MigratedTimestampSpellingClearedBySuccess verifies
// that recency is decided on the instant, not on the characters. A failure
// carrying the RFC 3339 spelling the normalizing migration copies out of the
// JSON blob must still be superseded by a later success written in the
// driver's own layout. Compared as text the migrated spelling sorts after the
// driver's on the same day — 'T' outranks ' ' — which would latch the failure.
func RunStatsResourceErrors_MigratedTimestampSpellingClearedBySuccess(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_MigratedTimestampSpellingClearedBySuccess", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.SetResourceUpdateModifiedTsRawForTest == nil {
			t.Skip("backend does not store modified_ts as text")
		}

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-20*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -20*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		// Restate the earlier failure the way the migration would have left it.
		migrated := util.TimeNow().Add(-20 * time.Minute).UTC().Format(time.RFC3339Nano)
		assert.NoError(t, td.SetResourceUpdateModifiedTsRawForTest("ksuid-1", migrated))

		recovered := outcomeCommand(
			forma_command.CommandStateSuccess,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(recovered, recovered.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a later success supersedes an earlier failure however its timestamp is spelled")
	})
}

// RunStatsResourceErrors_LaterCommandWinsOnTimestampTie verifies that when two
// commands carry a row for the same resource with an identical timestamp, the
// later command's row is the latest outcome: its success clears the earlier
// command's failure.
func RunStatsResourceErrors_LaterCommandWinsOnTimestampTie(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_LaterCommandWinsOnTimestampTie", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		sameTs := -10 * time.Minute
		failedRow := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationUpdate, resource_update.ResourceUpdateStateFailed, sameTs)
		succeededRow := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, sameTs)
		succeededRow.ModifiedTs = failedRow.ModifiedTs

		earlier := outcomeCommand(
			forma_command.CommandStateFailed,
			sameTs,
			[]resource_update.ResourceUpdate{failedRow},
		)
		later := outcomeCommand(
			forma_command.CommandStateSuccess,
			sameTs,
			[]resource_update.ResourceUpdate{succeededRow},
		)
		// Command ids are KSUIDs whose timestamp part has second granularity, so
		// two ids minted within the same second sort by their random payload.
		// Order the pair explicitly so the failure always carries the lower id.
		if earlier.ID > later.ID {
			earlier.ID, later.ID = later.ID, earlier.ID
		}
		assert.Less(t, earlier.ID, later.ID)

		assert.NoError(t, td.StoreFormaCommand(earlier, earlier.ID))
		assert.NoError(t, td.StoreFormaCommand(later, later.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"with timestamps tied the later command's success is the latest outcome")
	})
}

// RunStatsResourceErrors_FailedDeleteCounted verifies a failed delete is
// reported: the delete never landed, so the resource is stuck and still in the
// live inventory.
func RunStatsResourceErrors_FailedDeleteCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_FailedDeleteCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		// The create succeeded and wrote the inventory row; the delete failed,
		// so no tombstone was written and the row stays live.
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		created := outcomeCommand(
			forma_command.CommandStateSuccess,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(created, created.ID))

		failedDelete := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationDelete, resource_update.ResourceUpdateStateFailed, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failedDelete, failedDelete.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"a failed delete leaves the resource in error")
	})
}

// RunStatsResourceErrors_GroupedByType verifies counts are grouped by
// resource type, each type counting only its own still-failing live resources.
func RunStatsResourceErrors_GroupedByType(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_GroupedByType", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-2", "bucket-2", "AWS::S3::Bucket"), "cmd-create-bucket-2")
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-3", "queue-1", "AWS::SQS::Queue"), "cmd-create-queue-1")
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-4", "queue-2", "AWS::SQS::Queue"), "cmd-create-queue-2")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-2", "bucket-2", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-3", "queue-1", "AWS::SQS::Queue",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-4", "queue-2", "AWS::SQS::Queue",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		// One bucket and both queues are repaired; only bucket-2 stays failed.
		repaired := outcomeCommand(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
				outcomeUpdate("stack-a", "ksuid-3", "queue-1", "AWS::SQS::Queue",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(repaired, repaired.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1, "AWS::SQS::Queue": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_DeletedResourceNotCounted verifies a latched failure
// stops being reported once the resource's inventory row is tombstoned by a
// delete: the gauge counts what is live, not what a resource once did. It uses
// only the public Datastore API, so every backend runs it.
func RunStatsResourceErrors_DeletedResourceNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_DeletedResourceNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		res := storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		before, err := td.Stats()
		require.NoError(t, err)
		require.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, before.ResourceErrors,
			"setup: the failure must be reported while the resource is live")

		tombstoneResource(t, td, res)

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a deleted resource's failure must not stay on the gauge")
	})
}

// RunStatsResourceErrors_ReapedResourceNotCounted verifies a latched failure
// stops being reported once the resource's inventory row carries the reaped
// tombstone — the other way a row leaves the live inventory.
func RunStatsResourceErrors_ReapedResourceNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReapedResourceNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.MarkResourceReapedForTest == nil {
			t.Skip("backend does not expose MarkResourceReapedForTest")
		}

		res := storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		before, err := td.Stats()
		require.NoError(t, err)
		require.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, before.ResourceErrors,
			"setup: the failure must be reported while the resource is live")

		require.NoError(t, td.MarkResourceReapedForTest(string(res.URI())))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a reaped resource's failure must not stay on the gauge")
	})
}

// RunStatsResourceErrors_UntrackedKsuidNotCounted verifies a failure whose
// ksuid has no row in the resources table at all — the shape a create that
// never succeeded leaves behind — is not reported. This is a deliberate loss
// of signal: a resource that was never recorded as existing has no live row to
// label or to count against.
func RunStatsResourceErrors_UntrackedKsuidNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_UntrackedKsuidNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		require.False(t, resourceIsLive(t, td, "ksuid-1"),
			"setup: a failed create must leave no inventory row")

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a ksuid with no inventory row has nothing to report")
	})
}

// RunStatsResourceErrors_ReplaceDeleteSucceededCreateFailedNotCounted verifies
// the second shape that drops off the gauge: a replace whose delete side
// succeeded (tombstoning the row) and whose create side then failed (writing
// no row) leaves the ksuid with a latched failure and nothing live. Like an
// untracked create this is an accepted loss of signal, recorded here so it
// cannot change unnoticed.
func RunStatsResourceErrors_ReplaceDeleteSucceededCreateFailedNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceDeleteSucceededCreateFailedNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		res := storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")
		tombstoneResource(t, td, res)

		replace := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationDelete, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(replace, replace.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a replace that deleted the row and failed to recreate it leaves nothing live to count")
	})
}

// RunStatsResourceErrors_FailedDeleteStillCounted verifies the live-inventory
// restriction does not over-filter the commonest stuck resource: a delete that
// failed wrote no tombstone, so the row is still live and the failure is still
// reported.
func RunStatsResourceErrors_FailedDeleteStillCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_FailedDeleteStillCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failedDelete := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationDelete, resource_update.ResourceUpdateStateFailed, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failedDelete, failedDelete.ID))

		require.True(t, resourceIsLive(t, td, "ksuid-1"),
			"setup: a failed delete must leave the row live")

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"a delete operation that failed must not be mistaken for a tombstone")
	})
}

// RunStatsResourceErrors_FailedUpdateOnLiveResourceCounted verifies the
// commonest real error is reported: a resource that was created successfully
// and whose latest update failed is live and broken.
func RunStatsResourceErrors_FailedUpdateOnLiveResourceCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_FailedUpdateOnLiveResourceCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		created := outcomeCommand(
			forma_command.CommandStateSuccess,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(created, created.ID))

		failedUpdate := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failedUpdate, failedUpdate.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_UnmanagedResourceCounted verifies an unmanaged live
// resource is reported like any other. The live-resource count applies no
// managed filter, so the error count must not apply one either — otherwise the
// two gauges would disagree about which inventory they describe.
func RunStatsResourceErrors_UnmanagedResourceCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_UnmanagedResourceCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		unmanaged := liveResource(constants.UnmanagedStack, "ksuid-1", "bucket-1", "AWS::S3::Bucket")
		unmanaged.Managed = false
		storeLiveResource(t, td, unmanaged, "cmd-discover-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate(constants.UnmanagedStack, "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceTypes,
			"the live-resource count includes unmanaged resources")
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_TombstonedKsuidWithLaterSuccessNotCounted verifies a
// resource that was deleted and then recreated under the same ksuid does not
// carry its pre-delete failure back onto the gauge: it is live again, and its
// latest outcome is a success.
func RunStatsResourceErrors_TombstonedKsuidWithLaterSuccessNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_TombstonedKsuidWithLaterSuccessNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		res := storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-20*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -20*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		tombstoneResource(t, td, res)

		// The same ksuid is created again, so the tombstone is no longer the
		// resource's current row.
		recreated := liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket")
		recreated.Properties = json.RawMessage(`{"foo":"v2"}`)
		storeLiveResource(t, td, recreated, "cmd-recreate-bucket-1")

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a resource that is live again and succeeding must not report its pre-delete failure")
	})
}

// RunStatsResourceErrors_TombstonedKsuidWithNoLaterRowNotCounted verifies the
// shape a failed rebind leaves behind: the ksuid's inventory row was
// tombstoned, the attempt to bring it back failed, and no later row exists. The
// ksuid's latest outcome is a failure but nothing of it is live, so it is not
// reported.
func RunStatsResourceErrors_TombstonedKsuidWithNoLaterRowNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_TombstonedKsuidWithNoLaterRowNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		res := storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")
		tombstoneResource(t, td, res)

		failedRebind := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failedRebind, failedRebind.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a tombstoned ksuid with no later row has nothing live to report")
	})
}

// RunStatsResourceErrors_NewKsuidForSameTripletClearsGauge verifies that when a
// stack/label/type is rebound to a new ksuid, the gauge follows the new one:
// the retired ksuid's latched failure is tombstoned out of the inventory, the
// new ksuid is live and succeeding, and nothing is reported.
func RunStatsResourceErrors_NewKsuidForSameTripletClearsGauge(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_NewKsuidForSameTripletClearsGauge", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		retired := storeLiveResource(t, td, liveResource("stack-a", "ksuid-old", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-20*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-old", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -20*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		tombstoneResource(t, td, retired)

		// The same stack/label/type comes back under a fresh ksuid.
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-new", "bucket-1", "AWS::S3::Bucket"), "cmd-rebind-bucket-1")

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-new", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"the retired ksuid's failure must not outlive its inventory row")
	})
}

// RunStatsResourceErrors_LabelledFromLiveRowOnCaseOnlyTypeChange verifies the
// label comes from the live inventory row, not from the type the failure was
// recorded under. A resource whose updates were recorded under one spelling of
// its type and whose live row carries another spelling that differs only in
// case is reported under the live row's spelling, and two such resources
// aggregate into that one type rather than splitting across spellings.
func RunStatsResourceErrors_LabelledFromLiveRowOnCaseOnlyTypeChange(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_LabelledFromLiveRowOnCaseOnlyTypeChange", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		const liveType = "GRAFANA::Core::Dashboard"
		const recordedType = "Grafana::Core::Dashboard"

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "dashboard-1", liveType), "cmd-create-dashboard-1")
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-2", "dashboard-2", liveType), "cmd-create-dashboard-2")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "dashboard-1", recordedType,
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
				outcomeUpdate("stack-a", "ksuid-2", "dashboard-2", liveType,
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{liveType: 2}, s.ResourceErrors,
			"failures recorded under different spellings of a type aggregate under the live one")
	})
}

// RunStatsResourceErrors_OneKsuidUnderTwoLiveUrisCountedOnce verifies a ksuid
// carrying two simultaneously-live rows is one erroring resource, not two. The
// resources table keys on (uri, version) and indexes ksuid without a unique
// constraint, so nothing in the schema prevents the fan-out; the label is taken
// from the greatest-version row, so the count lands in exactly one type.
func RunStatsResourceErrors_OneKsuidUnderTwoLiveUrisCountedOnce(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_OneKsuidUnderTwoLiveUrisCountedOnce", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawInsertResourceRow == nil {
			t.Skip("backend does not expose RawInsertResourceRow")
		}

		// Two live rows for one ksuid under different uris and different types.
		// The versions are ordered so the greatest-version row is the topic.
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1#", "AAAAAAAAAAAAAAAAAAAAAAAAAAAA", "ksuid-1",
			"AWS::SQS::Queue", "default-target", string(types.OperationCreate)))
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1-alias#", "BBBBBBBBBBBBBBBBBBBBBBBBBBBB", "ksuid-1",
			"AWS::SNS::Topic", "default-target", string(types.OperationCreate)))

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "thing-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::SNS::Topic": 1}, s.ResourceErrors,
			"one ksuid is one erroring resource however many live rows carry it")
	})
}

// RunStatsResourceErrors_OneKsuidUnderTwoLiveUrisAtSameVersionCountedOnce
// verifies the collapse to one row per ksuid happens even when the versions of
// its live rows are equal. Selecting the greatest version alone leaves both
// rows of a tie standing and reports the resource twice; the uri tiebreak is
// what reduces them to one. The case asserts the resource is reported once
// under one of the two live rows' types, not which of them wins, so it holds
// whichever way the tiebreak is ordered.
func RunStatsResourceErrors_OneKsuidUnderTwoLiveUrisAtSameVersionCountedOnce(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_OneKsuidUnderTwoLiveUrisAtSameVersionCountedOnce", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawInsertResourceRow == nil {
			t.Skip("backend does not expose RawInsertResourceRow")
		}

		// Two live rows for one ksuid under different uris and different types,
		// carrying the same version so recency cannot separate them.
		const sameVersion = "AAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1#", sameVersion, "ksuid-1",
			"AWS::SQS::Queue", "default-target", string(types.OperationCreate)))
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1-alias#", sameVersion, "ksuid-1",
			"AWS::SNS::Topic", "default-target", string(types.OperationCreate)))

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "thing-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)

		reported := 0
		for resourceType, count := range s.ResourceErrors {
			reported += count
			assert.Contains(t, []string{"AWS::SQS::Queue", "AWS::SNS::Topic"}, resourceType,
				"the surviving row labels the count, whichever side of the tie survives")
		}
		assert.Len(t, s.ResourceErrors, 1,
			"one erroring resource lands under exactly one type")
		assert.Equal(t, 1, reported,
			"tied versions must still collapse to one row, not report the resource twice")
	})
}

// RunStatsResourceErrors_EmptyTypeNotBackfilledFromOlderLiveRow verifies a
// typeless row is dropped after the collapse to one row per ksuid, not before
// it. A resource whose current row carries no type and whose older row carries
// a real one must not be reported under the older row's type: excluding
// typeless rows up front would promote the older row and report the resource
// under a type it no longer has.
func RunStatsResourceErrors_EmptyTypeNotBackfilledFromOlderLiveRow(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_EmptyTypeNotBackfilledFromOlderLiveRow", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawInsertResourceRow == nil {
			t.Skip("backend does not expose RawInsertResourceRow")
		}

		// Two live rows for one ksuid: the lesser version carries a real type,
		// the greatest version carries none.
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1#", "AAAAAAAAAAAAAAAAAAAAAAAAAAAA", "ksuid-1",
			"AWS::S3::Bucket", "default-target", string(types.OperationCreate)))
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1-alias#", "BBBBBBBBBBBBBBBBBBBBBBBBBBBB", "ksuid-1",
			"", "default-target", string(types.OperationCreate)))

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "thing-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a ksuid whose current row is typeless must not be reported under an older row's type")
	})
}

// RunStatsResourceErrors_TypedCurrentRowCountedDespiteTypelessOlderRow is the
// mirror of RunStatsResourceErrors_EmptyTypeNotBackfilledFromOlderLiveRow: the
// typeless row is the older one and the current row carries a real type, so the
// resource must be reported under that type. Only the collapsed row's type
// decides whether a resource is reported; an implementation that excludes every
// ksuid holding any typeless live row would drop this one.
func RunStatsResourceErrors_TypedCurrentRowCountedDespiteTypelessOlderRow(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_TypedCurrentRowCountedDespiteTypelessOlderRow", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		if td.RawInsertResourceRow == nil {
			t.Skip("backend does not expose RawInsertResourceRow")
		}

		// Two live rows for one ksuid: the lesser version carries no type, the
		// greatest version carries a real one.
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1#", "AAAAAAAAAAAAAAAAAAAAAAAAAAAA", "ksuid-1",
			"", "default-target", string(types.OperationCreate)))
		require.NoError(t, td.RawInsertResourceRow(
			"formae://ksuid-1-alias#", "BBBBBBBBBBBBBBBBBBBBBBBBBBBB", "ksuid-1",
			"AWS::SQS::Queue", "default-target", string(types.OperationCreate)))

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "thing-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::SQS::Queue": 1}, s.ResourceErrors,
			"a ksuid whose current row is typed is reported under it, whatever an older row carries")
	})
}

// RunStatsResourceErrors_TypeComesFromLiveResourceRow verifies the reported
// type is the live inventory row's type, not the type stored on the failing
// update. The two disagree whenever a resource's type was rewritten after the
// failure was recorded.
func RunStatsResourceErrors_TypeComesFromLiveResourceRow(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_TypeComesFromLiveResourceRow", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "thing-1", "AWS::SQS::Queue"), "cmd-create-thing-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "thing-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::SQS::Queue": 1}, s.ResourceErrors,
			"the label is the live row's type, not the one the failure was recorded under")
	})
}

// RunStatsResourceErrors_LiveResourceWithEmptyTypeNotCounted verifies a live
// row carrying no type is not reported: there is nothing to report it under,
// and it must not surface as an empty-string key on the gauge.
func RunStatsResourceErrors_LiveResourceWithEmptyTypeNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_LiveResourceWithEmptyTypeNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		storeLiveResource(t, td, liveResource("stack-a", "ksuid-1", "thing-1", ""), "cmd-create-thing-1")

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "thing-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.NotContains(t, s.ResourceErrors, "", "a typeless live row must not produce an empty-string key")
		assert.Empty(t, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_ErrorsNeverExceedLiveResourcesOfThatType verifies the
// invariant the two gauges are read against each other by: for every type, the
// number of erroring resources is at most the number of live resources of that
// type. The fixture mixes live failures, tombstoned failures, failures whose
// ksuid never reached the inventory, and healthy resources — the history that
// makes an update-only count drift above the live inventory.
//
// The two counts are separate statements with no enclosing snapshot, so this
// asserts the invariant on a quiescent datastore; it is not a claim about the
// gauges read concurrently with a running agent.
func RunStatsResourceErrors_ErrorsNeverExceedLiveResourcesOfThatType(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ErrorsNeverExceedLiveResourcesOfThatType", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		// Three live buckets, one of which is failing.
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-b1", "bucket-1", "AWS::S3::Bucket"), "cmd-create-bucket-1")
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-b2", "bucket-2", "AWS::S3::Bucket"), "cmd-create-bucket-2")
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-b3", "bucket-3", "AWS::S3::Bucket"), "cmd-create-bucket-3")

		// One live, healthy queue — and two queues that were deleted while
		// failing.
		storeLiveResource(t, td, liveResource("stack-a", "ksuid-q1", "queue-1", "AWS::SQS::Queue"), "cmd-create-queue-1")
		gone1 := storeLiveResource(t, td, liveResource("stack-a", "ksuid-q2", "queue-2", "AWS::SQS::Queue"), "cmd-create-queue-2")
		gone2 := storeLiveResource(t, td, liveResource("stack-a", "ksuid-q3", "queue-3", "AWS::SQS::Queue"), "cmd-create-queue-3")

		history := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-b1", "bucket-1", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-b2", "bucket-2", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-b3", "bucket-3", "AWS::S3::Bucket",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-q1", "queue-1", "AWS::SQS::Queue",
					types.OperationUpdate, resource_update.ResourceUpdateStateSuccess, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-q2", "queue-2", "AWS::SQS::Queue",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-q3", "queue-3", "AWS::SQS::Queue",
					types.OperationUpdate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				// A create that never reached the inventory.
				outcomeUpdate("stack-a", "ksuid-q4", "queue-4", "AWS::SQS::Queue",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(history, history.ID))

		tombstoneResource(t, td, gone1)
		tombstoneResource(t, td, gone2)

		s, err := td.Stats()
		assert.NoError(t, err)

		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 3, "AWS::SQS::Queue": 1}, s.ResourceTypes,
			"setup: the live inventory is three buckets and one queue")
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors,
			"only the live failing bucket is an erroring resource")

		for resourceType, errors := range s.ResourceErrors {
			assert.LessOrEqual(t, errors, s.ResourceTypes[resourceType],
				"type %s reports %d erroring resources against %d live resources",
				resourceType, errors, s.ResourceTypes[resourceType])
		}
	})
}
