// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

// TestMetastructure_FinishIncompleteFormaCommands verifies that failed
// commands get picked up and run through completely after a node crash.
func TestMetastructure_FinishIncompleteFormaCommands(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
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
		db, err := datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
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
		db, err = datastore.NewDatastoreSQLite(context.Background(), &cfg.Agent.Datastore, "test")
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
