// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"context"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestAdmissionPrimitive(t *testing.T) {
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
	dstest.RunAdmissionPrimitive(t, first, second, first.admissionStore(), second.admissionStore(), func() datastore.CommandAdmitter { first.Close(); first = open(); return first })
	dstest.RunAdmissionWriters(t, first, second, first.admissionStore())
	dstest.RunAdmissionWriterReviewFixes(t, first, second, first.admissionStore(), second.admissionStore())
}
