// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package mssql

import (
	"context"
	"database/sql"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestAdmissionPrimitive(t *testing.T) {
	name := "admission_" + mksuid.New().String()
	master, err := sql.Open("sqlserver", "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable&database=master")
	require.NoError(t, err)
	_, err = master.Exec("CREATE DATABASE [" + name + "]")
	require.NoError(t, err)
	defer func() {
		_, _ = master.Exec("ALTER DATABASE [" + name + "] SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		_, _ = master.Exec("DROP DATABASE [" + name + "]")
		_ = master.Close()
	}()
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.MSSQLDatastore, MSSQL: pkgmodel.MSSQLConfig{Host: "localhost", Port: 1433, Database: name, AuthMode: pkgmodel.MSSQLAuthSQL, User: "sa", Password: "Formae_Test_1234!", ConnectionParams: "encrypt=disable"}}
	open := func() *DatastoreMSSQL {
		ds, err := NewDatastoreMSSQL(context.Background(), cfg, "test")
		require.NoError(t, err)
		return ds.(*DatastoreMSSQL)
	}
	first := open()
	defer func() { first.Close() }()
	second := open()
	defer second.Close()
	dstest.RunAdmissionPrimitive(t, first, second, first.admissionStore(), second.admissionStore(), func() datastore.CommandAdmitter { first.Close(); first = open(); return first })
	dstest.RunAdmissionWriters(t, first, second, first.admissionStore())
	dstest.RunAdmissionWriterReviewFixes(t, first, second, first.admissionStore(), second.admissionStore())
}
