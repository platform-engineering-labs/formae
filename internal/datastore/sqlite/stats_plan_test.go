// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResourceErrorsQueryPlanIsIndexServed asserts the shipped resource-errors
// query keeps the access strategy it was chosen for: SQLite flattens the live
// relation into correlated index probes rather than materialising it. The plan
// must reach the resources rows through idx_ksuid and carry no co-routine — a
// materialised live relation scans and sorts the whole resources table on every
// call, whatever the failure count, which is the shape this query was measured
// against and rejected.
//
// EXPLAIN QUERY PLAN needs no rows, so this asserts the plan the planner picks
// for the statement rather than timings on a seeded fixture.
func TestResourceErrorsQueryPlanIsIndexServed(t *testing.T) {
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}
	ds, err := NewDatastoreSQLite(context.Background(), cfg, "test")
	require.NoError(t, err)
	d, _ := ds.(DatastoreSQLite)
	defer d.CleanUp() //nolint:errcheck

	rows, err := d.Conn().Query("EXPLAIN QUERY PLAN "+resourceErrorsQuery,
		resource_update.OperationDelete,
		types.ResourceUpdateStateFailed,
		types.ResourceUpdateStateFailed,
		types.ResourceUpdateStateSuccess,
		types.ResourceUpdateStateFailed,
	)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notUsed, &detail))
		plan = append(plan, detail)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, plan, "EXPLAIN QUERY PLAN returned no rows")

	joined := strings.Join(plan, "\n")
	t.Logf("EXPLAIN QUERY PLAN:\n%s", joined)

	assert.Contains(t, joined, "idx_ksuid",
		"the live-inventory lookup must be served by idx_ksuid:\n%s", joined)
	assert.NotContains(t, joined, "CO-ROUTINE",
		"the live relation must stay flattened, not materialised:\n%s", joined)
	for _, detail := range plan {
		assert.False(t, strings.HasPrefix(detail, "SCAN "),
			"every table access must be an index search, got %q:\n%s", detail, joined)
	}
}
