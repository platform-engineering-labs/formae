// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A forma resource whose $res reference points at a resource that has been
// deleted (its latest datastore version is a tombstone) must be rejected with
// FormaReferencedResourcesNotFoundError. The triplet must not translate via an
// older version of the deleted row: that produces a dangling $ref whose ksuid
// no live-view lookup can resolve, surfacing as an internal error on every
// subsequent apply touching the stack.
func TestGenerateResourceUpdates_ReferenceToDeletedResourceIsRejected(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}, "test")
	require.NoError(t, err)

	target := &pkgmodel.Target{Label: "test-target", Namespace: "Test", Config: json.RawMessage(`{}`)}

	parent := &pkgmodel.Resource{
		Stack:      "stack-1",
		Label:      "res-a",
		Type:       "Test::Generic::Resource",
		Target:     "test-target",
		NativeID:   "native-parent",
		Properties: json.RawMessage(`{"Name":"res-a"}`),
		Managed:    true,
	}
	_, err = ds.StoreResource(parent, "create-command")
	require.NoError(t, err)

	child := &pkgmodel.Resource{
		Stack:      "stack-1",
		Label:      "child-a",
		Type:       "Test::Generic::ChildResource",
		Target:     "test-target",
		NativeID:   "native-child",
		Properties: json.RawMessage(`{"Name":"child-a","ParentId":"res-a"}`),
		Managed:    true,
	}
	_, err = ds.StoreResource(child, "create-command")
	require.NoError(t, err)

	// The parent is deleted (e.g. an absorbed out-of-band deletion): its
	// latest version is now a tombstone.
	stored, err := ds.LoadResourceByNativeID("native-parent", "Test::Generic::Resource")
	require.NoError(t, err)
	require.NotNil(t, stored)
	_, err = ds.DeleteResource(stored, "delete-command")
	require.NoError(t, err)

	// An apply carrying only the child, whose ParentId references the
	// now-deleted parent.
	forma := &pkgmodel.Forma{
		Resources: []pkgmodel.Resource{
			{
				Stack:  "stack-1",
				Label:  "child-a",
				Type:   "Test::Generic::ChildResource",
				Target: "test-target",
				Properties: json.RawMessage(`{
					"Name": "child-a",
					"ParentId": {
						"$res": true,
						"$label": "res-a",
						"$type": "Test::Generic::Resource",
						"$stack": "stack-1",
						"$property": "Name"
					}
				}`),
			},
		},
	}

	_, err = resource_update.GenerateResourceUpdates(
		forma,
		pkgmodel.CommandApply,
		pkgmodel.FormaApplyModePatch,
		resource_update.FormaCommandSourceUser,
		[]*pkgmodel.Target{target},
		ds,
		nil, nil,
	)

	require.Error(t, err)
	var notFound apimodel.FormaReferencedResourcesNotFoundError
	require.True(t, errors.As(err, &notFound),
		"expected FormaReferencedResourcesNotFoundError, got: %v", err)
}
