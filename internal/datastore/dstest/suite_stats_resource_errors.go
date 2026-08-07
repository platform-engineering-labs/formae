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

// outcomeUpdate constructs a ResourceUpdate whose completion state and
// modified timestamp the caller controls, so a test can stage a sequence of
// per-resource outcomes without sleeping. modifiedOffset is added to the
// current time; resourceType is written to the DesiredState's Type, the label
// Stats().ResourceErrors counts under (pass "" to model an update whose
// DesiredState carries no type).
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

// RunStatsResourceErrors_StillFailedCounted verifies a resource whose only
// completed outcome is a failure is reported once under its type.
func RunStatsResourceErrors_StillFailedCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_StillFailedCounted", func(t *testing.T) {
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

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1}, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_FailedThenSucceededNotCounted verifies a later
// success supersedes the earlier failure, so the resource is no longer
// reported as an error.
func RunStatsResourceErrors_FailedThenSucceededNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_FailedThenSucceededNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors, "a later success must clear the earlier failure")
	})
}

// RunStatsResourceErrors_RepeatedFailuresCountOnce verifies repeated failures
// of the same resource are reported as one erroring resource, not one per
// attempt.
func RunStatsResourceErrors_RepeatedFailuresCountOnce(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_RepeatedFailuresCountOnce", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		for i, offset := range []time.Duration{-15 * time.Minute, -10 * time.Minute, -5 * time.Minute} {
			cmd := outcomeCommand(
				forma_command.CommandStateFailed,
				offset,
				[]resource_update.ResourceUpdate{
					outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
						types.OperationCreate, resource_update.ResourceUpdateStateFailed, offset),
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

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		inFlight := outcomeCommand(
			forma_command.CommandStateInProgress,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateInProgress, -1*time.Minute),
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

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		canceled := outcomeCommand(
			forma_command.CommandStateCanceled,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateCanceled, -1*time.Minute),
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

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		rejected := outcomeCommand(
			forma_command.CommandStateFailed,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateRejected, -1*time.Minute),
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
// earlier failure. Such a row has no label to report under, but it is a
// completed outcome: dropping it from the latest-outcome computation would
// leave the earlier failure reported forever.
func RunStatsResourceErrors_LatestSuccessWithEmptyTypeClearsFailure(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_LatestSuccessWithEmptyTypeClearsFailure", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		succeeded := outcomeCommand(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(succeeded, succeeded.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Empty(t, s.ResourceErrors,
			"a typeless later success is still the latest outcome and must clear the failure")
	})
}

// RunStatsResourceErrors_LatestFailedWithMissingTypeNotCounted verifies a
// failure whose DesiredState carries no type is not reported: there is no
// type to report it under, and it must not surface as an empty-string key.
func RunStatsResourceErrors_LatestFailedWithMissingTypeNotCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_LatestFailedWithMissingTypeNotCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-5*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -5*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.NotContains(t, s.ResourceErrors, "", "a typeless failure must not produce an empty-string key")
		assert.Empty(t, s.ResourceErrors)
	})
}

// RunStatsResourceErrors_ReplaceDeleteFailedThenCreateSucceeds verifies that
// when a replace's delete side fails and its create side later succeeds, the
// resource is not reported: the newest completed outcome wins.
func RunStatsResourceErrors_ReplaceDeleteFailedThenCreateSucceeds(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceDeleteFailedThenCreateSucceeds", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

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
// opposite completion order: a replace whose create side failed last leaves
// the resource reported as an error.
func RunStatsResourceErrors_ReplaceDeleteSucceedsThenCreateFailed(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_ReplaceDeleteSucceedsThenCreateFailed", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

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

		sameTs := -10 * time.Minute
		deleteSide := outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
			types.OperationDelete, resource_update.ResourceUpdateStateSuccess, sameTs)
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
			"an unresolvable tie must report the failure rather than hide it")
	})
}

// RunStatsResourceErrors_FailedDeleteCounted verifies a failed delete is
// reported: the resource is stuck and its type comes from the DesiredState
// the delete row carries.
func RunStatsResourceErrors_FailedDeleteCounted(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_FailedDeleteCounted", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

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
// resource type, each type counting only its own still-failing resources.
func RunStatsResourceErrors_GroupedByType(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("StatsResourceErrors_GroupedByType", func(t *testing.T) {
		td := newDS(t)
		defer td.CleanUpFn() //nolint:errcheck

		failed := outcomeCommand(
			forma_command.CommandStateFailed,
			-10*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-2", "bucket-2", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-3", "queue-1", "AWS::SQS::Queue",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
				outcomeUpdate("stack-a", "ksuid-4", "queue-2", "AWS::SQS::Queue",
					types.OperationCreate, resource_update.ResourceUpdateStateFailed, -10*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(failed, failed.ID))

		// One bucket and both queues are repaired; only bucket-2 stays failed.
		repaired := outcomeCommand(
			forma_command.CommandStateSuccess,
			-1*time.Minute,
			[]resource_update.ResourceUpdate{
				outcomeUpdate("stack-a", "ksuid-1", "bucket-1", "AWS::S3::Bucket",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
				outcomeUpdate("stack-a", "ksuid-3", "queue-1", "AWS::SQS::Queue",
					types.OperationCreate, resource_update.ResourceUpdateStateSuccess, -1*time.Minute),
			},
		)
		assert.NoError(t, td.StoreFormaCommand(repaired, repaired.ID))

		s, err := td.Stats()
		assert.NoError(t, err)
		assert.Equal(t, map[string]int{"AWS::S3::Bucket": 1, "AWS::SQS::Queue": 1}, s.ResourceErrors)
	})
}
