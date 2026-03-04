// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore/aurora"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				if d, ok := ds.(*aurora.DatastoreAuroraDataAPI); ok {
					return d.CleanUp()
				}
				return nil
			},
		}
	})
}
