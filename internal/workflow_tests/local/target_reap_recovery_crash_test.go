// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	pkgplugin "github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestApplyForma_RecoveryReAdoptSurvivesCrash verifies the durability of the
// recovery re-stamp: if the agent crashes after the recover target update is
// terminal (fresh incarnation minted, resource rows un-reaped) but before the
// recovery command's resource re-adopts run, a restart resumes the command from
// the persisted resource_updates rows and the re-adopts still persist — they are
// NOT rejected by the resource-write guard. Durability rests on the un-reap being
// committed with the target update and the incarnation not being persisted with
// the resource update, so the resumed write carries no stale expectation.
func TestApplyForma_RecoveryReAdoptSurvivesCrash(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var recoveryPhase atomic.Bool

		// Phase 1 (initial apply): Create succeeds so the resource exists and can
		// be reaped. Phase 2 (recovery apply): Create hangs as InProgress so the
		// resource re-adopt never persists before we crash — but the recover target
		// update (which runs first, in dependency order) does complete and commit.
		overrides1 := &pkgplugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if recoveryPhase.Load() {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusInProgress,
						},
					}, nil
				}
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        "native-" + request.Label,
					},
				}, nil
			},
			Status: func(request *resource.StatusRequest) (*resource.StatusResult, error) {
				return &resource.StatusResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusInProgress,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"v1"}`,
				}, nil
			},
		}

		tmpDir := t.TempDir()
		// "no-reset" in the path keeps the test-helper cleanup from deleting the DB
		// file on stop(), so the restart can reopen the same database.
		dbPath := tmpDir + "/no-reset-reap-recovery-crash.db"
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
		cfg.Agent.Datastore.Sqlite = pkgmodel.SqliteConfig{FilePath: dbPath}

		db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		require.NoError(t, err)

		m, stop, err := test_helpers.NewTestMetastructureWithEverything(t, overrides1, db, cfg)
		require.NoError(t, err)

		r := require.New(t)

		schema := pkgmodel.Schema{Fields: []string{"foo"}}
		v1 := json.RawMessage(`{"foo":"v1"}`)
		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "crash-stack"}},
			Resources: []pkgmodel.Resource{
				{Label: "res1", Type: "FakeAWS::Resource", Properties: v1, Schema: schema, Stack: "crash-stack", Target: "crash-target"},
			},
			Targets: []pkgmodel.Target{{Label: "crash-target"}},
		}
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err)

		r.Eventually(func() bool {
			resources, err := db.LoadResourcesByStack("crash-stack")
			return err == nil && len(resources) == 1
		}, 15*time.Second, 200*time.Millisecond, "initial apply should create the resource")

		before, err := db.LoadTarget("crash-target")
		r.NoError(err)
		oldIncarnation := before.Health.IncarnationID
		r.NotEmpty(oldIncarnation)

		reapTargetForTest(t, db, "crash-target")

		// Enter the recovery phase and re-apply the same forma. The recover target
		// update runs first and commits (fresh incarnation + un-reaped resources);
		// the resource re-adopt hangs as InProgress.
		recoveryPhase.Store(true)
		_, err = m.ApplyForma(f, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client")
		r.NoError(err, "recovery re-apply must be admitted")

		// Wait until the recover target update has committed: fresh incarnation and
		// no reaped tombstone remaining (the un-reap is durable, part of the target
		// update).
		r.Eventually(func() bool {
			target, err := db.LoadTarget("crash-target")
			if err != nil || target == nil || target.Health == nil {
				return false
			}
			if target.Health.IncarnationID == oldIncarnation || target.Health.State == pkgmodel.TargetHealthStateReaped {
				return false
			}
			reaped, err := db.LoadReapedResources()
			return err == nil && len(reaped) == 0
		}, 15*time.Second, 200*time.Millisecond, "recover target update must commit before the crash")

		// The recovery command is still incomplete (the re-adopt has not run).
		incomplete, err := db.LoadIncompleteFormaCommands()
		r.NoError(err)
		r.NotEmpty(incomplete, "the recovery command must still be in flight at crash time")

		// Crash.
		stop()

		// Restart against the same DB with a Create that succeeds, so the resumed
		// re-adopt can complete.
		overrides2 := &pkgplugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						NativeID:        "native-" + request.Label,
					},
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"foo":"v1"}`,
				}, nil
			},
		}

		db2, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
		r.NoError(err)

		// The un-reap committed with the recover target update, so it survives the
		// crash: the resource is live again, no tombstone remains, and the recovery
		// command is still incomplete with its re-adopt pending.
		preLive, err := db2.LoadResourcesByStack("crash-stack")
		r.NoError(err)
		r.Len(preLive, 1, "the un-reap must survive the crash (durable with the target update)")
		preReaped, err := db2.LoadReapedResources()
		r.NoError(err)
		r.Empty(preReaped, "no tombstone should remain after the crash")

		_, def, err := test_helpers.NewTestMetastructureWithEverything(t, overrides2, db2, cfg)
		defer def()
		r.NoError(err)

		// ReRunIncompleteCommands resumes the recovery command; the re-adopt persists
		// under the fresh incarnation (NOT rejected by the guard), and the command
		// drains to a terminal state.
		r.Eventually(func() bool {
			resources, err := db2.LoadResourcesByStack("crash-stack")
			if err != nil || len(resources) != 1 {
				return false
			}
			incomplete, err := db2.LoadIncompleteFormaCommands()
			return err == nil && len(incomplete) == 0
		}, 20*time.Second, 200*time.Millisecond, "resume must persist the re-adopt and finish the command")

		reaped, err := db2.LoadReapedResources()
		r.NoError(err)
		r.Empty(reaped, "no reaped tombstone should remain after crash recovery")
	})
}
