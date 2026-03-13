// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	pkgplugin "github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

// TestMetastructure_FinishIncompleteFormaCommands verifies that failed
// commands get picked up and run through completely after a node crash.
func TestMetastructure_FinishIncompleteFormaCommands(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &pkgplugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
					}}, nil
			},
		}
		tmpDir := t.TempDir()
		db_path := tmpDir + "/no-reset-formae.db"
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
		cfg.Agent.Datastore.Sqlite = pkgmodel.SqliteConfig{
			FilePath: db_path,
		}
		db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		if err != nil {
			t.Error(err)
		}

		m, stop, err := test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource1",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "test-stack1",
					Target:     "test-target1",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target1",
					Namespace: "test-namespace1",
				},
			},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")
		assert.NoError(t, err)

		time.Sleep(5 * time.Millisecond)
		fas, err := db.LoadFormaCommands()
		assert.NoError(t, err)
		fas_incomplete, err := db.LoadIncompleteFormaCommands()
		assert.NoError(t, err)

		assert.Equal(t, 1, len(fas))
		assert.Equal(t, 1, len(fas_incomplete))
		// Crash the node
		stop()

		cfg.Agent.Datastore.Sqlite.FilePath = db_path
		db, err = dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		if err != nil {
			t.Error(err)
		}

		fas, err = db.LoadFormaCommands()
		assert.NoError(t, err)
		fas_incomplete, err = db.LoadIncompleteFormaCommands()
		assert.NoError(t, err)

		assert.Equal(t, 1, len(fas))
		assert.Equal(t, 1, len(fas_incomplete))

		m, def, err := test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		assert.Eventually(t, func() bool {
			fas, err = db.LoadFormaCommands()
			if err != nil {
				t.Fatalf("Failed to load forma commands: %v", err)
			}
			fas_incomplete, err = db.LoadIncompleteFormaCommands()
			if err != nil {
				t.Fatalf("Failed to load incomplete forma commands: %v", err)
			}

			return len(fas) == 1 && len(fas_incomplete) == 0 &&
				fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess
		}, 8*time.Second, 100*time.Millisecond, "Forma commands did not finish successfully after restart")
	})
}

// TestMetastructure_FinalizeAllTerminalAfterCrash verifies that when the agent
// crashes after all resource updates completed but before the command itself
// transitioned to a final state, the recovery path correctly finalizes the
// command without spawning a changeset executor.
//
// Scenario:
// 1. Store a command with all resource updates already in terminal states
//    (simulating a crash between last resource completion and command finalization)
// 2. Restart the metastructure, triggering ReRunIncompleteCommands
// 3. Verify the command transitions to the correct final state
func TestMetastructure_FinalizeAllTerminalAfterCrash(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &pkgplugin.ResourcePluginOverrides{}
		tmpDir := t.TempDir()
		dbPath := tmpDir + "/finalize-test.db"
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
		cfg.Agent.Datastore.Sqlite = pkgmodel.SqliteConfig{
			FilePath: dbPath,
		}

		db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		assert.NoError(t, err)

		// Create a command that looks like it was mid-flight when the agent crashed:
		// - Command state: InProgress
		// - All resource updates: terminal (Success/Failed)
		commandID := util.NewID()
		ksuid1 := util.NewID()
		ksuid2 := util.NewID()

		cmd := &forma_command.FormaCommand{
			ID:      commandID,
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateInProgress,
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState: pkgmodel.Resource{
						Label:      "res-success",
						Type:       "FakeAWS::S3::Bucket",
						Stack:      "test-stack",
						Target:     "test-target",
						Ksuid:      ksuid1,
						Properties: json.RawMessage(`{"name":"res-success"}`),
					},
					Operation: resource_update.OperationCreate,
					State:     resource_update.ResourceUpdateStateSuccess,
				},
				{
					DesiredState: pkgmodel.Resource{
						Label:      "res-failed",
						Type:       "FakeAWS::S3::Bucket",
						Stack:      "test-stack",
						Target:     "test-target",
						Ksuid:      ksuid2,
						Properties: json.RawMessage(`{"name":"res-failed"}`),
					},
					Operation: resource_update.OperationCreate,
					State:     resource_update.ResourceUpdateStateFailed,
				},
			},
		}

		// Store the command directly in the DB to simulate post-crash state
		err = db.StoreFormaCommand(cmd, commandID)
		assert.NoError(t, err)

		// Verify the command is incomplete
		incomplete, err := db.LoadIncompleteFormaCommands()
		assert.NoError(t, err)
		assert.Equal(t, 1, len(incomplete))

		// Start a new metastructure — this triggers ReRunIncompleteCommands
		cfg.Agent.Datastore.Sqlite.FilePath = dbPath
		db, err = dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		assert.NoError(t, err)

		_, def, err := test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
		defer def()
		assert.NoError(t, err)

		// The command should be finalized as Failed (one Success + one Failed = Failed)
		assert.Eventually(t, func() bool {
			incomplete, err = db.LoadIncompleteFormaCommands()
			if err != nil {
				return false
			}
			if len(incomplete) != 0 {
				return false
			}
			commands, err := db.LoadFormaCommands()
			if err != nil {
				return false
			}
			return len(commands) == 1 && commands[0].State == forma_command.CommandStateFailed
		}, 5*time.Second, 50*time.Millisecond, "Command should be finalized as Failed after crash recovery")
	})
}

// TestMetastructure_CascadedFailuresNotReExecutedAfterCrash verifies that
// cascade-failed resources (which have terminal state in DB but empty
// ProgressResult) are not re-included in the recovery changeset after a crash.
//
// This prevents the pendingCompletions underflow bug where:
// 1. Resource B depends on A, A fails → B is cascade-marked as Failed
// 2. Agent crashes
// 3. On restart, B has state=Failed but empty ProgressResult
// 4. Without the fix, UpdateState() would see empty progress → NotStarted
// 5. Recovery changeset includes B, FormaCommandPersister counts B as already
//    terminal → pendingCompletions too low → underflow on B's completion
func TestMetastructure_CascadedFailuresNotReExecutedAfterCrash(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		createCallCount := 0
		overrides := &pkgplugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				createCallCount++
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           "native-" + request.Label,
						ResourceProperties: json.RawMessage(`{"name":"` + request.Label + `"}`),
					},
				}, nil
			},
		}
		tmpDir := t.TempDir()
		dbPath := tmpDir + "/cascade-test.db"
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
		cfg.Agent.Datastore.Sqlite = pkgmodel.SqliteConfig{
			FilePath: dbPath,
		}

		db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		assert.NoError(t, err)

		// Simulate post-crash state:
		// - Parent resource A: Failed (has ProgressResult showing failure)
		// - Child resource B: Failed (cascade-marked, EMPTY ProgressResult)
		// - Child resource C: NotStarted (never started, should be re-executed)
		commandID := util.NewID()
		ksuidA := util.NewID()
		ksuidB := util.NewID()
		ksuidC := util.NewID()

		cmd := &forma_command.FormaCommand{
			ID:      commandID,
			Command: pkgmodel.CommandApply,
			State:   forma_command.CommandStateInProgress,
			ResourceUpdates: []resource_update.ResourceUpdate{
				{
					DesiredState: pkgmodel.Resource{
						Label:      "parent-a",
						Type:       "FakeAWS::S3::Bucket",
						Stack:      "test-stack",
						Target:     "test-target",
						Ksuid:      ksuidA,
						Properties: json.RawMessage(`{"name":"parent-a"}`),
					},
					ResourceTarget: pkgmodel.Target{
						Label:     "test-target",
						Namespace: "FakeAWS",
						Config:    json.RawMessage(`{}`),
					},
					Operation: resource_update.OperationCreate,
					State:     resource_update.ResourceUpdateStateFailed,
					ProgressResult: []pkgplugin.TrackedProgress{
						{ProgressResult: resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
						}},
					},
				},
				{
					DesiredState: pkgmodel.Resource{
						Label:      "child-b",
						Type:       "FakeAWS::S3::Bucket",
						Stack:      "test-stack",
						Target:     "test-target",
						Ksuid:      ksuidB,
						Properties: json.RawMessage(`{"name":"child-b"}`),
					},
					ResourceTarget: pkgmodel.Target{
						Label:     "test-target",
						Namespace: "FakeAWS",
						Config:    json.RawMessage(`{}`),
					},
					Operation: resource_update.OperationCreate,
					State:     resource_update.ResourceUpdateStateFailed,
					// Empty ProgressResult — cascade-marked, never executed
				},
				{
					DesiredState: pkgmodel.Resource{
						Label:      "independent-c",
						Type:       "FakeAWS::S3::Bucket",
						Stack:      "test-stack",
						Target:     "test-target",
						Ksuid:      ksuidC,
						Properties: json.RawMessage(`{"name":"independent-c"}`),
					},
					ResourceTarget: pkgmodel.Target{
						Label:     "test-target",
						Namespace: "FakeAWS",
						Config:    json.RawMessage(`{}`),
					},
					Operation: resource_update.OperationCreate,
					State:     resource_update.ResourceUpdateStateNotStarted,
				},
			},
		}

		err = db.StoreFormaCommand(cmd, commandID)
		assert.NoError(t, err)

		// Start metastructure — triggers ReRunIncompleteCommands
		cfg.Agent.Datastore.Sqlite.FilePath = dbPath
		db, err = dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		assert.NoError(t, err)

		_, def, err := test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
		defer def()
		assert.NoError(t, err)

		// The command should eventually complete:
		// - A: Failed (unchanged, was already terminal)
		// - B: Failed (unchanged, cascade failure respected)
		// - C: Success (re-executed by recovery changeset)
		assert.Eventually(t, func() bool {
			incomplete, err := db.LoadIncompleteFormaCommands()
			if err != nil || len(incomplete) != 0 {
				return false
			}
			commands, err := db.LoadFormaCommands()
			if err != nil || len(commands) != 1 {
				return false
			}
			cmd := commands[0]
			if cmd.State != forma_command.CommandStateFailed {
				return false
			}
			// Verify resource states
			for _, ru := range cmd.ResourceUpdates {
				switch ru.DesiredState.Label {
				case "parent-a", "child-b":
					if ru.State != resource_update.ResourceUpdateStateFailed {
						return false
					}
				case "independent-c":
					if ru.State != resource_update.ResourceUpdateStateSuccess {
						return false
					}
				}
			}
			return true
		}, 8*time.Second, 50*time.Millisecond, "Command should complete with A=Failed, B=Failed, C=Success")

		// Only resource C should have been executed (1 Create call)
		assert.Equal(t, 1, createCallCount, "Only independent-c should have been created")
	})
}
