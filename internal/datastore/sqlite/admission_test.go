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
