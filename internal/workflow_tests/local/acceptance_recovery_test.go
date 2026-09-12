// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestAcceptanceRecoveryNoProviderWorkAndPendingTarget(t *testing.T) {
	for _, withTarget := range []bool{false, true} {
		name := "pure"
		if withTarget {
			name = "pending-target"
		}
		t.Run(name, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				cfg := test_helpers.NewTestMetastructureConfig()
				cfg.Agent.Datastore.DatastoreType = pkgmodel.SqliteDatastore
				cfg.Agent.Datastore.Sqlite.FilePath = t.TempDir() + "/acceptance.db"
				db, err := dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
				require.NoError(t, err)
				command := &forma_command.FormaCommand{Setup: &forma_command.SetupBoundary{Version: 1}, ID: util.NewID(), Command: pkgmodel.CommandApply, State: forma_command.CommandStateInProgress, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: pkgmodel.Resource{Ksuid: util.NewID(), Label: "accepted", Stack: "test-stack", Target: "test-target", Type: "FakeAWS::S3::Bucket", Properties: json.RawMessage(`{"name":"accepted"}`)}, Operation: resource_update.OperationAccept, State: resource_update.ResourceUpdateStateSuccess, Version: "reviewed"}}}
				if withTarget {
					command.TargetUpdates = []target_update.TargetUpdate{{Target: pkgmodel.Target{Label: "test-target", Namespace: "FakeAWS", Config: json.RawMessage(`{}`)}, Operation: target_update.TargetOperationCreate, State: target_update.TargetUpdateStateNotStarted}}
				}
				require.NoError(t, db.StoreFormaCommand(command, command.ID))
				db.Close()
				db, err = dssqlite.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
				require.NoError(t, err)
				var calls atomic.Int64
				overrides := &plugin.ResourcePluginOverrides{
					Create: func(*resource.CreateRequest) (*resource.CreateResult, error) { calls.Add(1); return nil, nil },
					Read:   func(*resource.ReadRequest) (*resource.ReadResult, error) { calls.Add(1); return nil, nil },
					Update: func(*resource.UpdateRequest) (*resource.UpdateResult, error) { calls.Add(1); return nil, nil },
					Delete: func(*resource.DeleteRequest) (*resource.DeleteResult, error) { calls.Add(1); return nil, nil },
				}
				_, stop, err := test_helpers.NewTestMetastructureWithEverything(t, overrides, db, cfg)
				require.NoError(t, err)
				defer stop()
				require.Eventually(t, func() bool {
					got, err := db.GetFormaCommandByCommandID(command.ID)
					return err == nil && got.State == forma_command.CommandStateSuccess
				}, 5*time.Second, 25*time.Millisecond)
				got, err := db.GetFormaCommandByCommandID(command.ID)
				require.NoError(t, err)
				require.Equal(t, "reviewed", got.ResourceUpdates[0].Version)
				require.Equal(t, resource_update.ResourceUpdateStateSuccess, got.ResourceUpdates[0].State)
				inventory, err := db.LoadAllResourcesByStack()
				require.NoError(t, err)
				require.Empty(t, inventory)
				require.Zero(t, calls.Load(), "acceptance must not call any provider operation")
				if withTarget {
					require.Equal(t, target_update.TargetUpdateStateSuccess, got.TargetUpdates[0].State)
				}
			})
		})
	}
}
