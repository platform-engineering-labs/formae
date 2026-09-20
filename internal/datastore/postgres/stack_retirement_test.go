// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestStackRetirement(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.PostgresDatastore, Postgres: pkgmodel.PostgresConfig{Host: "localhost", Port: 5432, User: "postgres", Password: "admin", Database: "admission_" + mksuid.New().String()}}
	open := func() DatastorePostgres {
		ds, err := NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
		require.NoError(t, err)
		return ds.(DatastorePostgres)
	}
	first := open()
	second := open()
	t.Cleanup(func() {
		second.Close()
		first.Close()
		admin, err := pgx.Connect(context.Background(), BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, "postgres"))
		require.NoError(t, err)
		defer func() { require.NoError(t, admin.Close(context.Background())) }()
		_, err = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE %s", pgx.Identifier{cfg.Postgres.Database}.Sanitize()))
		require.NoError(t, err)
	})
	dstest.RunStackRetirement(t, first, second, first.admissionStore())
}
