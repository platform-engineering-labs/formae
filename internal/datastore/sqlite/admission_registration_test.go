//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

var registrationTables = []struct {
	name          string
	resolveStore  func(datastore.AdmissionStore, []string) ([]string, error)
	resolvePublic func(DatastoreSQLite, []string) ([]string, error)
}{
	{"admission_stack_labels", datastore.AdmissionStore.ResolveAdmissionStackGuards, DatastoreSQLite.ResolveAdmissionStackGuards},
	{"admission_inventory_targets", datastore.AdmissionStore.ResolveAdmissionTargetInventoryGuards, DatastoreSQLite.ResolveAdmissionTargetInventoryGuards},
	{"admission_resource_ids", datastore.AdmissionStore.ResolveAdmissionResourceIdentityGuards, DatastoreSQLite.ResolveAdmissionResourceIdentityGuards},
}

func registrationDatastores(t *testing.T) (DatastoreSQLite, DatastoreSQLite) {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.SqliteDatastore, Sqlite: pkgmodel.SqliteConfig{FilePath: filepath.Join(t.TempDir(), "registration.db")}}
	open := func() DatastoreSQLite {
		ds, err := NewDatastoreSQLite(context.Background(), cfg, "test")
		require.NoError(t, err)
		d := ds.(DatastoreSQLite)
		t.Cleanup(d.Close)
		return d
	}
	return open(), open()
}

type registrationResult struct {
	keys []string
	err  error
}

func registrationReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(15 * time.Second):
		t.Fatal("registration did not complete")
		var zero T
		return zero
	}
}

// Gate a real transaction only after its first absent read. Both connections
// therefore hold the old snapshot before either registration may proceed.
type registrationReadGate struct {
	datastore.AdmissionTransaction
	once    *sync.Once
	read    chan<- struct{}
	release <-chan struct{}
}

func (g registrationReadGate) Query(q string, args ...any) ([]string, error) {
	row, err := g.AdmissionTransaction.Query(q, args...)
	if err == nil && len(row) == 0 {
		g.once.Do(func() { g.read <- struct{}{}; <-g.release })
	}
	return row, err
}

func TestAdmissionRegistrationRestartsStaleReaders(t *testing.T) {
	for _, table := range registrationTables {
		t.Run(table.name, func(t *testing.T) {
			first, second := registrationDatastores(t)
			established, err := table.resolvePublic(first, []string{"a-established"})
			require.NoError(t, err)
			reads := make(chan struct{}, 2)
			releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
			done := []chan registrationResult{make(chan registrationResult, 1), make(chan registrationResult, 1)}
			var releaseOnce [2]sync.Once
			release := func(i int) { releaseOnce[i].Do(func() { close(releases[i]) }) }
			defer release(0)
			defer release(1)
			for i, ds := range []DatastoreSQLite{first, second} {
				store := ds.admissionStore()
				begin := store.Begin
				var once sync.Once
				store.Begin = func() (datastore.AdmissionTransaction, error) {
					tx, err := begin()
					if err != nil {
						return nil, err
					}
					return registrationReadGate{tx, &once, reads, releases[i]}, nil
				}
				go func() {
					keys, err := table.resolveStore(store, []string{"new-identity", "a-established", "new-identity"})
					done[i] <- registrationResult{keys, err}
				}()
			}
			registrationReceive(t, reads)
			registrationReceive(t, reads)
			release(0)
			x := registrationReceive(t, done[0])
			release(1)
			y := registrationReceive(t, done[1])
			require.NoError(t, x.err)
			require.NoError(t, y.err)
			require.Len(t, x.keys, 2)
			require.Contains(t, x.keys, established[0])
			require.Equal(t, x.keys, y.keys)
		})
	}
}

func TestAdmissionRegistrationReleasesFailedWriterIntent(t *testing.T) {
	first, second := registrationDatastores(t)
	_, err := first.conn.Exec("PRAGMA query_only=ON")
	require.NoError(t, err)
	keys, err := first.ResolveAdmissionStackGuards([]string{"new-identity"})
	require.ErrorContains(t, err, "readonly")
	require.Nil(t, keys)
	// These operations require the failed transaction's connection and lock to
	// have been released. The original SQL error must remain visible above.
	_, err = first.conn.Exec("PRAGMA query_only=OFF")
	require.NoError(t, err)
	keys, err = second.ResolveAdmissionStackGuards([]string{"new-identity"})
	require.NoError(t, err)
	again, err := first.ResolveAdmissionStackGuards([]string{"new-identity"})
	require.NoError(t, err)
	require.Equal(t, keys, again)
}

func TestAdmissionRegistrationPublicResolvers(t *testing.T) {
	for _, table := range registrationTables {
		t.Run(table.name, func(t *testing.T) {
			first, second := registrationDatastores(t)
			start, done := make(chan struct{}), make(chan registrationResult, 2)
			for _, ds := range []DatastoreSQLite{first, second} {
				go func() {
					<-start
					keys, err := table.resolvePublic(ds, []string{"z-last", "a-first", "a-first"})
					done <- registrationResult{keys, err}
				}()
			}
			close(start)
			x, y := registrationReceive(t, done), registrationReceive(t, done)
			require.NoError(t, x.err)
			require.NoError(t, y.err)
			require.Len(t, x.keys, 2)
			require.IsIncreasing(t, x.keys)
			require.Equal(t, x.keys, y.keys)

			// Established identities remain readable while another connection owns
			// the writer lock, including duplicate inputs and an empty batch.
			held, err := second.admissionStore().Begin()
			require.NoError(t, err)
			defer func(cleanup func() error) { _ = cleanup() }(held.Rollback)
			require.NoError(t, held.Exec("INSERT INTO "+table.name+"(label) VALUES (?)", "uncommitted"))
			go func() {
				keys, err := table.resolvePublic(first, []string{"a-first", "z-last", "a-first"})
				done <- registrationResult{keys, err}
			}()
			result := registrationReceive(t, done)
			require.NoError(t, result.err)
			require.Equal(t, x.keys, result.keys)
			empty, err := table.resolvePublic(first, nil)
			require.NoError(t, err)
			require.Empty(t, empty)
		})
	}
}

func TestAdmissionRegistrationRollsBackFailedBatch(t *testing.T) {
	for _, table := range registrationTables {
		t.Run(table.name, func(t *testing.T) {
			first, second := registrationDatastores(t)
			_, err := first.conn.Exec("CREATE TRIGGER reject_registration BEFORE INSERT ON " + table.name + " WHEN NEW.label='b-fail' BEGIN SELECT RAISE(ABORT, 'registration rejected'); END")
			require.NoError(t, err)
			keys, err := table.resolvePublic(first, []string{"a-created", "b-fail"})
			require.ErrorContains(t, err, "registration rejected")
			require.Nil(t, keys)
			var count int
			require.NoError(t, first.conn.QueryRow("SELECT COUNT(*) FROM "+table.name).Scan(&count))
			require.Zero(t, count, "a failed batch must not retain its earlier insert")
			_, err = first.conn.Exec("DROP TRIGGER reject_registration")
			require.NoError(t, err)
			keys, err = table.resolvePublic(second, []string{"a-created", "b-fail"})
			require.NoError(t, err, "a failed registration must release its writer lock")
			require.Len(t, keys, 2)
			again, err := table.resolvePublic(first, []string{"b-fail", "a-created"})
			require.NoError(t, err)
			require.Equal(t, keys, again)
		})
	}
}
