// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestAdmissionCommandLifecycle(t *testing.T) {
	dstest.RunAdmissionCommandLifecycle(t, func(t *testing.T) dstest.AdmissionCommandLifecycleFixture {
		cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.SqliteDatastore, Sqlite: pkgmodel.SqliteConfig{FilePath: filepath.Join(t.TempDir(), "lifecycle.db")}}
		ds, err := NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		d := ds.(DatastoreSQLite)
		t.Cleanup(d.Close)
		_, err = d.Conn().Exec(`
			CREATE TABLE admission_lifecycle_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, event TEXT NOT NULL);
			CREATE TRIGGER admission_lifecycle_fc_insert BEFORE INSERT ON forma_commands BEGIN INSERT INTO admission_lifecycle_events(event) VALUES ('forma_commands:insert'); END;
			CREATE TRIGGER admission_lifecycle_fc_update BEFORE UPDATE ON forma_commands BEGIN INSERT INTO admission_lifecycle_events(event) VALUES ('forma_commands:update'); END;
			CREATE TRIGGER admission_lifecycle_ru_insert BEFORE INSERT ON resource_updates BEGIN INSERT INTO admission_lifecycle_events(event) VALUES ('resource_updates:insert'); END;
			CREATE TRIGGER admission_lifecycle_ru_update BEFORE UPDATE ON resource_updates BEGIN INSERT INTO admission_lifecycle_events(event) VALUES ('resource_updates:update'); END;
		`)
		require.NoError(t, err)
		return dstest.AdmissionCommandLifecycleFixture{
			Datastore: ds,
			Backend:   "sqlite",
			AdmissionTriggerExistsForTest: func(name string) (bool, error) {
				var count int
				err := d.Conn().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&count)
				return count == 1, err
			},
			DropCommandUpdateTriggerForTest: func() error {
				_, err := d.Conn().Exec(`DROP TRIGGER admission_forma_commands_update`)
				return err
			},
			ResetWriterEventsForTest: func() error {
				_, err := d.Conn().Exec(`DELETE FROM admission_lifecycle_events`)
				return err
			},
			WriterEventsForTest: func() ([]string, error) {
				rows, err := d.Conn().Query(`SELECT event FROM admission_lifecycle_events ORDER BY seq`)
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
	})
}

func TestAdmissionPrimitive(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.SqliteDatastore, Sqlite: pkgmodel.SqliteConfig{FilePath: filepath.Join(t.TempDir(), "admission.db")}}
	open := func() DatastoreSQLite {
		ds, err := NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		return ds.(DatastoreSQLite)
	}
	first := open()
	defer func() { first.Close() }()
	second := open()
	defer second.Close()
	dstest.RunExternalChangeHistory(t, first)
	dstest.RunAdmissionPrimitive(t, first, second, first.admissionStore(), second.admissionStore(), func() datastore.CommandAdmitter { first.Close(); first = open(); return first })
	dstest.RunAdmissionWriters(t, first, second, first.admissionStore())
	dstest.RunAdmissionWriterReviewFixes(t, first, second, first.admissionStore(), second.admissionStore())
}
