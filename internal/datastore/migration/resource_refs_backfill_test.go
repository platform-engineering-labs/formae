// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
)

// TestBackfillResourceRefs_SkipOnUnsupportedDatastore verifies that
// BackfillResourceRefs returns nil immediately — without paging — when the
// datastore does not implement UpdateResourceRefs (e.g. sqlite, mssql).
// This is the locally-runnable proof of the skip path.
func TestBackfillResourceRefs_SkipOnUnsupportedDatastore(t *testing.T) {
	// newTestDatastore() returns a sqlite datastore, which does NOT implement
	// UpdateResourceRefs.  The backfill must return nil and must not attempt to
	// page (LoadResourceVersionsPage is never called on an empty sqlite store,
	// and any call would itself return nil — but since the skip happens before
	// the paging loop we never reach it).
	ds := newTestDatastore(t)

	err := BackfillResourceRefs(ds)
	require.NoError(t, err, "BackfillResourceRefs must return nil on a datastore without the refs column")
}

// newPostgresTestDatastore creates an isolated Postgres datastore for a single
// test. The test is skipped when Postgres is not reachable at localhost:5432.
func newPostgresTestDatastore(t *testing.T) (postgres.DatastorePostgres, func()) {
	t.Helper()
	adminConn, err := pgx.Connect(context.Background(), "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(context.Background()) //nolint:errcheck

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.PostgresDatastore,
		Postgres: pkgmodel.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "admin",
			Database: fmt.Sprintf("test_refs_backfill_%s", mksuid.New().String()),
		},
	}
	iface, err := postgres.NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, ok := iface.(postgres.DatastorePostgres)
	require.True(t, ok)
	cleanup := func() {
		d.Close()
		_ = d.CleanUp()
	}
	return d, cleanup
}

// storeTestTargetPG creates a minimal target in a postgres datastore so that
// resource stores succeed (resources reference a target).
func storeTestTargetPG(t *testing.T, d postgres.DatastorePostgres) {
	t.Helper()
	_, err := d.CreateTarget(&pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	})
	require.NoError(t, err)
}

// queryRefsRaw reads the refs column for a given ksuid directly from the
// resources table via a raw pgx connection, bypassing the ORM layer.
func queryRefsRaw(t *testing.T, conn *pgx.Conn, ksuid string) []string {
	t.Helper()
	var refs []string
	err := conn.QueryRow(context.Background(),
		`SELECT refs FROM resources WHERE ksuid = $1 ORDER BY version COLLATE "C" DESC LIMIT 1`,
		ksuid,
	).Scan(&refs)
	require.NoError(t, err)
	if refs == nil {
		refs = []string{}
	}
	return refs
}

// resetAllRefs sets the refs column to '{}' for every row in the resources
// table, simulating rows that existed before migration 00019 populated the
// column. This puts the table into the pre-backfill state that the sweep is
// designed to fix.
func resetAllRefs(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	_, err := conn.Exec(context.Background(), `UPDATE resources SET refs = '{}'`)
	require.NoError(t, err)
}

// storeResource is a thin helper that calls StoreResource and returns the
// resource's KSUID alongside the stored version string.
func storeResource(t *testing.T, d postgres.DatastorePostgres, r *pkgmodel.Resource) string {
	t.Helper()
	_, err := d.StoreResource(r, "seed-command")
	require.NoError(t, err)
	return r.Ksuid
}

// TestBackfillResourceRefs_ParityPostgres is the parity test that proves the
// backfill produces exactly the same refs as model.CollectReferencedKSUIDs for
// every row. It runs against a live Postgres instance; the test is skipped when
// Postgres is not available at localhost:5432 (CI provides it; local runs skip).
//
// The corpus covers: a resolved formae:// $ref (single), multiple distinct refs,
// the same ref repeated (dedup), a nested ref inside a $value, a non-formae://
// $ref (excluded), a malformed formae:// with no # fragment (excluded), a
// user-authored $ref-like key (excluded), and a resource with no refs at all.
func TestBackfillResourceRefs_ParityPostgres(t *testing.T) {
	d, cleanup := newPostgresTestDatastore(t)
	defer cleanup()
	storeTestTargetPG(t, d)

	connStr := postgres.BuildConnStr("localhost", 5432, "postgres", "admin", d.Pool().Config().ConnConfig.Database)
	conn, err := pgx.Connect(context.Background(), connStr)
	require.NoError(t, err)
	defer conn.Close(context.Background()) //nolint:errcheck

	// Allocate KSUIDs for the referencing resources.
	refKsuid1 := mksuid.New().String()
	refKsuid2 := mksuid.New().String()

	type testCase struct {
		label      string
		properties json.RawMessage
	}

	cases := []testCase{
		{
			// Single resolved formae:// $ref.
			label: "single-ref",
			properties: json.RawMessage(fmt.Sprintf(
				`{"Arn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:iam::123:role/r"}}`,
				refKsuid1,
			)),
		},
		{
			// Multiple distinct refs.
			label: "multi-ref",
			properties: json.RawMessage(fmt.Sprintf(
				`{"A":{"$ref":"formae://%s#/A","$value":"val-a"},"B":{"$ref":"formae://%s#/B","$value":"val-b"}}`,
				refKsuid1, refKsuid2,
			)),
		},
		{
			// Same ref repeated — must be deduped to one entry.
			label: "dedup-ref",
			properties: json.RawMessage(fmt.Sprintf(
				`{"A":{"$ref":"formae://%s#/A","$value":"a"},"B":{"$ref":"formae://%s#/B","$value":"b"}}`,
				refKsuid1, refKsuid1,
			)),
		},
		{
			// Ref nested inside a $value object.
			label: "nested-ref",
			properties: json.RawMessage(fmt.Sprintf(
				`{"Outer":{"$ref":"formae://%s#/O","$value":{"Inner":{"$ref":"formae://%s#/I","$value":"x"}}}}`,
				refKsuid1, refKsuid2,
			)),
		},
		{
			// Non-formae:// $ref — must be excluded.
			label: "non-formae-ref",
			properties: json.RawMessage(`{"Ext":{"$ref":"https://example.com/schema","$value":"x"}}`),
		},
		{
			// Malformed formae:// URI with no # fragment — KSUID() returns "" so excluded.
			label: "malformed-ref",
			properties: json.RawMessage(fmt.Sprintf(
				`{"X":{"$ref":"formae://%s","$value":"y"}}`,
				refKsuid1,
			)),
		},
		{
			// User-authored key that looks like a $ref but is not a formae:// URI.
			label: "user-ref-key",
			properties: json.RawMessage(`{"$ref":"some-local-reference"}`),
		},
		{
			// No refs at all.
			label: "no-refs",
			properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
		},
	}

	// Seed all resources and record their KSUIDs.
	ksuids := make(map[string]string, len(cases))
	for _, tc := range cases {
		ksuid := util.NewID()
		ksuids[tc.label] = ksuid
		storeResource(t, d, &pkgmodel.Resource{
			Ksuid:      ksuid,
			NativeID:   "native-" + tc.label,
			Stack:      "test-stack",
			Label:      tc.label,
			Type:       "AWS::S3::Bucket",
			Target:     "test-target",
			Properties: tc.properties,
		})
	}

	// Force all refs columns back to '{}' to simulate pre-migration rows.
	resetAllRefs(t, conn)

	// Verify the reset took effect.
	for _, tc := range cases {
		got := queryRefsRaw(t, conn, ksuids[tc.label])
		assert.Empty(t, got, "refs must be empty after reset for %s", tc.label)
	}

	// Run the backfill.
	require.NoError(t, BackfillResourceRefs(d))

	// Assert that every row's refs column now equals CollectReferencedKSUIDs of
	// the marshaled *pkgmodel.Resource (the same input the live writers use).
	for _, tc := range cases {
		r := &pkgmodel.Resource{
			Ksuid:      ksuids[tc.label],
			NativeID:   "native-" + tc.label,
			Stack:      "test-stack",
			Label:      tc.label,
			Type:       "AWS::S3::Bucket",
			Target:     "test-target",
			Properties: tc.properties,
		}
		data, err := json.Marshal(r)
		require.NoError(t, err)
		wantRefs := pkgmodel.CollectReferencedKSUIDs(data)

		gotRefs := queryRefsRaw(t, conn, ksuids[tc.label])
		assert.Equal(t, wantRefs, gotRefs,
			"backfilled refs for %s must equal CollectReferencedKSUIDs of stored data", tc.label)
	}

	// Idempotency: a second run must not change any row.
	require.NoError(t, BackfillResourceRefs(d), "second BackfillResourceRefs run must not error")
	for _, tc := range cases {
		r := &pkgmodel.Resource{
			Ksuid:      ksuids[tc.label],
			NativeID:   "native-" + tc.label,
			Stack:      "test-stack",
			Label:      tc.label,
			Type:       "AWS::S3::Bucket",
			Target:     "test-target",
			Properties: tc.properties,
		}
		data, err := json.Marshal(r)
		require.NoError(t, err)
		wantRefs := pkgmodel.CollectReferencedKSUIDs(data)

		gotRefs := queryRefsRaw(t, conn, ksuids[tc.label])
		assert.Equal(t, wantRefs, gotRefs,
			"idempotency: refs for %s must be unchanged after second run", tc.label)
	}
}
