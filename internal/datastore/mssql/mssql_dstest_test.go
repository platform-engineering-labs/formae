// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package mssql_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// dstestMSSQLBase points at the local SQL Server test container.
// encrypt=disable: the container uses a self-signed cert.
// Each subtest gets a fresh database, dropped in CleanUpFn.
const dstestMSSQLBase = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

func TestDatastore(t *testing.T) {
	probe, err := sql.Open("sqlserver", dstestMSSQLBase+"&database=master")
	if err != nil {
		t.Skipf("mssql driver unavailable: %v", err)
	}
	if err := probe.Ping(); err != nil {
		_ = probe.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}
	_ = probe.Close()

	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()

		dbName := fmt.Sprintf("formae_dstest_%d", time.Now().UnixNano())
		master, err := sql.Open("sqlserver", dstestMSSQLBase+"&database=master")
		if err != nil {
			t.Fatalf("open master: %v", err)
		}
		if _, err := master.Exec(fmt.Sprintf("CREATE DATABASE [%s]", dbName)); err != nil {
			_ = master.Close()
			t.Fatalf("create test db %s: %v", dbName, err)
		}
		_ = master.Close()

		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.MSSQLDatastore,
			MSSQL: pkgmodel.MSSQLConfig{
				Host:             "localhost",
				Port:             1433,
				Database:         dbName,
				AuthMode:         pkgmodel.MSSQLAuthSQL,
				User:             "sa",
				Password:         "Formae_Test_1234!",
				ConnectionParams: "encrypt=disable",
			},
		}
		ds, err := mssql.NewDatastoreMSSQL(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("create mssql datastore: %v", err)
		}

		conn := ds.(*mssql.DatastoreMSSQL).Conn()
		return dstest.TestDatastore{
			Datastore: ds,
			RawInsertResource: func(uri, version, target, operation string) error {
				_, err := conn.Exec(
					"INSERT INTO resources (uri, version, target, operation) VALUES (@p1, @p2, @p3, @p4)",
					uri, version, target, operation,
				)
				return err
			},
			SetTargetHealthStateForTest: func(label, state string) error {
				_, err := conn.Exec(
					`UPDATE targets SET health_state = @p1 WHERE label = @p2 AND version = (SELECT MAX(version) FROM targets WHERE label = @p2)`,
					state, label,
				)
				return err
			},
			CleanUpFn: func() error {
				ds.Close()
				m, err := sql.Open("sqlserver", dstestMSSQLBase+"&database=master")
				if err != nil {
					return err
				}
				defer func() { _ = m.Close() }()
				// Kick remaining sessions so the drop isn't blocked.
				_, _ = m.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
				_, err = m.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
				return err
			},
		}
	})
}
