// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// resourceRow holds the column values for a single resources table insert.
type resourceRow struct {
	uri       string
	version   string
	commandID string
	operation string
	nativeID  string
	stack     string
	typ       string
	label     string
	target    string
	data      []byte
	managed   bool
	ksuid     string
}

// pgxCopyFromRows implements pgx.CopyFromSource over a []resourceRow.
type pgxCopyFromRows struct {
	rows []resourceRow
	idx  int
}

func (r *pgxCopyFromRows) Next() bool {
	r.idx++
	return r.idx <= len(r.rows)
}

func (r *pgxCopyFromRows) Values() ([]any, error) {
	row := r.rows[r.idx-1]
	return []any{
		row.uri,
		row.version,
		row.commandID,
		row.operation,
		row.nativeID,
		row.stack,
		row.typ,
		row.label,
		row.target,
		pgtype.Text{String: string(row.data), Valid: true},
		row.managed,
		row.ksuid,
	}, nil
}

func (r *pgxCopyFromRows) Err() error { return nil }

// TestListResourceSummaries_ScaleExplain seeds ~25K resources across many stacks
// into a fresh isolated database and then uses EXPLAIN (FORMAT JSON) to verify
// that the filtered summary query is index-served rather than falling back to
// pure sequential scans of the resources relation.
//
// This is the SECONDARY/structural guard for the summary path — the primary
// regression protection is the Go-level guards in the earlier tests. The
// assertion is therefore deliberately robust rather than aggressive.
//
// Primary assertion (structural): when a stack filter is applied, the plan tree
// contains at least one index-served access node on the resources relation.
// The accepted index-served node types are "Index Scan", "Index Only Scan", and
// "Bitmap Index Scan". That proves the summary path uses an index; the real
// regression would be a plan that lost all index usage and went to pure
// sequential scans. We deliberately do NOT assert the planner's anti-join
// strategy (e.g. whether the outer r1 relation is reached via a Seq Scan + hash
// anti-join) — that choice is planner-dependent and asserting it would be
// brittle. Any Seq Scan on r1 is emitted only as an informational log.
//
// Note: the UNFILTERED "list all latest" query necessarily reads every latest
// row — that is inherent to the semantics and is not a regression. This test
// therefore exclusively targets the FILTERED (stack=X) plan, where index usage
// is expected.
func TestListResourceSummaries_ScaleExplain(t *testing.T) {
	ctx := context.Background()

	// Skip when postgres is not available (local runs without a db; CI has it).
	adminConn, err := pgx.Connect(ctx, "postgres://postgres:admin@localhost:5432/postgres")
	if err != nil {
		t.Skipf("Postgres not available: %v", err)
	}
	adminConn.Close(ctx)

	// Create a fresh isolated database so this large seed does not pollute
	// other test suites.
	cfg := &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.PostgresDatastore,
		Postgres: pkgmodel.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "admin",
			Database: fmt.Sprintf("test_scale_%s", mksuid.New().String()),
		},
	}
	ds, err := postgres.NewDatastorePostgresEnsureDatabase(ctx, cfg, "test-scale")
	require.NoError(t, err)

	d, ok := ds.(postgres.DatastorePostgres)
	require.True(t, ok, "expected DatastorePostgres")
	t.Cleanup(func() {
		d.Close()
		_ = d.CleanUp()
	})

	// Connect directly to the new database for bulk COPY and ANALYZE.
	connStr := postgres.BuildConnStr(
		cfg.Postgres.Host, cfg.Postgres.Port,
		cfg.Postgres.User, cfg.Postgres.Password,
		cfg.Postgres.Database,
	)
	conn, err := pgx.Connect(ctx, connStr)
	require.NoError(t, err)
	defer conn.Close(ctx) //nolint:errcheck

	// Seed ~25 000 resources: 250 stacks × 100 resources each.
	// Each resource has a single version row and a distinct URI so the
	// NOT EXISTS self-join produces the expected latest-row result.
	// The "data" column carries a minimal JSON blob — the summary query never
	// reads it, so size does not matter.
	const (
		numStacks         = 250
		resourcesPerStack = 100
		targetStack       = "scale-stack-007" // the stack the EXPLAIN query filters on
		totalResources    = numStacks * resourcesPerStack
	)
	smallData := []byte(`{"k":"v"}`)

	rows := make([]resourceRow, 0, totalResources)
	for s := 0; s < numStacks; s++ {
		stack := fmt.Sprintf("scale-stack-%03d", s)
		for r := 0; r < resourcesPerStack; r++ {
			id := mksuid.New().String()
			rows = append(rows, resourceRow{
				uri:       fmt.Sprintf("formae://scale/%s/r%d", stack, r),
				version:   id, // single version per URI; KSUID gives chronological order
				commandID: fmt.Sprintf("cmd-%s", id),
				operation: "update",
				nativeID:  fmt.Sprintf("native-%s-%d", stack, r),
				stack:     stack,
				typ:       resourceType(r),
				label:     fmt.Sprintf("label-%s-%04d", stack, r),
				target:    "",
				data:      smallData,
				managed:   r%10 != 0, // ~10 % unmanaged for variety
				ksuid:     id,
			})
		}
	}

	// Bulk-load via COPY for speed — avoids 25 000 individual round-trips.
	cols := []string{
		"uri", "version", "command_id", "operation",
		"native_id", "stack", "type", "label",
		"target", "data", "managed", "ksuid",
	}
	n, err := conn.CopyFrom(
		ctx,
		pgx.Identifier{"resources"},
		cols,
		&pgxCopyFromRows{rows: rows},
	)
	require.NoError(t, err)
	require.Equal(t, int64(totalResources), n, "expected all rows to be inserted")

	// Also add a few multi-version URIs for realism (same URI, two versions —
	// only the second should be returned by the summary query).
	extraRows := make([]resourceRow, 0, 10)
	for i := 0; i < 5; i++ {
		baseID := mksuid.New().String()
		laterID := mksuid.New().String()
		uri := fmt.Sprintf("formae://scale/multi-ver/%d", i)
		for _, ver := range []struct{ id, op string }{{baseID, "update"}, {laterID, "update"}} {
			extraRows = append(extraRows, resourceRow{
				uri:       uri,
				version:   ver.id,
				commandID: fmt.Sprintf("cmd-%s", ver.id),
				operation: ver.op,
				nativeID:  fmt.Sprintf("native-multi-%d", i),
				stack:     targetStack,
				typ:       "AWS::EC2::Instance",
				label:     fmt.Sprintf("multi-label-%d", i),
				target:    "",
				data:      smallData,
				managed:   true,
				ksuid:     baseID, // same ksuid for both versions (simulates an update)
			})
		}
	}
	if len(extraRows) > 0 {
		_, err = conn.CopyFrom(
			ctx,
			pgx.Identifier{"resources"},
			cols,
			&pgxCopyFromRows{rows: extraRows},
		)
		require.NoError(t, err)
	}

	// Run ANALYZE so the query planner has up-to-date statistics for the
	// freshly-seeded table. Without this the planner may fall back to
	// sequential scans due to stale/missing statistics.
	_, err = conn.Exec(ctx, "ANALYZE resources")
	require.NoError(t, err)

	// The filtered summary SQL mirrors exactly what ListResourceSummaries
	// builds for a stack=<value> query. It must track the implementation in
	// postgres.go whenever that function changes.
	//
	// The $1 placeholder is the delete-operation literal; $2 is the stack value.
	filteredSummarySQL := `
	SELECT label, stack, type, native_id, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND r1.operation != $1 AND r1.operation != 'reaped'
	AND stack = $2
	ORDER BY type, label`

	explainSQL := fmt.Sprintf("EXPLAIN (FORMAT JSON) %s", filteredSummarySQL)

	var planJSON string
	err = conn.QueryRow(ctx, explainSQL, "delete", targetStack).Scan(&planJSON)
	require.NoError(t, err, "EXPLAIN query failed")

	// Parse the EXPLAIN JSON output. Postgres wraps the plan tree in a
	// top-level JSON array: [{"Plan": {...}, ...}].
	var explainOutput []map[string]any
	require.NoError(t, json.Unmarshal([]byte(planJSON), &explainOutput),
		"failed to parse EXPLAIN JSON output")
	require.NotEmpty(t, explainOutput, "EXPLAIN returned an empty result")

	topPlan, ok := explainOutput[0]["Plan"].(map[string]any)
	require.True(t, ok, "expected top-level 'Plan' key in EXPLAIN output")

	// Collect every plan node from the tree so we can assert on them as a flat slice.
	allNodes := collectPlanNodes(topPlan)

	// The node types that count as index-served access on the resources
	// relation. We accept any of them and deliberately do not assert the exact
	// join strategy the planner chooses (that is planner-dependent).
	indexServedNodeTypes := map[string]bool{
		"Index Scan":        true,
		"Index Only Scan":   true,
		"Bitmap Index Scan": true,
	}

	// Primary assertion: the filtered summary plan is index-served — at least one
	// index-served access node touches the resources relation. Losing all index
	// usage (pure sequential scans) is the regression this guards against.
	indexServed := false
	for _, node := range allNodes {
		nodeType, _ := node["Node Type"].(string)
		relation, _ := node["Relation Name"].(string)

		if indexServedNodeTypes[nodeType] && relation == "resources" {
			indexServed = true
		}

		// Informational only — the planner may legitimately choose a Seq Scan +
		// hash anti-join for the outer r1 relation even while index-serving the
		// filter, so we never assert on this.
		if nodeType == "Seq Scan" && relation == "resources" {
			alias, _ := node["Alias"].(string)
			t.Logf("plan contains a Seq Scan on resources (alias %q) — informational, not asserted", alias)
		}
	}

	if !indexServed {
		t.Errorf(
			"filtered summary query plan is not index-served: expected at least one of "+
				"Index Scan / Index Only Scan / Bitmap Index Scan on the resources relation. "+
				"Full plan:\n%s", planJSON,
		)
	}
}

// collectPlanNodes recursively walks the EXPLAIN JSON plan tree and returns
// every plan node (including sub-plans and initplans) as a flat slice.
func collectPlanNodes(node map[string]any) []map[string]any {
	nodes := []map[string]any{node}
	if plans, ok := node["Plans"].([]any); ok {
		for _, p := range plans {
			if child, ok := p.(map[string]any); ok {
				nodes = append(nodes, collectPlanNodes(child)...)
			}
		}
	}
	return nodes
}

// resourceType returns a varied resource type string based on the resource index
// so the seeded data spans multiple types, making the fixture realistic.
func resourceType(r int) string {
	types := []string{
		"AWS::S3::Bucket",
		"AWS::EC2::Instance",
		"AWS::EC2::VPC",
		"AWS::IAM::Role",
		"AWS::RDS::DBInstance",
	}
	return types[r%len(types)]
}
