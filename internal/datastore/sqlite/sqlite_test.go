// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore/dstest"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
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
		return dstest.TestDatastore{
			Datastore: ds,
			CleanUpFn: func() error {
				if d, ok := ds.(dssqlite.DatastoreSQLite); ok {
					return d.CleanUp()
				}
				return nil
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
	return dstest.TestDatastore{
		Datastore: ds,
		CleanUpFn: func() error {
			if d, ok := ds.(dssqlite.DatastoreSQLite); ok {
				return d.CleanUp()
			}
			return nil
		},
	}
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
