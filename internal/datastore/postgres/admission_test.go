// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
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
	dstest.RunExternalChangeHistory(t, first)
	dstest.RunAdmissionPrimitive(t, first, second, first.admissionStore(), second.admissionStore(), func() datastore.CommandAdmitter { first.Close(); first = open(); return first })
	dstest.RunAdmissionWriters(t, first, second, first.admissionStore())
	dstest.RunAdmissionWriterReviewFixes(t, first, second, first.admissionStore(), second.admissionStore())
}

func TestAdmissionCommandLifecycle(t *testing.T) {
	dstest.RunAdmissionCommandLifecycle(t, newAdmissionCommandLifecycleFixture)
}

func TestAdmissionCommandLifecycleProperty(t *testing.T) {
	dstest.RunAdmissionCommandLifecycleProperty(t, newAdmissionCommandLifecycleFixture)
}

func newAdmissionCommandLifecycleFixture(t dstest.AdmissionLifecycleTestingT) dstest.AdmissionCommandLifecycleFixture {
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.PostgresDatastore, Postgres: pkgmodel.PostgresConfig{Host: "localhost", Port: 5432, User: "postgres", Password: "admin", Database: "admission_lifecycle_" + mksuid.New().String()}}
	ds, err := NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
	require.NoError(t, err)
	d := ds.(DatastorePostgres)
	cleanup := func() error {
		d.Close()
		admin, err := pgx.Connect(context.Background(), BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, "postgres"))
		if err != nil {
			return err
		}
		defer admin.Close(context.Background()) //nolint:errcheck
		_, err = admin.Exec(context.Background(), fmt.Sprintf("DROP DATABASE %s", pgx.Identifier{cfg.Postgres.Database}.Sanitize()))
		return err
	}
	closed := false
	closeOnce := func() error {
		if closed {
			return nil
		}
		closed = true
		return cleanup()
	}
	t.Cleanup(func() { require.NoError(t, closeOnce()) })
	_, err = d.Pool().Exec(context.Background(), `
			CREATE TABLE admission_lifecycle_events (seq BIGSERIAL PRIMARY KEY, event TEXT NOT NULL);
			CREATE FUNCTION admission_lifecycle_log() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
				INSERT INTO admission_lifecycle_events(event) VALUES (TG_TABLE_NAME || ':' || lower(TG_OP));
				RETURN NEW;
			END $$;
			CREATE TRIGGER admission_lifecycle_fc BEFORE INSERT OR UPDATE ON forma_commands FOR EACH ROW EXECUTE FUNCTION admission_lifecycle_log();
			CREATE TRIGGER admission_lifecycle_ru BEFORE INSERT OR UPDATE ON resource_updates FOR EACH ROW EXECUTE FUNCTION admission_lifecycle_log();
		`)
	require.NoError(t, err)
	return dstest.AdmissionCommandLifecycleFixture{
		Datastore:      ds,
		AdmissionStore: d.admissionStore(),
		Backend:        "postgres",
		CloseForTest:   closeOnce,
		AdmissionTriggerExistsForTest: func(name string) (bool, error) {
			var exists bool
			err := d.Pool().QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname=$1 AND NOT tgisinternal)`, name).Scan(&exists)
			return exists, err
		},
		DropCommandUpdateTriggerForTest: func() error {
			_, err := d.Pool().Exec(context.Background(), `DROP TRIGGER admission_forma_commands_update ON forma_commands`)
			return err
		},
		ResetWriterEventsForTest: func() error {
			_, err := d.Pool().Exec(context.Background(), `TRUNCATE admission_lifecycle_events RESTART IDENTITY`)
			return err
		},
		WriterEventsForTest: func() ([]string, error) {
			rows, err := d.Pool().Query(context.Background(), `SELECT event FROM admission_lifecycle_events ORDER BY seq`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var events []string
			for rows.Next() {
				var event string
				if err := rows.Scan(&event); err != nil {
					return nil, err
				}
				events = append(events, event)
			}
			return events, rows.Err()
		},
	}
}
