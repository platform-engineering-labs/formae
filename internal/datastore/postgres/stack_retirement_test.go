// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/demula/mksuid/v2"
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
	defer func() { _ = first.CleanUp() }()
	defer func() { first.Close() }()
	second := open()
	defer second.Close()
	dstest.RunStackRetirement(t, first, second, first.admissionStore())
}
