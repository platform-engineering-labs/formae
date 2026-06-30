// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// neverReturningOverrides models a plugin whose operations never complete: Create
// reports InProgress and Status keeps reporting InProgress forever. Without --force a
// command using this plugin would hang indefinitely (no meaningful timeout exists for
// legitimately-unbounded operations); --force is the escape hatch.
func neverReturningOverrides() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
			return &resource.CreateResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
					RequestID:       "request-" + request.Label,
					NativeID:        "native-" + request.Label,
				},
			}, nil
		},
		Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
			// Never completes.
			return &resource.StatusResult{
				ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
					RequestID:       request.RequestID,
				},
			}, nil
		},
	}
}

// twoBucketForma builds a forma with independent resources that all start immediately
// and stay InProgress under the never-returning override.
func twoBucketForma() *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "bucket1",
				Type:       "FakeAWS::S3::Bucket",
				Properties: json.RawMessage(`{"BucketName": "test-bucket-1"}`),
				Stack:      "test-stack",
				Target:     "test-target",
				Managed:    true,
			},
			{
				Label:      "bucket2",
				Type:       "FakeAWS::S3::Bucket",
				Properties: json.RawMessage(`{"BucketName": "test-bucket-2"}`),
				Stack:      "test-stack",
				Target:     "test-target",
				Managed:    true,
			},
			{
				Label:      "vpc",
				Type:       "FakeAWS::EC2::VPC",
				Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				Stack:      "test-stack",
				Target:     "test-target",
				Managed:    true,
			},
		},
		Targets: []pkgmodel.Target{{Label: "test-target"}},
	}
}

// loadSingleCommand loads the (single) command from the datastore.
func loadSingleCommand(t *testing.T, ds datastore.Datastore) *forma_command.FormaCommand {
	t.Helper()
	commands, err := ds.LoadFormaCommands()
	require.NoError(t, err)
	require.Len(t, commands, 1)
	return commands[0]
}

// TestMetastructure_ForceCancelCommand_ReachesCanceledImmediately verifies that --force
// drives a command with multiple InProgress resources (gated on a never-returning plugin)
// to a terminal Canceled state, and that each force-canceled-in-progress resource update
// gets a progress entry carrying OperationStatus = OperationStatusCanceled.
func TestMetastructure_ForceCancelCommand_ReachesCanceledImmediately(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, neverReturningOverrides())
		defer def()
		require.NoError(t, err)

		resp, err := m.ApplyForma(twoBucketForma(), &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)
		require.NotNil(t, resp)
		commandID := resp.CommandID

		// Wait for at least 2 resources to be InProgress.
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(commands) != 1 || len(commands[0].ResourceUpdates) != 3 {
				return false
			}
			inProgress := 0
			for _, ru := range commands[0].ResourceUpdates {
				if ru.State == resource_update.ResourceUpdateStateInProgress {
					inProgress++
				}
			}
			return inProgress >= 2
		}, 10*time.Second, 50*time.Millisecond, "expected at least 2 resource updates to be in progress")

		// Force-cancel.
		cancelResp, err := m.CancelCommand(commandID, true, "test-client")
		require.NoError(t, err)
		require.NotNil(t, cancelResp)

		// The command must reach a terminal Canceled state quickly.
		assert.Eventually(t, func() bool {
			cmd := loadSingleCommand(t, m.Datastore)
			return cmd.State == forma_command.CommandStateCanceled
		}, 5*time.Second, 50*time.Millisecond, "expected command to reach Canceled state immediately")

		cmd := loadSingleCommand(t, m.Datastore)
		require.Len(t, cmd.ResourceUpdates, 3)

		// Every resource update must be terminal Canceled.
		forceCanceledMidOp := 0
		for _, ru := range cmd.ResourceUpdates {
			assert.Equal(t, resource_update.ResourceUpdateStateCanceled, ru.State,
				"resource %s should be Canceled", ru.DesiredState.Label)

			// In-progress resources get a force-cancel progress entry.
			hasCanceledProgress := false
			for _, p := range ru.ProgressResult {
				if p.OperationStatus == resource.OperationStatusCanceled {
					hasCanceledProgress = true
				}
			}
			if hasCanceledProgress {
				forceCanceledMidOp++
			}
		}

		// At least the 2 InProgress resources must carry a Canceled progress entry.
		assert.GreaterOrEqual(t, forceCanceledMidOp, 2,
			"expected at least 2 force-canceled-in-progress resources to carry an OperationStatusCanceled progress entry")

		// The cancel response must report the force-canceled-in-progress URIs.
		assert.GreaterOrEqual(t, len(cancelResp.ForceCanceledInProgress), 2,
			"expected the cancel response to report the force-canceled-in-progress resources")

		// No resources should be persisted to the stack (none completed).
		resources, err := m.Datastore.LoadResourcesByStack("test-stack")
		if err == nil {
			assert.Empty(t, resources, "no resources should be persisted: all were abandoned mid-create")
		}
	})
}

// TestMetastructure_ForceCancelCommand_ConvergesWhereNonForceHangs is the deterministic
// version of the real escape-hatch case: under the never-returning plugin a non-force
// cancel would leave the command waiting forever. With --force the command converges to
// Canceled within a short, conservative bound.
func TestMetastructure_ForceCancelCommand_ConvergesWhereNonForceHangs(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, neverReturningOverrides())
		defer def()
		require.NoError(t, err)

		resp, err := m.ApplyForma(twoBucketForma(), &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)
		commandID := resp.CommandID

		// Wait for the resources to be InProgress.
		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(commands) != 1 {
				return false
			}
			inProgress := 0
			for _, ru := range commands[0].ResourceUpdates {
				if ru.State == resource_update.ResourceUpdateStateInProgress {
					inProgress++
				}
			}
			return inProgress >= 2
		}, 10*time.Second, 50*time.Millisecond, "expected resources to be in progress")

		// A non-force cancel here would never converge (the plugin never returns).
		// Issue it, confirm the command stays non-terminal for a short window.
		_, err = m.CancelCommand(commandID, false, "test-client")
		require.NoError(t, err)

		assert.Never(t, func() bool {
			cmd := loadSingleCommand(t, m.Datastore)
			return cmd.State == forma_command.CommandStateCanceled
		}, 1*time.Second, 100*time.Millisecond, "non-force cancel should NOT converge while resources hang")

		// Now escalate with --force; it must converge to Canceled within a short bound.
		_, err = m.CancelCommand(commandID, true, "test-client")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			cmd := loadSingleCommand(t, m.Datastore)
			return cmd.State == forma_command.CommandStateCanceled
		}, 5*time.Second, 50*time.Millisecond, "force-cancel should converge to Canceled within a short bound")
	})
}

// faultInjectingDatastore wraps a Datastore and fails ForceCancelResourceUpdates a
// configurable number of times before delegating to the real implementation.
type faultInjectingDatastore struct {
	datastore.Datastore
	failuresRemaining atomic.Int32
}

func (d *faultInjectingDatastore) ForceCancelResourceUpdates(commandID string, inProgress []datastore.ForceCancelRow, notStarted []datastore.ResourceUpdateRef, modifiedTs time.Time) (datastore.ForceCancelResult, error) {
	if d.failuresRemaining.Load() > 0 {
		d.failuresRemaining.Add(-1)
		return datastore.ForceCancelResult{}, fmt.Errorf("injected ForceCancelResourceUpdates failure")
	}
	return d.Datastore.ForceCancelResourceUpdates(commandID, inProgress, notStarted, modifiedTs)
}

// TestMetastructure_ForceCancelCommand_PersisterFailureTerminatesNoActors verifies the
// persist-before-terminate contract: when the BulkForceCancel datastore write fails, the
// force-cancel surfaces the error, terminates no actors (the command stays non-terminal),
// and a retry succeeds.
func TestMetastructure_ForceCancelCommand_PersisterFailureTerminatesNoActors(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		base, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		require.NoError(t, err)

		ds := &faultInjectingDatastore{Datastore: base}
		ds.failuresRemaining.Store(1) // fail the first force-cancel, succeed on retry

		m, def, err := test_helpers.NewTestMetastructureWithEverything(t, neverReturningOverrides(), ds, cfg)
		defer def()
		require.NoError(t, err)

		resp, err := m.ApplyForma(twoBucketForma(), &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)
		commandID := resp.CommandID

		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(commands) != 1 {
				return false
			}
			inProgress := 0
			for _, ru := range commands[0].ResourceUpdates {
				if ru.State == resource_update.ResourceUpdateStateInProgress {
					inProgress++
				}
			}
			return inProgress >= 2
		}, 10*time.Second, 50*time.Millisecond, "expected resources to be in progress")

		// First force-cancel: the injected datastore failure must surface as an error.
		_, err = m.CancelCommand(commandID, true, "test-client")
		require.Error(t, err, "force-cancel should surface the persister error")

		// No actors were terminated: the command is still non-terminal (InProgress).
		cmd := loadSingleCommand(t, m.Datastore)
		assert.Equal(t, forma_command.CommandStateInProgress, cmd.State,
			"command must stay InProgress when the force-cancel persist fails (terminate no actors)")

		// Retry succeeds and the command converges to Canceled.
		_, err = m.CancelCommand(commandID, true, "test-client")
		require.NoError(t, err, "retry after a transient persister failure should succeed")

		assert.Eventually(t, func() bool {
			cmd := loadSingleCommand(t, m.Datastore)
			return cmd.State == forma_command.CommandStateCanceled
		}, 5*time.Second, 50*time.Millisecond, "retry should drive the command to Canceled")
	})
}

// TestMetastructure_ForceCancelCommand_WriteFenceDropsLateMessages verifies the monotonic
// terminality write-fence at the workflow level: after a force-cancel commits, a late
// completion message for a force-canceled resource update is dropped — the per-RU state
// stays Canceled and the command stays Canceled.
func TestMetastructure_ForceCancelCommand_WriteFenceDropsLateMessages(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		m, def, err := test_helpers.NewTestMetastructure(t, neverReturningOverrides())
		defer def()
		require.NoError(t, err)

		// Spawn the test helper actor so we can Call the persister directly to inject
		// late messages.
		helperMessages := make(chan any, 16)
		_, err = testutil.StartTestHelperActor(m.Node, helperMessages)
		require.NoError(t, err)

		resp, err := m.ApplyForma(twoBucketForma(), &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client")
		require.NoError(t, err)
		commandID := resp.CommandID

		assert.Eventually(t, func() bool {
			commands, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(commands) != 1 {
				return false
			}
			inProgress := 0
			for _, ru := range commands[0].ResourceUpdates {
				if ru.State == resource_update.ResourceUpdateStateInProgress {
					inProgress++
				}
			}
			return inProgress >= 2
		}, 10*time.Second, 50*time.Millisecond, "expected resources to be in progress")

		_, err = m.CancelCommand(commandID, true, "test-client")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			cmd := loadSingleCommand(t, m.Datastore)
			return cmd.State == forma_command.CommandStateCanceled
		}, 5*time.Second, 50*time.Millisecond, "expected command to reach Canceled")

		cmd := loadSingleCommand(t, m.Datastore)

		// Pick a force-canceled resource update and inject a LATE completion as if its
		// orphaned operator had finished after the force-cancel committed.
		require.NotEmpty(t, cmd.ResourceUpdates)
		victim := cmd.ResourceUpdates[0]

		_, err = testutil.Call(m.Node, "FormaCommandPersister", messages.MarkResourceUpdateAsComplete{
			CommandID:          commandID,
			ResourceURI:        victim.DesiredState.URI(),
			Operation:          victim.Operation,
			FinalState:         types.ResourceUpdateStateSuccess,
			ResourceModifiedTs: time.Now(),
		})
		require.NoError(t, err)

		// Also inject a late progress update.
		_, _ = testutil.Call(m.Node, "FormaCommandPersister", messages.UpdateResourceProgress{
			CommandID:     commandID,
			ResourceURI:   victim.DesiredState.URI(),
			Operation:     victim.Operation,
			ResourceState: types.ResourceUpdateStateInProgress,
			Progress: plugin.TrackedProgress{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.Operation(victim.Operation),
					OperationStatus: resource.OperationStatusSuccess,
				},
			},
		})

		// The late messages must be dropped: the victim stays Canceled and the command
		// stays Canceled.
		cmd = loadSingleCommand(t, m.Datastore)
		assert.Equal(t, forma_command.CommandStateCanceled, cmd.State,
			"command must stay Canceled after late messages")
		for _, ru := range cmd.ResourceUpdates {
			if ru.DesiredState.Ksuid == victim.DesiredState.Ksuid {
				assert.Equal(t, resource_update.ResourceUpdateStateCanceled, ru.State,
					"force-canceled resource must stay Canceled after a late completion")
			}
		}
	})
}
