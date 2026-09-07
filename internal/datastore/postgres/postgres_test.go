// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectTestDB opens a raw pgx connection to the test database, skipping the test if
// Postgres is unavailable. Callers are responsible for closing the connection.
func connectTestDB(t *testing.T, host string, port int, user, password, database string) *pgx.Conn {
	t.Helper()
	connStr := postgres.BuildConnStr(host, port, user, password, database)
	conn, err := pgx.Connect(context.Background(), connStr)
	require.NoError(t, err)
	return conn
}

// newTestDatastore creates an isolated Postgres datastore for a single test and
// returns it together with a cleanup function. The test is skipped when Postgres
// is not reachable.
func newTestDatastore(t *testing.T) (postgres.DatastorePostgres, func()) {
	t.Helper()
	adminConn, err := pgx.Connect(context.Background(), "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(context.Background())

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.PostgresDatastore,
		Postgres: pkgmodel.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "admin",
			Database: fmt.Sprintf("test_refs_%s", mksuid.New().String()),
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

// storeTestTarget creates a minimal target so that resource stores succeed.
func storeTestTarget(t *testing.T, d postgres.DatastorePostgres) {
	t.Helper()
	_, err := d.CreateTarget(&pkgmodel.Target{
		Label:     "test-target",
		Namespace: "AWS",
		Config:    json.RawMessage(`{}`),
	})
	require.NoError(t, err)
}

// queryRefs reads the refs column for a given ksuid from the resources table.
func queryRefs(t *testing.T, conn *pgx.Conn, ksuid string) []string {
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

// TestResourceRefs_StoreCreate verifies that a resource stored via the INSERT
// (create) path has its refs column populated from the data JSON.
func TestResourceRefs_StoreCreate(t *testing.T) {
	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	conn := connectTestDB(t, "localhost", 5432, "postgres", "admin", d.Pool().Config().ConnConfig.Database)
	defer conn.Close(context.Background()) //nolint:errcheck

	parentKsuid := mksuid.New().String()
	childKsuid := mksuid.New().String()

	// Store the parent first so the database exists.
	_, err := d.StoreResource(&pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "parent-native",
		Stack:      "test-stack",
		Label:      "parent-resource",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"my-bucket"}`),
	}, "cmd-1")
	require.NoError(t, err)

	// Store a child resource whose Properties carry a cross-resource $ref.
	childProps := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:iam::123:role/r"}}`, parentKsuid)
	childData, err := json.Marshal(&pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "child-native",
		Stack:      "test-stack",
		Label:      "child-resource",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(childProps),
	})
	require.NoError(t, err)

	_, err = d.StoreResource(&pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "child-native",
		Stack:      "test-stack",
		Label:      "child-resource",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(childProps),
	}, "cmd-1")
	require.NoError(t, err)

	gotRefs := queryRefs(t, conn, childKsuid)
	wantRefs := pkgmodel.CollectReferencedKSUIDs(childData)
	assert.Equal(t, wantRefs, gotRefs, "refs column must equal CollectReferencedKSUIDs of stored data")
}

// TestResourceRefs_StoreCreate_NoRefs verifies that a resource with no cross-resource
// references gets an empty (non-null) refs column.
func TestResourceRefs_StoreCreate_NoRefs(t *testing.T) {
	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	conn := connectTestDB(t, "localhost", 5432, "postgres", "admin", d.Pool().Config().ConnConfig.Database)
	defer conn.Close(context.Background()) //nolint:errcheck

	ksuid := mksuid.New().String()
	_, err := d.StoreResource(&pkgmodel.Resource{
		Ksuid:      ksuid,
		NativeID:   "no-ref-native",
		Stack:      "test-stack",
		Label:      "no-ref-resource",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"plain-bucket"}`),
	}, "cmd-1")
	require.NoError(t, err)

	gotRefs := queryRefs(t, conn, ksuid)
	assert.Empty(t, gotRefs, "resource with no $refs must have an empty refs column")
}

// TestResourceRefs_StoreUpsert verifies that when the same resource is stored a second
// time (triggering the INSERT … ON CONFLICT DO UPDATE upsert path), the refs column is
// updated to match the new data.
func TestResourceRefs_StoreUpsert(t *testing.T) {
	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	conn := connectTestDB(t, "localhost", 5432, "postgres", "admin", d.Pool().Config().ConnConfig.Database)
	defer conn.Close(context.Background()) //nolint:errcheck

	parentKsuid := mksuid.New().String()
	childKsuid := mksuid.New().String()

	// First store: no refs.
	_, err := d.StoreResource(&pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "parent-upsert",
		Stack:      "test-stack",
		Label:      "parent-upsert",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"upsert-bucket"}`),
	}, "cmd-1")
	require.NoError(t, err)

	childProps := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:iam::123:role/r"}}`, parentKsuid)
	childResource := &pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "child-upsert",
		Stack:      "test-stack",
		Label:      "child-upsert",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(childProps),
	}

	// First store of the child (INSERT path).
	_, err = d.StoreResource(childResource, "cmd-1")
	require.NoError(t, err)

	childData, err := json.Marshal(childResource)
	require.NoError(t, err)
	wantRefs := pkgmodel.CollectReferencedKSUIDs(childData)

	gotRefs := queryRefs(t, conn, childKsuid)
	assert.Equal(t, wantRefs, gotRefs, "refs must be populated on initial store")

	// Second store with same data triggers upsert path; refs must remain correct.
	_, err = d.StoreResource(childResource, "cmd-2")
	require.NoError(t, err)

	gotRefs = queryRefs(t, conn, childKsuid)
	assert.Equal(t, wantRefs, gotRefs, "refs must still be correct after upsert")
}

// TestResourceRefs_UpdateResourceVersionData verifies that UpdateResourceVersionData
// populates the refs column when data contains $ref values.
func TestResourceRefs_UpdateResourceVersionData(t *testing.T) {
	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	conn := connectTestDB(t, "localhost", 5432, "postgres", "admin", d.Pool().Config().ConnConfig.Database)
	defer conn.Close(context.Background()) //nolint:errcheck

	parentKsuid := mksuid.New().String()
	childKsuid := mksuid.New().String()

	_, err := d.StoreResource(&pkgmodel.Resource{
		Ksuid:      parentKsuid,
		NativeID:   "parent-uvd",
		Stack:      "test-stack",
		Label:      "parent-uvd",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"uvd-bucket"}`),
	}, "cmd-1")
	require.NoError(t, err)

	// Store the child without any refs initially.
	childResource := &pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "child-uvd",
		Stack:      "test-stack",
		Label:      "child-uvd",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(`{"RoleArn":"plain-arn"}`),
	}
	versionID, err := d.StoreResource(childResource, "cmd-1")
	require.NoError(t, err)

	// Derive the version portion from the returned version ID.
	// versionID is "<ksuid>_<version>" — split on the last underscore.
	var version string
	for i := len(versionID) - 1; i >= 0; i-- {
		if versionID[i] == '_' {
			version = versionID[i+1:]
			break
		}
	}
	require.NotEmpty(t, version, "expected a version segment in versionID %q", versionID)

	// Now update with a resource that carries a $ref.
	childProps := fmt.Sprintf(`{"RoleArn":{"$ref":"formae://%s#/Arn","$value":"arn:aws:iam::123:role/r"}}`, parentKsuid)
	updatedResource := &pkgmodel.Resource{
		Ksuid:      childKsuid,
		NativeID:   "child-uvd",
		Stack:      "test-stack",
		Label:      "child-uvd",
		Type:       "AWS::IAM::Role",
		Target:     "test-target",
		Properties: json.RawMessage(childProps),
	}
	err = d.UpdateResourceVersionData(string(childResource.URI()), version, updatedResource)
	require.NoError(t, err)

	updatedData, err := json.Marshal(updatedResource)
	require.NoError(t, err)
	wantRefs := pkgmodel.CollectReferencedKSUIDs(updatedData)

	gotRefs := queryRefs(t, conn, childKsuid)
	assert.Equal(t, wantRefs, gotRefs, "refs must reflect the $ref in the updated data")
}

// TestResourceRefs_UpdateResourceRefs verifies that UpdateResourceRefs directly
// overwrites the refs column.
func TestResourceRefs_UpdateResourceRefs(t *testing.T) {
	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	conn := connectTestDB(t, "localhost", 5432, "postgres", "admin", d.Pool().Config().ConnConfig.Database)
	defer conn.Close(context.Background()) //nolint:errcheck

	ksuid := mksuid.New().String()
	versionID, err := d.StoreResource(&pkgmodel.Resource{
		Ksuid:      ksuid,
		NativeID:   "urr-native",
		Stack:      "test-stack",
		Label:      "urr-resource",
		Type:       "AWS::S3::Bucket",
		Target:     "test-target",
		Properties: json.RawMessage(`{"BucketName":"urr-bucket"}`),
	}, "cmd-1")
	require.NoError(t, err)

	var version string
	for i := len(versionID) - 1; i >= 0; i-- {
		if versionID[i] == '_' {
			version = versionID[i+1:]
			break
		}
	}
	require.NotEmpty(t, version)

	uri := fmt.Sprintf("formae://%s#", ksuid)
	newRefs := []string{"ref-ksuid-aaa", "ref-ksuid-bbb"}
	err = d.UpdateResourceRefs(uri, version, newRefs)
	require.NoError(t, err)

	gotRefs := queryRefs(t, conn, ksuid)
	assert.Equal(t, newRefs, gotRefs, "UpdateResourceRefs must overwrite the refs column")
}

func TestDatastore(t *testing.T) {
	// Verify we can actually connect to postgres with our test credentials
	connStr := "postgres://postgres:admin@localhost:5432/postgres"
	conn, err := pgx.Connect(context.Background(), connStr)
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	conn.Close(context.Background())

	dstest.RunAll(t, func(t *testing.T) dstest.TestDatastore {
		t.Helper()
		cfg := &pkgmodel.DatastoreConfig{
			DatastoreType: pkgmodel.PostgresDatastore,
			Postgres: pkgmodel.PostgresConfig{
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "admin",
				Database: fmt.Sprintf("test_%s", mksuid.New().String()),
			},
		}
		ds, err := postgres.NewDatastorePostgresEnsureDatabase(context.Background(), cfg, "test")
		if err != nil {
			t.Fatalf("Failed to create Postgres datastore: %v", err)
		}
		d, _ := ds.(postgres.DatastorePostgres)
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				// Close the pool before dropping the database so this datastore's
				// pooled connections are released back to the server. Without this,
				// every conformance subtest leaks its pool and the suite eventually
				// exhausts Postgres's connection limit ("too many clients already").
				d.Close()
				return d.CleanUp()
			},
			LoadAgentBootsForTest: func() ([]datastore.AgentBoot, error) {
				rows, err := d.Pool().Query(context.Background(),
					`SELECT boot_id, version, booted_at FROM agent_boots ORDER BY booted_at, boot_id`)
				if err != nil {
					return nil, err
				}
				defer rows.Close()
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
				_, err := d.Pool().Exec(context.Background(),
					`UPDATE targets SET health_state = $1 WHERE label = $2 AND version = (SELECT MAX(version) FROM targets WHERE label = $2)`,
					state, label,
				)
				return err
			},
			SetStackValidFromForTest: func(label string, validFrom []time.Time) error {
				ctx := context.Background()
				rows, err := d.Pool().Query(ctx,
					`SELECT version FROM stacks WHERE label = $1 ORDER BY version COLLATE "C" ASC`, label)
				if err != nil {
					return err
				}
				var versions []string
				for rows.Next() {
					var version string
					if err := rows.Scan(&version); err != nil {
						rows.Close()
						return err
					}
					versions = append(versions, version)
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return err
				}
				if len(versions) != len(validFrom) {
					return fmt.Errorf("stack %q has %d versions, got %d timestamps", label, len(versions), len(validFrom))
				}
				for i, version := range versions {
					if _, err := d.Pool().Exec(ctx,
						`UPDATE stacks SET valid_from = $1 WHERE label = $2 AND version = $3`,
						validFrom[i].UTC(), label, version,
					); err != nil {
						return err
					}
				}
				return nil
			},
			SetPolicyDataForTest: func(label, policyData string) error {
				_, err := d.Pool().Exec(context.Background(),
					`UPDATE policies SET policy_data = $1 WHERE label = $2 AND version = (SELECT MAX(version) FROM policies WHERE label = $2)`,
					policyData, label,
				)
				return err
			},
			NullResourceUpdateModifiedTsForTest: func(ksuid string) error {
				_, err := d.Pool().Exec(context.Background(),
					`UPDATE resource_updates SET modified_ts = NULL WHERE ksuid = $1`, ksuid,
				)
				return err
			},
			NullFormaCommandSubjectForTest: func(commandID string) error {
				_, err := d.Pool().Exec(context.Background(),
					`UPDATE forma_commands SET subject = NULL, subject_name = NULL WHERE command_id = $1`, commandID,
				)
				return err
			},
			GeneratorIDForTest: func(label, stackLabel string) (string, error) {
				var id string
				// generators.stack_id stores the stack's KSUID, not its label, so
				// the stack is resolved by label first (its own current row), the
				// same way the datastore's own Get/DeleteGenerator do.
				err := d.Pool().QueryRow(context.Background(),
					`SELECT g.id FROM generators g
					 JOIN (SELECT id FROM stacks WHERE label = $1 ORDER BY version COLLATE "C" DESC LIMIT 1) s ON g.stack_id = s.id
					 WHERE g.label = $2
					 ORDER BY g.version COLLATE "C" DESC LIMIT 1`,
					stackLabel, label,
				).Scan(&id)
				if errors.Is(err, pgx.ErrNoRows) {
					return "", nil
				}
				return id, err
			},
		}
	})
}

// Latest-version subqueries (`r2.version > r1.version`) on the resources table
// depend on byte-order semantics because KSUIDs are base62 (mixed case) and only
// sort chronologically under byte ordering. Under the default Postgres collation,
// locale rules can put an uppercase-letter version before a chronologically-later
// lowercase-letter version, causing the "latest" filter to pick the wrong row.
// When the true latest is a delete and the locale-latest is an update, the
// resource leaks back through as "managed" even after destroy.
//
// Scenario: `update` row has version "2wQUVeqpc…" (uppercase 'U' at position 3),
// `delete` row has a chronologically-later version "2wQfTRyui…" (lowercase 'f').
// Byte order: delete > update (correct — delete is latest, exclude resource).
// Locale order: update > delete (wrong — update appears latest, resource leaks).
func TestLoadResourcesByStack_ExcludesDeletedResourceWhenVersionsMixCase(t *testing.T) {
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(ctx)

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.PostgresDatastore,
		Postgres: pkgmodel.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "admin",
			Database: fmt.Sprintf("test_collate_%s", mksuid.New().String()),
		},
	}
	ds, err := postgres.NewDatastorePostgresEnsureDatabase(ctx, cfg, "test")
	require.NoError(t, err)
	defer func() {
		if d, ok := ds.(postgres.DatastorePostgres); ok {
			_ = d.CleanUp()
		}
	}()

	// Open a second connection for direct INSERT to control the version column precisely.
	connStr := postgres.BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, cfg.Postgres.Database)
	conn, err := pgx.Connect(ctx, connStr)
	require.NoError(t, err)
	defer conn.Close(ctx) //nolint:errcheck

	// Earlier update row with an uppercase letter in the version's locale-significant position.
	updateVersion := "2wQUVeqpcVTSF4ROlhC9N4kMUPk"
	// Chronologically-later delete row with a lowercase letter. Byte order puts this after
	// updateVersion (correct); locale order puts it before (the bug).
	deleteVersion := "2wQfTRyuifVOB5LKSwee7d8aErH"

	uri := "formae://test-ksuid"
	data := json.RawMessage(`{"Schema":{},"Properties":{}}`)

	_, err = conn.Exec(ctx, `INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uri, updateVersion, "cmd-1", "update", "native-1", "test-stack", "type-1", "label-1", "", data, true, "test-ksuid")
	require.NoError(t, err)

	_, err = conn.Exec(ctx, `INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uri, deleteVersion, "cmd-2", "delete", "native-1", "test-stack", "type-1", "label-1", "", data, true, "test-ksuid")
	require.NoError(t, err)

	// Sanity check: under the default collation, 'U' > 'f' — the bug's precondition.
	var localeGT bool
	err = conn.QueryRow(ctx, `SELECT 'U' > 'f'`).Scan(&localeGT)
	require.NoError(t, err)
	require.True(t, localeGT, "test precondition: default collation must treat 'U' as greater than 'f'")

	results, err := ds.LoadResourcesByStack("test-stack")
	require.NoError(t, err)

	assert.Empty(t, results,
		"resource with a later delete row must be excluded from LoadResourcesByStack, "+
			"but uncollated version comparison picked the earlier update row as 'latest'")
}

// TestFindResourcesDependingOn_UsesRefsIndex asserts that the refs && $1 predicate
// of the cascade dependency lookup is backed by idx_resources_refs rather than a
// full scan of the refs column. The test seeds several hundred rows with cross-refs,
// runs ANALYZE, disables sequential scans for the session, then asks Postgres for the
// EXPLAIN plan and verifies that idx_resources_refs appears.
//
// enable_seqscan is disabled because at the few-hundred-row scale of this test the
// planner would pick a sequential scan on cost grounds; disabling it asserts that an
// indexed path *exists* for the overlap predicate. The plan is not asserted to be
// scan-free: the NOT EXISTS "latest version" anti-join legitimately reads the
// candidate rows sharing a uri regardless of table size, and that is not what this
// index targets.
//
// The test is skipped automatically when Postgres is unavailable (the local SQLite
// path is covered by the shared dstest suite).
func TestFindResourcesDependingOn_UsesRefsIndex(t *testing.T) {
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(ctx)

	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	// Seed a parent resource.
	parentKsuid := mksuid.New().String()
	_, err = d.StoreResource(&pkgmodel.Resource{
		Ksuid: parentKsuid, NativeID: "idx-parent", Stack: "s", Label: "idx-parent",
		Type: "AWS::S3::Bucket", Target: "test-target",
		Properties: json.RawMessage(`{"BucketName":"idx-test"}`),
	}, "cmd-0")
	require.NoError(t, err)

	// Seed 300 child rows: 100 reference parentKsuid, 200 are unrelated.
	for i := 0; i < 100; i++ {
		childProps := fmt.Sprintf(`{"V":{"$ref":"formae://%s#/Arn","$value":"v"}}`, parentKsuid)
		_, err = d.StoreResource(&pkgmodel.Resource{
			Ksuid:      mksuid.New().String(),
			NativeID:   fmt.Sprintf("idx-child-ref-%d", i),
			Stack:      "s",
			Label:      fmt.Sprintf("idx-child-ref-%d", i),
			Type:       "AWS::IAM::Role",
			Target:     "test-target",
			Properties: json.RawMessage(childProps),
		}, "cmd-1")
		require.NoError(t, err)
	}
	for i := 0; i < 200; i++ {
		_, err = d.StoreResource(&pkgmodel.Resource{
			Ksuid:      mksuid.New().String(),
			NativeID:   fmt.Sprintf("idx-child-noref-%d", i),
			Stack:      "s",
			Label:      fmt.Sprintf("idx-child-noref-%d", i),
			Type:       "AWS::EC2::Subnet",
			Target:     "test-target",
			Properties: json.RawMessage(`{"CidrBlock":"10.0.0.0/24"}`),
		}, "cmd-1")
		require.NoError(t, err)
	}

	// Update table statistics so the planner has accurate row counts.
	_, err = d.Pool().Exec(ctx, "ANALYZE resources")
	require.NoError(t, err)

	// Disable sequential scans on a dedicated connection so the SET and the
	// EXPLAIN run against the same session, then confirm the overlap predicate
	// resolves to the GIN index.
	conn, err := d.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	_, err = conn.Exec(ctx, "SET enable_seqscan = off")
	require.NoError(t, err)

	explainQuery := `EXPLAIN (FORMAT JSON)
	SELECT data, ksuid, refs
	FROM resources r1
	WHERE refs && $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $2 AND operation != 'reaped'`

	var planJSON string
	err = conn.QueryRow(ctx, explainQuery, []string{parentKsuid}, "delete").Scan(&planJSON)
	require.NoError(t, err)

	assert.Contains(t, planJSON, "idx_resources_refs",
		"EXPLAIN plan must reference idx_resources_refs; got plan:\n%s", planJSON)
}

// TestFindResourcesDependingOnMany_UsesRefsIndex is the equivalent plan guard for
// FindResourcesDependingOnMany, which passes the entire frontier as a single text[]
// parameter. As with the single-KSUID guard, sequential scans are disabled for the
// session so the overlap predicate is asserted to have an indexed path; the version
// anti-join is not required to be scan-free.
func TestFindResourcesDependingOnMany_UsesRefsIndex(t *testing.T) {
	ctx := context.Background()

	adminConn, err := pgx.Connect(ctx, "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(ctx)

	d, cleanup := newTestDatastore(t)
	defer cleanup()
	storeTestTarget(t, d)

	// Seed two parent resources (frontier of size 2).
	parent1Ksuid := mksuid.New().String()
	parent2Ksuid := mksuid.New().String()
	for i, pk := range []string{parent1Ksuid, parent2Ksuid} {
		_, err = d.StoreResource(&pkgmodel.Resource{
			Ksuid: pk, NativeID: fmt.Sprintf("many-parent-%d", i), Stack: "s",
			Label: fmt.Sprintf("many-parent-%d", i),
			Type:  "AWS::S3::Bucket", Target: "test-target",
			Properties: json.RawMessage(fmt.Sprintf(`{"BucketName":"mp%d"}`, i)),
		}, "cmd-0")
		require.NoError(t, err)
	}

	// Seed 200 children, alternating refs between the two parents.
	for i := 0; i < 200; i++ {
		pk := parent1Ksuid
		if i%2 == 1 {
			pk = parent2Ksuid
		}
		childProps := fmt.Sprintf(`{"V":{"$ref":"formae://%s#/Arn","$value":"v"}}`, pk)
		_, err = d.StoreResource(&pkgmodel.Resource{
			Ksuid:      mksuid.New().String(),
			NativeID:   fmt.Sprintf("many-child-%d", i),
			Stack:      "s",
			Label:      fmt.Sprintf("many-child-%d", i),
			Type:       "AWS::IAM::Role",
			Target:     "test-target",
			Properties: json.RawMessage(childProps),
		}, "cmd-1")
		require.NoError(t, err)
	}

	_, err = d.Pool().Exec(ctx, "ANALYZE resources")
	require.NoError(t, err)

	conn, err := d.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	_, err = conn.Exec(ctx, "SET enable_seqscan = off")
	require.NoError(t, err)

	explainQuery := `EXPLAIN (FORMAT JSON)
	SELECT data, ksuid, refs
	FROM resources r1
	WHERE refs && $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $2 AND operation != 'reaped'`

	var planJSON string
	frontier := []string{parent1Ksuid, parent2Ksuid}
	err = conn.QueryRow(ctx, explainQuery, frontier, "delete").Scan(&planJSON)
	require.NoError(t, err)

	assert.Contains(t, planJSON, "idx_resources_refs",
		"EXPLAIN plan must reference idx_resources_refs; got plan:\n%s", planJSON)
}
