// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore/aurora"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func TestDatastore(t *testing.T) {
	clusterArn := os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN")
	secretArn := os.Getenv("FORMAE_TEST_AURORA_SECRET_ARN")
	if clusterArn == "" || secretArn == "" {
		t.Skip("Aurora env vars not set, skipping")
	}

	auroraDatabase := os.Getenv("FORMAE_TEST_AURORA_DATABASE")
	if auroraDatabase == "" {
		auroraDatabase = "formae"
	}

	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.AuroraDataAPIDatastore,
			AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{
				ClusterARN: clusterArn,
				SecretARN:  secretArn,
				Database:   auroraDatabase,
				Region:     os.Getenv("FORMAE_TEST_AURORA_REGION"),
				Endpoint:   os.Getenv("FORMAE_TEST_AURORA_ENDPOINT"),
			},
		}
		ds, err := aurora.NewDatastoreAuroraDataAPI(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("Failed to create Aurora datastore: %v", err)
		}
		// Clean up any stale data from previous test runs
		if d, ok := ds.(*aurora.DatastoreAuroraDataAPI); ok {
			if err := d.CleanUp(); err != nil {
				slog.Warn("Failed to clean up datastore", "error", err)
			}
		}
		d, _ := ds.(*aurora.DatastoreAuroraDataAPI)
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				return d.CleanUp()
			},
			SetTargetHealthStateForTest: func(label, state string) error {
				return d.SetHealthStateForTesting(label, state)
			},
		}
	})
}

// TestBulkStoreResourceUpdates_ConflictUpdatesPayloadColumns verifies that
// re-storing a resource_updates row (a conflict on the command_id/ksuid/operation
// primary key) overwrites every payload column with the incoming values,
// matching the SQLite (INSERT OR REPLACE) and Postgres (ON CONFLICT DO UPDATE SET
// listing every column) behavior. Requires a local RDS Data API emulator; see
// `make local-data-api-up` and `make test-unit-auroradataapi`.
func TestBulkStoreResourceUpdates_ConflictUpdatesPayloadColumns(t *testing.T) {
	clusterArn := os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN")
	secretArn := os.Getenv("FORMAE_TEST_AURORA_SECRET_ARN")
	if clusterArn == "" || secretArn == "" {
		t.Skip("Aurora env vars not set, skipping. Verify via make local-data-api-up && make test-unit-auroradataapi")
	}

	auroraDatabase := os.Getenv("FORMAE_TEST_AURORA_DATABASE")
	if auroraDatabase == "" {
		auroraDatabase = "formae"
	}

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.AuroraDataAPIDatastore,
		AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{
			ClusterARN: clusterArn,
			SecretARN:  secretArn,
			Database:   auroraDatabase,
			Region:     os.Getenv("FORMAE_TEST_AURORA_REGION"),
			Endpoint:   os.Getenv("FORMAE_TEST_AURORA_ENDPOINT"),
		},
	}
	ds, err := aurora.NewDatastoreAuroraDataAPI(context.Background(), cfg, "test")
	if err != nil {
		t.Fatalf("Failed to create Aurora datastore: %v", err)
	}
	d, ok := ds.(*aurora.DatastoreAuroraDataAPI)
	if !ok {
		t.Fatalf("expected *aurora.DatastoreAuroraDataAPI, got %T", ds)
	}
	if err := d.CleanUp(); err != nil {
		slog.Warn("Failed to clean up datastore before test", "error", err)
	}
	t.Cleanup(func() {
		if err := d.CleanUp(); err != nil {
			slog.Warn("Failed to clean up datastore after test", "error", err)
		}
	})

	commandID := "cmd-conflict-test"
	ksuid := "ksuid-conflict-test"
	now := time.Now().UTC().Truncate(time.Second)

	original := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Ksuid:      ksuid,
			Label:      "original-label",
			Properties: json.RawMessage(`{"plaintext":"secret-value"}`),
		},
		PriorState: pkgmodel.Resource{
			Ksuid:      ksuid,
			Properties: json.RawMessage(`{"plaintext":"prior-secret-value"}`),
		},
		Operation:                resource_update.OperationCreate,
		State:                    resource_update.ResourceUpdateStateInProgress,
		StartTs:                  now,
		ModifiedTs:               now,
		PreviousProperties:       json.RawMessage(`{"plaintext":"previous-secret-value"}`),
		ProgressResult:           []plugin.TrackedProgress{{ResourceType: "original-type"}},
		MostRecentProgressResult: plugin.TrackedProgress{ResourceType: "original-type"},
	}

	if err := d.BulkStoreResourceUpdates(commandID, []resource_update.ResourceUpdate{original}); err != nil {
		t.Fatalf("initial BulkStoreResourceUpdates failed: %v", err)
	}

	// Re-store the same (command_id, ksuid, operation) with hashed replacement
	// values, simulating a re-store that must overwrite plaintext payload
	// columns left over from the original insert.
	hashed := original
	hashed.DesiredState.Properties = json.RawMessage(`{"plaintext":"HASHED"}`)
	hashed.PriorState.Properties = json.RawMessage(`{"plaintext":"HASHED"}`)
	hashed.PreviousProperties = json.RawMessage(`{"plaintext":"HASHED"}`)
	hashed.ProgressResult = []plugin.TrackedProgress{{ResourceType: "HASHED-type"}}
	hashed.MostRecentProgressResult = plugin.TrackedProgress{ResourceType: "HASHED-type"}
	hashed.ModifiedTs = now.Add(time.Minute)

	if err := d.BulkStoreResourceUpdates(commandID, []resource_update.ResourceUpdate{hashed}); err != nil {
		t.Fatalf("conflicting BulkStoreResourceUpdates failed: %v", err)
	}

	loaded, err := d.LoadResourceUpdates(commandID)
	if err != nil {
		t.Fatalf("LoadResourceUpdates failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected exactly 1 resource update row (conflict should update in place, not duplicate), got %d", len(loaded))
	}

	got := loaded[0]

	if string(got.DesiredState.Properties) != string(hashed.DesiredState.Properties) {
		t.Errorf("resource column not updated on conflict: got %s, want %s", got.DesiredState.Properties, hashed.DesiredState.Properties)
	}
	if string(got.PriorState.Properties) != string(hashed.PriorState.Properties) {
		t.Errorf("existing_resource column not updated on conflict: got %s, want %s", got.PriorState.Properties, hashed.PriorState.Properties)
	}
	if string(got.PreviousProperties) != string(hashed.PreviousProperties) {
		t.Errorf("previous_properties column not updated on conflict: got %s, want %s", got.PreviousProperties, hashed.PreviousProperties)
	}
	if len(got.ProgressResult) != 1 || got.ProgressResult[0].ResourceType != "HASHED-type" {
		t.Errorf("progress_result column not updated on conflict: got %+v", got.ProgressResult)
	}
	if got.MostRecentProgressResult.ResourceType != "HASHED-type" {
		t.Errorf("most_recent_progress column not updated on conflict: got %+v", got.MostRecentProgressResult)
	}
	if !got.ModifiedTs.Equal(hashed.ModifiedTs) {
		t.Errorf("modified_ts column not updated on conflict: got %v, want %v", got.ModifiedTs, hashed.ModifiedTs)
	}
}
