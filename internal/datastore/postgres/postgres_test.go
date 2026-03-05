// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestDatastore(t *testing.T) {
	// Verify we can actually connect to postgres with our test credentials
	connStr := "postgres://postgres:admin@localhost:5432/postgres"
	conn, err := pgx.Connect(context.Background(), connStr)
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	conn.Close(context.Background())

	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.PostgresDatastore,
			Postgres: pkgmodel.PostgresConfig{
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "admin",
				Database: fmt.Sprintf("test_%s", mksuid.New().String()),
			},
		}
		ds, err := postgres.NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("Failed to create Postgres datastore: %v", err)
		}
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				if d, ok := ds.(postgres.DatastorePostgres); ok {
					return d.CleanUp()
				}
				return nil
			},
		}
	})
}
