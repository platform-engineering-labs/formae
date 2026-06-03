// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package mssql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const resourcesTestBaseDSN = "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable"

// newResourcesTestDS creates a brand-new database on the local test container,
// returns a connected DatastoreMSSQL, a raw *sql.DB to the SAME database (for
// direct INSERTs that need precise control over the version column), and a
// cleanup func.
func newResourcesTestDS(t *testing.T) (datastore.Datastore, *sql.DB, func()) {
	t.Helper()

	master, err := sql.Open("sqlserver", resourcesTestBaseDSN+"&database=master")
	if err != nil {
		t.Skipf("mssql driver open failed: %v", err)
	}
	if err := master.Ping(); err != nil {
		_ = master.Close()
		t.Skipf("local mssql not reachable on :1433: %v", err)
	}

	dbName := fmt.Sprintf("formae_resources_%d", time.Now().UnixNano())
	if _, err := master.Exec(fmt.Sprintf("CREATE DATABASE [%s]", dbName)); err != nil {
		_ = master.Close()
		t.Fatalf("create test db: %v", err)
	}

	dropDB := func() {
		_, _ = master.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName))
		_, _ = master.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName))
		_ = master.Close()
	}

	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: "mssql",
		MSSQL: pkgmodel.MSSQLConfig{
			AuthMode: pkgmodel.MSSQLAuthSQL,
			Host:     "localhost",
			Port:     1433,
			User:     "sa",
			Password: "Formae_Test_1234!",
			Database: dbName,
		},
	}

	ds, err := mssql.NewDatastoreMSSQL(context.Background(), cfg, "test-agent")
	if err != nil {
		dropDB()
		t.Fatalf("NewDatastoreMSSQL: %v", err)
	}

	raw, err := sql.Open("sqlserver", resourcesTestBaseDSN+"&database="+dbName)
	if err != nil {
		ds.Close()
		dropDB()
		t.Fatalf("open raw db: %v", err)
	}

	cleanup := func() {
		_ = raw.Close()
		ds.Close()
		dropDB()
	}
	return ds, raw, cleanup
}

// makeResourcesTestResource builds a minimal resource. NativeID + Type identify
// the row in storeResource's existence lookup; Properties make ResourcesAreEqual
// meaningful.
func makeResourcesTestResource(ksuid, label, stack, nativeID string) *pkgmodel.Resource {
	return &pkgmodel.Resource{
		Ksuid:      ksuid,
		Label:      label,
		Type:       "aws::s3::bucket",
		Stack:      stack,
		NativeID:   nativeID,
		Target:     "tgt-1",
		Managed:    true,
		Properties: json.RawMessage(fmt.Sprintf(`{"name":%q}`, label)),
	}
}

func TestMSSQLResourcesRoundTrip(t *testing.T) {
	ds, _, cleanup := newResourcesTestDS(t)
	defer cleanup()

	res := makeResourcesTestResource("k-rt-1", "bucket-rt-1", "stack-rt", "native-rt-1")

	if _, err := ds.StoreResource(res, "cmd-rt-1"); err != nil {
		t.Fatalf("StoreResource: %v", err)
	}

	loaded, err := ds.LoadResource(res.URI())
	if err != nil {
		t.Fatalf("LoadResource: %v", err)
	}
	if loaded == nil {
		t.Fatalf("LoadResource returned nil")
	}
	if loaded.Ksuid != "k-rt-1" || loaded.Label != "bucket-rt-1" {
		t.Errorf("LoadResource = %+v, want ksuid k-rt-1 label bucket-rt-1", loaded)
	}

	byID, err := ds.LoadResourceById("k-rt-1")
	if err != nil {
		t.Fatalf("LoadResourceById: %v", err)
	}
	if byID == nil || byID.Label != "bucket-rt-1" {
		t.Errorf("LoadResourceById = %+v", byID)
	}

	byNative, err := ds.LoadResourceByNativeID("native-rt-1", "aws::s3::bucket")
	if err != nil {
		t.Fatalf("LoadResourceByNativeID: %v", err)
	}
	if byNative == nil || byNative.Ksuid != "k-rt-1" {
		t.Errorf("LoadResourceByNativeID = %+v", byNative)
	}

	latest, err := ds.LatestLabelForResource("bucket-rt-1")
	if err != nil {
		t.Fatalf("LatestLabelForResource: %v", err)
	}
	if latest != "bucket-rt-1" {
		t.Errorf("LatestLabelForResource = %q, want bucket-rt-1", latest)
	}

	all, err := ds.LoadAllResources()
	if err != nil {
		t.Fatalf("LoadAllResources: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("LoadAllResources len = %d, want 1", len(all))
	}

	byStack, err := ds.LoadResourcesByStack("stack-rt")
	if err != nil {
		t.Fatalf("LoadResourcesByStack: %v", err)
	}
	if len(byStack) != 1 {
		t.Fatalf("LoadResourcesByStack len = %d, want 1", len(byStack))
	}

	allByStack, err := ds.LoadAllResourcesByStack()
	if err != nil {
		t.Fatalf("LoadAllResourcesByStack: %v", err)
	}
	if len(allByStack["stack-rt"]) != 1 {
		t.Fatalf("LoadAllResourcesByStack[stack-rt] len = %d, want 1", len(allByStack["stack-rt"]))
	}

	q, err := ds.QueryResources(&datastore.ResourceQuery{
		Stack: &datastore.QueryItem[string]{Item: "stack-rt", Constraint: datastore.Required},
		Type:  &datastore.QueryItem[string]{Item: "AWS::S3::Bucket", Constraint: datastore.Required},
	})
	if err != nil {
		t.Fatalf("QueryResources: %v", err)
	}
	if len(q) != 1 || q[0].Ksuid != "k-rt-1" {
		t.Fatalf("QueryResources = %+v, want [k-rt-1]", q)
	}

	// DeleteResource writes a tombstone version. The latest-version-wins views
	// (LoadResourcesByStack / LoadAllResources / QueryResources) must then exclude
	// the resource. (LoadResource intentionally returns the latest non-delete row
	// and is not a deletion check — same behavior as postgres/sqlite.)
	if _, err := ds.DeleteResource(res, "cmd-rt-del"); err != nil {
		t.Fatalf("DeleteResource: %v", err)
	}
	if got, _ := ds.LoadResourcesByStack("stack-rt"); len(got) != 0 {
		t.Errorf("LoadResourcesByStack after delete len = %d, want 0", len(got))
	}
	if got, _ := ds.LoadAllResources(); len(got) != 0 {
		t.Errorf("LoadAllResources after delete len = %d, want 0", len(got))
	}
	if got, _ := ds.QueryResources(&datastore.ResourceQuery{
		Stack: &datastore.QueryItem[string]{Item: "stack-rt", Constraint: datastore.Required},
	}); len(got) != 0 {
		t.Errorf("QueryResources after delete len = %d, want 0", len(got))
	}
}

func TestMSSQLResourcesDependsOn(t *testing.T) {
	ds, _, cleanup := newResourcesTestDS(t)
	defer cleanup()

	dep := makeResourcesTestResource("k-dep-target", "dep-target", "stack-dep", "native-dep-target")
	if _, err := ds.StoreResource(dep, "cmd-dep-1"); err != nil {
		t.Fatalf("StoreResource dep: %v", err)
	}

	referrer := &pkgmodel.Resource{
		Ksuid:      "k-dep-ref",
		Label:      "dep-ref",
		Type:       "aws::s3::bucket",
		Stack:      "stack-dep",
		NativeID:   "native-dep-ref",
		Target:     "tgt-1",
		Managed:    true,
		Properties: json.RawMessage(`{"bucketRef":{"$ref":"formae://k-dep-target#properties.id"}}`),
	}
	if _, err := ds.StoreResource(referrer, "cmd-dep-2"); err != nil {
		t.Fatalf("StoreResource referrer: %v", err)
	}

	deps, err := ds.FindResourcesDependingOn("k-dep-target")
	if err != nil {
		t.Fatalf("FindResourcesDependingOn: %v", err)
	}
	if len(deps) != 1 || deps[0].Ksuid != "k-dep-ref" {
		t.Fatalf("FindResourcesDependingOn = %+v, want [k-dep-ref]", deps)
	}

	many, err := ds.FindResourcesDependingOnMany([]string{"k-dep-target", "k-nonexistent"})
	if err != nil {
		t.Fatalf("FindResourcesDependingOnMany: %v", err)
	}
	if len(many["k-dep-target"]) != 1 || many["k-dep-target"][0].Ksuid != "k-dep-ref" {
		t.Fatalf("FindResourcesDependingOnMany[k-dep-target] = %+v", many["k-dep-target"])
	}
	if len(many["k-nonexistent"]) != 0 {
		t.Errorf("FindResourcesDependingOnMany[k-nonexistent] = %+v, want empty", many["k-nonexistent"])
	}
}

// TestMSSQLResourcesExcludesDeletedWhenVersionsMixCase exercises the latest-
// version subquery's COLLATE Latin1_General_BIN2. The earlier UPDATE row has an
// uppercase letter; the chronologically-later DELETE row has a lowercase letter.
// Under MSSQL's default case-insensitive collation, 'U' sorts before 'f', so the
// uncollated comparison would pick the UPDATE row as latest and leak the deleted
// resource back as managed. The binary collation orders byte-for-byte, so the
// DELETE row wins and the resource is correctly excluded.
func TestMSSQLResourcesExcludesDeletedWhenVersionsMixCase(t *testing.T) {
	ds, raw, cleanup := newResourcesTestDS(t)
	defer cleanup()

	// Byte order: 'U' (0x55) < 'f' (0x66), so deleteVersion > updateVersion under BIN2.
	updateVersion := "2wQUVeqpcVTSF4ROlhC9N4kMUPk"
	deleteVersion := "2wQfTRyuifVOB5LKSwee7d8aErH"

	uri := "formae://k-mixcase"
	data := `{"Schema":{},"Properties":{}}`

	insert := func(version, operation string) {
		_, err := raw.Exec(`INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12)`,
			uri, version, "cmd-"+operation, operation, "native-mix", "stack-mix", "type-mix", "label-mix", "tgt-mix", data, true, "k-mixcase")
		if err != nil {
			t.Fatalf("direct insert (%s): %v", operation, err)
		}
	}

	insert(updateVersion, "update")
	insert(deleteVersion, "delete")

	var localeGT bool
	if err := raw.QueryRow(`SELECT CASE WHEN 'U' > 'f' THEN 1 ELSE 0 END`).Scan(&localeGT); err != nil {
		t.Fatalf("collation precondition query: %v", err)
	}
	if !localeGT {
		t.Fatalf("test precondition: default collation must treat 'U' as greater than 'f'")
	}

	results, err := ds.LoadResourcesByStack("stack-mix")
	if err != nil {
		t.Fatalf("LoadResourcesByStack: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("LoadResourcesByStack = %+v, want empty (later delete must win under BIN2 collation)", results)
	}

	if all, _ := ds.LoadAllResources(); len(all) != 0 {
		t.Errorf("LoadAllResources = %+v, want empty", all)
	}
	if q, _ := ds.QueryResources(&datastore.ResourceQuery{}); len(q) != 0 {
		t.Errorf("QueryResources = %+v, want empty", q)
	}
}
