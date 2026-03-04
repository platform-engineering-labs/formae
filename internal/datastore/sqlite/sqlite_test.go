// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite_test

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestDatastore(t *testing.T) {
	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.SqliteDatastore,
			Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
		}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("Failed to create SQLite datastore: %v", err)
		}
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				if d, ok := ds.(dssqlite.DatastoreSQLite); ok {
					return d.CleanUp()
				}
				return nil
			},
		}
	})
}
