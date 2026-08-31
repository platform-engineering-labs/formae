// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
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
			LoadAgentBootsForTest: func() ([]datastore.AgentBoot, error) {
				conn := d.Conn()
				rows, err := conn.Query(`SELECT boot_id, version, booted_at FROM agent_boots ORDER BY booted_at, boot_id`)
				if err != nil {
					return nil, err
				}
				defer rows.Close() //nolint:errcheck
				var out []datastore.AgentBoot
				for rows.Next() {
					var b datastore.AgentBoot
					var ts string
					if err := rows.Scan(&b.BootID, &b.Version, &ts); err != nil {
						return nil, err
					}
					b.BootedAt, err = time.Parse(time.RFC3339Nano, ts)
					if err != nil {
						return nil, err
					}
					out = append(out, b)
				}
				return out, rows.Err()
			},
			CountReapAuditRowsForTest: func(label string) (int, error) {
				conn := d.Conn()
				var n int
				err := conn.QueryRow(
					`SELECT COUNT(*) FROM target_reap_audit WHERE label = ?`, label,
				).Scan(&n)
				return n, err
			},
			SetStackValidFromForTest: func(label string, validFrom []time.Time) error {
				return setStackValidFrom(d, label, validFrom)
			},
			SetPolicyDataForTest: func(label, policyData string) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE policies SET policy_data = ? WHERE label = ? AND version = (SELECT MAX(version) FROM policies WHERE label = ?)`,
					policyData, label, label,
				)
				return err
			},
			NullResourceUpdateModifiedTsForTest: func(ksuid string) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE resource_updates SET modified_ts = NULL WHERE ksuid = ?`, ksuid,
				)
				return err
			},
			SetResourceUpdateModifiedTsRawForTest: func(ksuid, raw string) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE resource_updates SET modified_ts = ? WHERE ksuid = ?`, raw, ksuid,
				)
				return err
			},
			NullFormaCommandSubjectForTest: func(commandID string) error {
				conn := d.Conn()
				_, err := conn.Exec(
					`UPDATE forma_commands SET subject = NULL, subject_name = NULL WHERE command_id = ?`, commandID,
				)
				return err
			},
			GeneratorIDForTest: func(label, stackLabel string) (string, error) {
				conn := d.Conn()
				var id string
				// generators.stack_id stores the stack's KSUID, not its label, so
				// the stack is resolved by label first (its own current row), the
				// same way the datastore's own Get/DeleteGenerator do.
				err := conn.QueryRow(
					`SELECT g.id FROM generators g
					 JOIN (SELECT id FROM stacks WHERE label = ? ORDER BY version DESC LIMIT 1) s ON g.stack_id = s.id
					 WHERE g.label = ?
					 ORDER BY g.version DESC LIMIT 1`,
					stackLabel, label,
				).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					return "", nil
				}
				return id, err
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

// TestCreateTarget_StripsOpaqueRefValue verifies that CreateTarget does not
// persist a $value from an opaque $ref envelope in targets.config. The
// stored config must retain $ref and $visibility but must not contain $value
// or the resolved secret.
func TestCreateTarget_StripsOpaqueRefValue(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	opaqueConfig := json.RawMessage(`{"auth":{"$ref":"formae://x","$visibility":"Opaque","$value":"super-secret"}}`)
	_, err = ds.CreateTarget(&pkgmodel.Target{
		Label:     "opaque-create",
		Namespace: "AWS",
		Config:    opaqueConfig,
	})
	require.NoError(t, err)

	// Read the raw stored bytes directly from the DB — bypassing any unmarshalling.
	var raw string
	err = d.Conn().QueryRow(
		`SELECT config FROM targets WHERE label = 'opaque-create' ORDER BY version DESC LIMIT 1`,
	).Scan(&raw)
	require.NoError(t, err)

	assert.Contains(t, raw, `$ref`, "stored config must preserve $ref")
	assert.Contains(t, raw, `$visibility`, "stored config must preserve $visibility")
	assert.NotContains(t, raw, `$value`, "stored config must not contain $value")
	assert.NotContains(t, raw, "super-secret", "stored config must not contain the resolved secret")
}

// TestUpdateTarget_StripsOpaqueRefValue verifies that UpdateTarget does not
// persist a $value from an opaque $ref envelope in targets.config.
func TestUpdateTarget_StripsOpaqueRefValue(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	// Seed with a clean config first.
	_, err = ds.CreateTarget(&pkgmodel.Target{
		Label:     "opaque-update",
		Namespace: "AWS",
		Config:    json.RawMessage(`{"Region":"us-east-1"}`),
	})
	require.NoError(t, err)

	loaded, err := ds.LoadTarget("opaque-update")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Update with a config that carries an opaque $ref $value.
	loaded.Config = json.RawMessage(`{"auth":{"$ref":"formae://x","$visibility":"Opaque","$value":"super-secret"}}`)
	_, err = ds.UpdateTarget(loaded)
	require.NoError(t, err)

	var raw string
	err = d.Conn().QueryRow(
		`SELECT config FROM targets WHERE label = 'opaque-update' ORDER BY version DESC LIMIT 1`,
	).Scan(&raw)
	require.NoError(t, err)

	assert.Contains(t, raw, `$ref`, "stored config must preserve $ref")
	assert.NotContains(t, raw, `$value`, "stored config must not contain $value")
	assert.NotContains(t, raw, "super-secret", "stored config must not contain the resolved secret")
}

// TestStoreFormaCommand_StripsOpaqueRefValueFromTargetUpdates verifies that
// StoreFormaCommand does not persist $value from an opaque $ref envelope in
// forma_commands.target_updates. The stored blob must retain $ref but must not
// contain $value or the resolved secret.
func TestStoreFormaCommand_StripsOpaqueRefValueFromTargetUpdates(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	opaqueConfig := json.RawMessage(`{"auth":{"$ref":"formae://x","$visibility":"Opaque","$value":"super-secret"}}`)

	commandID := util.NewID()
	fc := &forma_command.FormaCommand{
		ID:      commandID,
		Command: pkgmodel.CommandApply,
		State:   forma_command.CommandStateNotStarted,
		Config:  config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		TargetUpdates: []target_update.TargetUpdate{
			{
				Target: pkgmodel.Target{
					Label:     "opaque-target",
					Namespace: "AWS",
					Config:    opaqueConfig,
				},
				Operation: target_update.TargetOperationCreate,
				State:     target_update.TargetUpdateStateNotStarted,
			},
		},
	}

	require.NoError(t, ds.StoreFormaCommand(fc, commandID))

	// Read the raw stored bytes directly from the DB — bypassing any unmarshalling.
	var raw string
	err = d.Conn().QueryRow(
		`SELECT target_updates FROM forma_commands WHERE command_id = ?`, commandID,
	).Scan(&raw)
	require.NoError(t, err)

	assert.Contains(t, raw, `$ref`, "stored target_updates must preserve $ref")
	assert.NotContains(t, raw, `$value`, "stored target_updates must not contain $value")
	assert.NotContains(t, raw, "super-secret", "stored target_updates must not contain the resolved secret")
}

// TestBulkStoreResourceUpdates_StripsOpaqueRefValueFromExistingTarget verifies
// that BulkStoreResourceUpdates does not persist a $value from an opaque $ref
// envelope in resource_updates.existing_target. A legacy target row written
// before stripping was introduced may still carry a plaintext opaque $ref
// $value; re-persisting it unstripped would re-introduce the secret.
func TestBulkStoreResourceUpdates_StripsOpaqueRefValueFromExistingTarget(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	opaqueConfig := json.RawMessage(`{"auth":{"$ref":"formae://x","$visibility":"Opaque","$value":"super-secret"}}`)

	commandID := util.NewID()
	ksuid := util.NewID()

	ru := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Ksuid:  ksuid,
			Stack:  "default",
			Type:   "AWS::S3::Bucket",
			Label:  "my-bucket",
			Target: "tgt",
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "tgt",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		},
		ExistingTarget: pkgmodel.Target{
			Label:     "tgt",
			Namespace: "AWS",
			Config:    opaqueConfig,
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateNotStarted,
	}

	require.NoError(t, ds.BulkStoreResourceUpdates(commandID, []resource_update.ResourceUpdate{ru}))

	// Read the raw stored bytes directly from the DB — bypassing any unmarshalling.
	var raw string
	err = d.Conn().QueryRow(
		`SELECT existing_target FROM resource_updates WHERE command_id = ?`, commandID,
	).Scan(&raw)
	require.NoError(t, err)

	assert.Contains(t, raw, `$ref`, "stored existing_target must preserve $ref")
	assert.Contains(t, raw, `$visibility`, "stored existing_target must preserve $visibility")
	assert.NotContains(t, raw, `$value`, "stored existing_target must not contain $value")
	assert.NotContains(t, raw, "super-secret", "stored existing_target must not contain the resolved secret")
}

// TestCreateTarget_StripsOpaqueGenValueWithoutVisibility proves the
// fail-closed strip at the storage boundary: a $gen envelope missing
// $visibility entirely (a shape normal translation never produces — the
// schema fixes $visibility to "Opaque" on every $gen) must still be stripped
// before it reaches the database. Before the fix, gating the strip predicate
// on a $visibility reading let this exact shape land in the SQLite file in
// cleartext.
func TestCreateTarget_StripsOpaqueGenValueWithoutVisibility(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	const sentinel = "gen-cleartext-sentinel-no-visibility-storage"
	opaqueConfig := json.RawMessage(`{"auth":{"$gen":true,"$generator":"2ABcDeFgHiJkLmNoPqRsTuVwXyZ","$output":"value","$value":"` + sentinel + `"}}`)
	_, err = ds.CreateTarget(&pkgmodel.Target{
		Label:     "gen-no-visibility-create",
		Namespace: "AWS",
		Config:    opaqueConfig,
	})
	require.NoError(t, err)

	var raw string
	err = d.Conn().QueryRow(
		`SELECT config FROM targets WHERE label = 'gen-no-visibility-create' ORDER BY version DESC LIMIT 1`,
	).Scan(&raw)
	require.NoError(t, err)

	assert.Contains(t, raw, `$gen`, "stored config must preserve $gen")
	assert.NotContains(t, raw, `$value`, "stored config must not contain $value")
	assert.NotContains(t, raw, sentinel, "the generated secret must never reach at-rest storage, even without $visibility")
}

// TestBulkStoreResourceUpdates_StripsOpaqueGenValueFromExistingTarget proves
// what actually reaches storage, not what the strip function's return value
// claims: a resolved generator value seeded onto a target config must not
// survive to the persisted row. If any write path bypassed the shared strip
// predicate, this reads the sentinel straight back out of the database.
func TestBulkStoreResourceUpdates_StripsOpaqueGenValueFromExistingTarget(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(dssqlite.DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	const sentinel = "gen-cleartext-sentinel-storage-9f3a"
	opaqueConfig := json.RawMessage(`{"auth":{"$gen":true,"$generator":"2ABcDeFgHiJkLmNoPqRsTuVwXyZ","$output":"value","$visibility":"Opaque","$value":"` + sentinel + `"}}`)

	commandID := util.NewID()
	ksuid := util.NewID()

	ru := resource_update.ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Ksuid:  ksuid,
			Stack:  "default",
			Type:   "AWS::S3::Bucket",
			Label:  "my-bucket",
			Target: "tgt",
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "tgt",
			Namespace: "AWS",
			Config:    json.RawMessage(`{}`),
		},
		ExistingTarget: pkgmodel.Target{
			Label:     "tgt",
			Namespace: "AWS",
			Config:    opaqueConfig,
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateNotStarted,
	}

	require.NoError(t, ds.BulkStoreResourceUpdates(commandID, []resource_update.ResourceUpdate{ru}))

	// Read the raw stored bytes directly from the DB — bypassing any unmarshalling.
	var raw string
	err = d.Conn().QueryRow(
		`SELECT existing_target FROM resource_updates WHERE command_id = ?`, commandID,
	).Scan(&raw)
	require.NoError(t, err)

	assert.Contains(t, raw, `$gen`, "stored existing_target must preserve $gen")
	assert.Contains(t, raw, `$generator`, "stored existing_target must preserve $generator")
	assert.NotContains(t, raw, `$value`, "stored existing_target must not contain $value")
	assert.NotContains(t, raw, sentinel, "the generated secret must never reach at-rest storage")
}

// setStackValidFrom rewrites the valid_from of a stack's versions in ascending
// version order. SQLite's CURRENT_TIMESTAMP default writes "YYYY-MM-DD HH:MM:SS"
// in UTC, so the backdated values are written in that same shape — the expiry
// query's datetime() arithmetic reads the column, not a Go time.
func setStackValidFrom(d dssqlite.DatastoreSQLite, label string, validFrom []time.Time) error {
	conn := d.Conn()
	rows, err := conn.Query(`SELECT version FROM stacks WHERE label = ? ORDER BY version ASC`, label)
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
			`UPDATE stacks SET valid_from = ? WHERE label = ? AND version = ?`,
			validFrom[i].UTC().Format("2006-01-02 15:04:05"), label, version,
		); err != nil {
			return err
		}
	}
	return nil
}
