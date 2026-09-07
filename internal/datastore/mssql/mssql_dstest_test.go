// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package mssql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore"
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
			LoadAgentBootsForTest: func() ([]datastore.AgentBoot, error) {
				rows, err := conn.Query(`SELECT boot_id, version, booted_at FROM agent_boots ORDER BY booted_at, boot_id`)
				if err != nil {
					return nil, err
				}
				defer rows.Close() //nolint:errcheck
				var out []datastore.AgentBoot
				for rows.Next() {
					var b datastore.AgentBoot
					if err := rows.Scan(&b.BootID, &b.Version, &b.BootedAt); err != nil {
						return nil, err
					}
					out = append(out, b)
				}
				return out, rows.Err()
			},
			SetTargetHealthStateForTest: func(label, state string) error {
				_, err := conn.Exec(
					`UPDATE targets SET health_state = @p1 WHERE label = @p2 AND version = (SELECT MAX(version) FROM targets WHERE label = @p2)`,
					state, label,
				)
				return err
			},
			SetStackValidFromForTest: func(label string, validFrom []time.Time) error {
				rows, err := conn.Query(
					`SELECT version FROM stacks WHERE label = @p1 ORDER BY version COLLATE Latin1_General_BIN2 ASC`, label)
				if err != nil {
					return err
				}
				var versions []string
				for rows.Next() {
					var version string
					if err := rows.Scan(&version); err != nil {
						_ = rows.Close()
						return err
					}
					versions = append(versions, version)
				}
				if err := rows.Err(); err != nil {
					_ = rows.Close()
					return err
				}
				if err := rows.Close(); err != nil {
					return err
				}
				if len(versions) != len(validFrom) {
					return fmt.Errorf("stack %q has %d versions, got %d timestamps", label, len(versions), len(validFrom))
				}
				for i, version := range versions {
					if _, err := conn.Exec(
						`UPDATE stacks SET valid_from = @p1 WHERE label = @p2 AND version = @p3`,
						validFrom[i].UTC(), label, version,
					); err != nil {
						return err
					}
				}
				return nil
			},
			SetPolicyDataForTest: func(label, policyData string) error {
				_, err := conn.Exec(
					`UPDATE policies SET policy_data = @p1 WHERE label = @p2 AND version = (SELECT MAX(version) FROM policies WHERE label = @p2)`,
					policyData, label,
				)
				return err
			},
			NullResourceUpdateModifiedTsForTest: func(ksuid string) error {
				_, err := conn.Exec(
					`UPDATE resource_updates SET modified_ts = NULL WHERE ksuid = @p1`, ksuid,
				)
				return err
			},
			NullFormaCommandSubjectForTest: func(commandID string) error {
				_, err := conn.Exec(
					`UPDATE forma_commands SET subject = NULL, subject_name = NULL WHERE command_id = @p1`, commandID,
				)
				return err
			},
			GeneratorIDForTest: func(label, stackLabel string) (string, error) {
				var id string
				// generators.stack_id stores the stack's KSUID, not its label, so
				// the stack is resolved by label first (its own current row), the
				// same way the datastore's own Get/DeleteGenerator do.
				err := conn.QueryRow(
					`SELECT TOP (1) g.id FROM generators g
					 JOIN (SELECT TOP (1) id FROM stacks WHERE label = @p1 ORDER BY version COLLATE Latin1_General_BIN2 DESC) s ON g.stack_id = s.id
					 WHERE g.label = @p2
					 ORDER BY g.version COLLATE Latin1_General_BIN2 DESC`,
					stackLabel, label,
				).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					return "", nil
				}
				return id, err
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
