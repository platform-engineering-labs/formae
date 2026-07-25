// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatastore(t *testing.T) {
	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.SqliteDatastore,
			Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
		}
		ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("Failed to create SQLite datastore: %v", err)
		}
		d, _ := ds.(dssqlite.DatastoreSQLite)
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				return d.CleanUp()
			},
			SetTargetHealthStateForTest: func(label, state string) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE targets SET health_state = ? WHERE label = ? AND version = (SELECT MAX(version) FROM targets WHERE label = ?)`,
					state, label, label,
				)
				return err
			},
			SetTargetAccrualForTest: func(label string, firstUnreachableAt time.Time, accumSeconds int64) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE targets SET first_unreachable_at = ?, unreachable_accum_seconds = ? WHERE label = ? AND version = (SELECT MAX(version) FROM targets WHERE label = ?)`,
					firstUnreachableAt.UTC().Format(time.RFC3339Nano), accumSeconds, label, label,
				)
				return err
			},
			MarkResourceReapedForTest: func(uri string) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE resources SET operation = 'reaped' WHERE uri = ? AND version = (SELECT MAX(version) FROM resources WHERE uri = ?)`,
					uri, uri,
				)
				return err
			},
			RawResourceOperationForTest: func(uri string) (string, error) {
				conn := d.Conn()
				var op string
				err := conn.QueryRow(
					`SELECT operation FROM resources WHERE uri = ? ORDER BY version DESC LIMIT 1`, uri,
				).Scan(&op)
				if err == sql.ErrNoRows {
					return "", nil
				}
				return op, err
			},
			CountReapAuditRowsForTest: func(label string) (int, error) {
				conn := d.Conn()
				var n int
				err := conn.QueryRow(
					`SELECT COUNT(*) FROM target_reap_audit WHERE label = ?`, label,
				).Scan(&n)
				return n, err
			},
		}
	})
}

func newTestDS(t *testing.T) dstest.TestDatastore {
	t.Helper()
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	if err != nil {
		t.Fatalf("Failed to create SQLite datastore: %v", err)
	}
	d, _ := ds.(dssqlite.DatastoreSQLite)
	return dstest.TestDatastore{
		Datastore: ds,
		CleanUpFn: func() error {
			return d.CleanUp()
		},
		SetTargetHealthStateForTest: func(label, state string) error {
			conn := d.Conn()
			_, err := conn.Exec(
				`UPDATE targets SET health_state = ? WHERE label = ? AND version = (SELECT MAX(version) FROM targets WHERE label = ?)`,
				state, label, label,
			)
			return err
		},
		SetTargetAccrualForTest: func(label string, firstUnreachableAt time.Time, accumSeconds int64) error {
			conn := d.Conn()
			_, err := conn.Exec(
				`UPDATE targets SET first_unreachable_at = ?, unreachable_accum_seconds = ? WHERE label = ? AND version = (SELECT MAX(version) FROM targets WHERE label = ?)`,
				firstUnreachableAt.UTC().Format(time.RFC3339Nano), accumSeconds, label, label,
			)
			return err
		},
	}
}

// TestUpdateTarget_RecoveryUnreapIsAtomic proves the recovery re-declare (target
// version bump to a fresh incarnation) and the resource un-reap commit as one
// unit: both, or neither. A trigger forces the un-reap UPDATE to fail after the
// target-version INSERT has run; the whole UpdateTarget must roll back, leaving
// the target still reaped and its resource still tombstoned — never the stranded
// intermediate state (target recovered, resources still reaped) that two separate
// autocommit statements would leave behind on a crash between them. Removing the
// trigger and retrying then recovers both together.
//
// This is a white-box SQLite test because forcing the un-reap to fail needs a
// backend-specific trigger; the Postgres/MSSQL/Aurora UpdateTarget mirror the
// identical single-transaction structure, and the positive recovery path is
// covered for every backend by dstest.RunUpdateTargetUnreapsResourcesOnRecovery.
func TestUpdateTarget_RecoveryUnreapIsAtomic(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	label := "atomic-recover-target"
	stack := "atomic-recover-stack"

	// Seed a reap-ready target: unreachable, accrued past threshold, old timestamps.
	reaping, err := pkgmodel.MarshalReaping(&pkgmodel.ReapAfter{Kind: "after", MaxUnreachableSeconds: 100})
	require.NoError(t, err)
	_, err = ds.CreateTarget(&pkgmodel.Target{
		Label:     label,
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
		Reaping:   reaping,
	})
	require.NoError(t, err)

	loaded, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, loaded.Health)
	inc := loaded.Health.IncarnationID

	seenAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	observedAt := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	applied, err := ds.UpdateTargetHealth(pkgmodel.TargetHealthObservation{
		TargetLabel:   label,
		State:         pkgmodel.TargetHealthStateUnreachable,
		ObservedAt:    observedAt,
		LastSeenAt:    &seenAt,
		IncarnationID: inc,
	})
	require.NoError(t, err)
	require.True(t, applied)

	sampleAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	applied, err = ds.AdvanceTargetAccrual(label, inc, sampleAt, 100)
	require.NoError(t, err)
	require.True(t, applied)

	res := &pkgmodel.Resource{
		Ksuid:      util.NewID(),
		NativeID:   "native-atomic",
		Stack:      stack,
		Type:       "AWS::S3::Bucket",
		Label:      "atomic-res",
		Target:     label,
		Managed:    true,
		Properties: json.RawMessage(`{"key":"value"}`),
	}
	_, err = ds.StoreResource(res, "cmd-create")
	require.NoError(t, err)
	uri := string(res.URI())

	reaped, _, err := ds.PersistTargetReap(datastore.PersistTargetReapRequest{
		Label:            label,
		IncarnationID:    inc,
		LastSeenBefore:   time.Now().UTC(),
		LastSampleBefore: time.Now().UTC(),
		ReapedAt:         time.Now().UTC(),
	})
	require.NoError(t, err)
	require.True(t, reaped, "setup: target must reap")

	// Force the recovery un-reap UPDATE to fail: any transition of a resources row
	// out of the 'reaped' marker aborts.
	_, err = d.Conn().Exec(`
		CREATE TRIGGER fail_unreap
		BEFORE UPDATE ON resources
		WHEN OLD.operation = 'reaped' AND NEW.operation <> 'reaped'
		BEGIN
			SELECT RAISE(ABORT, 'forced un-reap failure');
		END;`)
	require.NoError(t, err)

	recoverTarget, err := ds.LoadTarget(label)
	require.NoError(t, err)
	_, err = ds.UpdateTarget(recoverTarget)
	require.Error(t, err, "the forced un-reap failure must fail the whole UpdateTarget")

	// Neither effect landed: the target is still reaped under its original
	// incarnation (no new version), and the resource is still a reaped tombstone.
	afterFail, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, afterFail.Health)
	assert.Equal(t, pkgmodel.TargetHealthStateReaped, afterFail.Health.State,
		"target must NOT be recovered when the un-reap fails")
	assert.Equal(t, inc, afterFail.Health.IncarnationID,
		"no fresh incarnation may be committed when the un-reap fails")

	stillReaped, err := ds.LoadReapedResources()
	require.NoError(t, err)
	assert.Len(t, stillReaped, 1, "the resource tombstone must survive the rolled-back recovery")

	// Remove the fault and retry: now both effects land together.
	_, err = d.Conn().Exec(`DROP TRIGGER fail_unreap`)
	require.NoError(t, err)

	retryTarget, err := ds.LoadTarget(label)
	require.NoError(t, err)
	_, err = ds.UpdateTarget(retryTarget)
	require.NoError(t, err)

	recovered, err := ds.LoadTarget(label)
	require.NoError(t, err)
	require.NotNil(t, recovered.Health)
	assert.Equal(t, pkgmodel.TargetHealthStateUnknown, recovered.Health.State,
		"retry must recover the target")
	assert.NotEqual(t, inc, recovered.Health.IncarnationID, "retry must mint a fresh incarnation")

	live, err := ds.LoadResourcesByStack(stack)
	require.NoError(t, err)
	assert.Len(t, live, 1, "retry must un-reap the resource")

	var op string
	err = d.Conn().QueryRow(
		`SELECT operation FROM resources WHERE uri = ? ORDER BY version DESC LIMIT 1`, uri,
	).Scan(&op)
	require.NoError(t, err)
	assert.Equal(t, string(resource_update.OperationUpdate), op,
		"retry must clear the reaped marker")

	remaining, err := ds.LoadReapedResources()
	require.NoError(t, err)
	assert.Empty(t, remaining, "no tombstone may remain after successful recovery")
}

// Store → Delete → Store with new KSUID and same data.
func TestStoreDeleteStore(t *testing.T) {
	td := newTestDS(t)
	ds := td.Datastore
	defer td.CleanUpFn() //nolint:errcheck

	target := &pkgmodel.Target{
		Label:     "target-1",
		Namespace: "default",
		Config:    json.RawMessage(`{}`),
	}
	_, err := ds.CreateTarget(target)
	assert.NoError(t, err)

	nativeID := "test-ns/my-configmap"
	resourceType := "K8S::Core::ConfigMap"
	properties := json.RawMessage(`{"metadata":{"name":"my-configmap","namespace":"test-ns"}}`)

	// Step 1: Store with KSUID-A
	ksuidA := util.NewID()
	resourceA := &pkgmodel.Resource{
		Ksuid:      ksuidA,
		NativeID:   nativeID,
		Stack:      "test-stack",
		Type:       resourceType,
		Label:      "cm",
		Target:     "target-1",
		Managed:    true,
		Properties: properties,
	}
	_, err = ds.StoreResource(resourceA, "cmd-create")
	assert.NoError(t, err)

	loaded, err := ds.LoadResourceById(ksuidA)
	assert.NoError(t, err)
	assert.NotNil(t, loaded, "resource should exist under KSUID-A after create")

	// Step 2: Delete
	_, err = ds.DeleteResource(resourceA, "cmd-delete")
	assert.NoError(t, err)

	// Step 3: Store again with KSUID-B, same native_id+type+data
	time.Sleep(1100 * time.Millisecond)
	ksuidB := util.NewID()
	resourceB := &pkgmodel.Resource{
		Ksuid:      ksuidB,
		NativeID:   nativeID,
		Stack:      "test-stack",
		Type:       resourceType,
		Label:      "cm",
		Target:     "target-1",
		Managed:    true,
		Properties: properties,
	}
	versionB, err := ds.StoreResource(resourceB, "cmd-recreate")
	assert.NoError(t, err)

	assert.True(t, strings.HasPrefix(versionB, ksuidB+"_"),
		"StoreResource version should start with KSUID-B (%s), got: %s", ksuidB, versionB)

	loaded, err = ds.LoadResourceById(ksuidB)
	assert.NoError(t, err)
	if assert.NotNil(t, loaded, "resource should be loadable under KSUID-B after delete+recreate") {
		assert.Equal(t, ksuidB, loaded.Ksuid, "loaded resource should have KSUID-B")
		assert.Equal(t, nativeID, loaded.NativeID)
	}
}
