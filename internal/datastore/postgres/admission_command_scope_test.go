// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestAdmissionCommandScopeMigrationUpDownUp(t *testing.T) {
	ctx := context.Background()
	database := "admission_command_migration_" + strings.ToLower(mksuid.New().String())
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.PostgresDatastore, Postgres: pkgmodel.PostgresConfig{
		Host: "localhost", Port: 5432, User: "postgres", Password: "admin", Database: database,
	}}
	ds, err := NewDatastorePostgresEnsureDatabase(ctx, cfg, "test")
	require.NoError(t, err)
	d := ds.(DatastorePostgres)
	t.Cleanup(func() {
		d.Close()
		admin, connectErr := pgx.Connect(ctx, BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, "postgres"))
		require.NoError(t, connectErr)
		defer func() { require.NoError(t, admin.Close(ctx)) }()
		_, dropErr := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE %s", pgx.Identifier{database}.Sanitize()))
		require.NoError(t, dropErr)
	})

	stack := &pkgmodel.Stack{Label: "migration-" + mksuid.New().String()}
	_, err = d.CreateStack(stack, "migration-test")
	require.NoError(t, err)
	command := &forma_command.FormaCommand{
		ID: mksuid.New().String(), Command: pkgmodel.CommandApply, State: forma_command.CommandStatePending,
		StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Source: forma_command.SourceUser,
		Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}},
	}
	require.NoError(t, d.StoreFormaCommand(command, command.ID))
	guardKey := datastore.AdmissionStackGuardKey(stack.ID)
	readRevision := func() int64 {
		guards, readErr := d.ReadAdmissionRevisions([]string{guardKey})
		require.NoError(t, readErr)
		require.Len(t, guards, 1)
		return guards[0].Revision
	}
	updateModifiedOnly := func() {
		_, updateErr := d.Pool().Exec(ctx, `UPDATE forma_commands SET modified_ts=clock_timestamp() WHERE command_id=$1`, command.ID)
		require.NoError(t, updateErr)
	}
	functionDefinition := func() string {
		var definition string
		queryErr := d.Pool().QueryRow(ctx, `SELECT pg_get_functiondef('admission_forma_commands_update()'::regprocedure)`).Scan(&definition)
		require.NoError(t, queryErr)
		return definition
	}

	require.Contains(t, functionDefinition(), "affected_resource_update_history AS MATERIALIZED")
	before := readRevision()
	updateModifiedOnly()
	require.Equal(t, before, readRevision(), "Up must ignore a modified_ts-only command write")

	migrationDB, err := sql.Open("pgx", BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, database))
	require.NoError(t, err)
	defer func() { require.NoError(t, migrationDB.Close()) }()
	goose.SetBaseFS(datastore.EmbedMigrationsPostgres)
	goose.SetTableName(datastore.MigrationHistoryV2)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Down(migrationDB, "migrations_postgres"))
	require.NotContains(t, functionDefinition(), "affected_resource_update_history AS MATERIALIZED")

	before = readRevision()
	updateModifiedOnly()
	require.Greater(t, readRevision(), before, "Down must restore migration 00031 behavior")

	require.NoError(t, datastore.RunMigrations(migrationDB, "postgres"))
	require.Contains(t, functionDefinition(), "affected_resource_update_history AS MATERIALIZED")
	before = readRevision()
	updateModifiedOnly()
	require.Equal(t, before, readRevision(), "second Up must restore the modified_ts fast path")
}

func TestAdmissionCommandUpdateScopesHistoryBeforeJSON(t *testing.T) {
	for _, unrelatedResources := range []int{0, 7560, 15120} {
		t.Run(fmt.Sprintf("unrelated_%d", unrelatedResources), func(t *testing.T) {
			testAdmissionCommandUpdateScopesHistoryBeforeJSON(t, unrelatedResources)
		})
	}
}

func testAdmissionCommandUpdateScopesHistoryBeforeJSON(t *testing.T, unrelatedResources int) {
	ctx := context.Background()
	database := "admission_command_scope_" + strings.ToLower(mksuid.New().String())
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.PostgresDatastore, Postgres: pkgmodel.PostgresConfig{
		Host: "localhost", Port: 5432, User: "postgres", Password: "admin", Database: database,
	}}
	ds, err := NewDatastorePostgresEnsureDatabase(ctx, cfg, "test")
	require.NoError(t, err)
	d := ds.(DatastorePostgres)
	t.Cleanup(func() {
		d.Close()
		admin, connectErr := pgx.Connect(ctx, BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, "postgres"))
		require.NoError(t, connectErr)
		defer func() { require.NoError(t, admin.Close(ctx)) }()
		_, dropErr := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE %s", pgx.Identifier{database}.Sanitize()))
		require.NoError(t, dropErr)
	})

	stack := &pkgmodel.Stack{Label: "affected-" + mksuid.New().String()}
	_, err = d.CreateStack(stack, "scope-test")
	require.NoError(t, err)
	target := &pkgmodel.Target{Label: "scope-target", Namespace: "AWS", Config: json.RawMessage(`{"region":"literal"}`)}
	_, err = d.CreateTarget(target)
	require.NoError(t, err)
	resourceID := mksuid.New().String()
	now := time.Now().UTC()
	command := &forma_command.FormaCommand{
		ID: mksuid.New().String(), Command: pkgmodel.CommandApply, State: forma_command.CommandStatePending,
		StartTs: now, ModifiedTs: now, Source: forma_command.SourceUser,
		Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}},
		ResourceUpdates: []resource_update.ResourceUpdate{{
			Operation: types.OperationUpdate, State: resource_update.ResourceUpdateStateInProgress,
			Source: resource_update.FormaCommandSourceUser, StackLabel: stack.Label,
			DesiredState: pkgmodel.Resource{Ksuid: resourceID, Stack: stack.Label, Label: "affected", Type: "AWS::S3::Bucket", Target: target.Label, Properties: json.RawMessage(`{"dependency":{"$ref":"formae://scope"}}`)},
		}},
	}
	require.NoError(t, d.StoreFormaCommand(command, command.ID))
	observed := &pkgmodel.Resource{Ksuid: resourceID, Stack: stack.Label, Label: "affected", Type: "AWS::S3::Bucket", Target: target.Label, Properties: json.RawMessage(`{"dependency":{"$ref":"formae://scope"}}`), Managed: true}
	_, err = d.StoreResource(observed, command.ID)
	require.NoError(t, err)

	_, err = d.Pool().Exec(ctx, fmt.Sprintf(`
		BEGIN;
		SET LOCAL session_replication_role=replica;
		INSERT INTO resources(uri,version,command_id,operation,stack,type,label,target,data,ksuid)
		SELECT 'formae://scope-unrelated-'||r::text, v::text, 'unrelated-sync', 'read', 'unmanaged', 'Synthetic::Resource',
		       'unrelated-'||r::text, 'synthetic', jsonb_build_object('Properties',jsonb_build_object('payload',repeat(md5(r::text),100))), 'unrelated-'||r::text
		FROM generate_series(1,%d) r CROSS JOIN generate_series(1,10) v;
		INSERT INTO resource_updates(command_id,ksuid,operation,state,stack_label,source,resource,resource_target)
		SELECT 'unrelated-'||r::text, 'unrelated-resource-'||r::text, 'update','Success','unrelated-stack','user',
		       jsonb_build_object('Properties',jsonb_build_object('payload',repeat(md5(r::text),100)))::text, '{}'
		FROM generate_series(1,%d) r;
		COMMIT;
		ANALYZE resources;
		ANALYZE resource_updates;
		ANALYZE command_stacks;
	`, unrelatedResources/10, unrelatedResources/10))
	require.NoError(t, err)

	var notices []string
	traceCfg, err := pgx.ParseConfig(BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, database))
	require.NoError(t, err)
	traceCfg.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) { notices = append(notices, notice.Message) }
	conn, err := pgx.ConnectConfig(ctx, traceCfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close(ctx)) }()
	_, err = conn.Exec(ctx, `
		LOAD 'auto_explain';
		SET client_min_messages=log;
		SET auto_explain.log_min_duration=0;
		SET auto_explain.log_analyze=on;
		SET auto_explain.log_buffers=on;
		SET auto_explain.log_nested_statements=on;
		SET auto_explain.log_format=json;
	`)
	require.NoError(t, err)

	for i := range 8 {
		state := forma_command.CommandStatePending
		if i%2 == 0 {
			state = forma_command.CommandStateInProgress
		}
		_, err = conn.Exec(ctx, `UPDATE forma_commands SET state=$1, modified_ts=clock_timestamp() WHERE command_id=$2`, state, command.ID)
		require.NoError(t, err)
		notices = notices[:0]
	}
	started := time.Now()
	_, err = conn.Exec(ctx, `UPDATE forma_commands SET state=$1, modified_ts=clock_timestamp() WHERE command_id=$2`, forma_command.CommandStateSuccess, command.ID)
	duration := time.Since(started)
	require.NoError(t, err)

	metrics := admissionNestedPlanMetrics(notices)
	t.Logf("warmed command UPDATE with %d unrelated resource rows: duration=%s nested_plans=%d max_resources_row_work=%.0f max_resource_updates_row_work=%.0f", unrelatedResources, duration, metrics.plans, metrics.maxResources, metrics.maxResourceUpdates)
	require.Positive(t, metrics.plans, "auto_explain must capture the trigger's nested statements")
	require.Positive(t, metrics.resourceNodes, "nested plans must include the affected resources query")
	require.LessOrEqual(t, metrics.maxResources, float64(2), "command UPDATE must filter/materialize affected resources before JSON and label processing")
	require.LessOrEqual(t, metrics.maxResourceUpdates, float64(2), "command UPDATE must filter affected resource-update history before JSON processing")

	command.State = forma_command.CommandStateFailed
	command.ModifiedTs = time.Now().UTC()
	require.NoError(t, d.StoreFormaCommand(command, command.ID), "real StoreFormaCommand must preserve full production persistence semantics")
	stored, err := d.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateFailed, stored.State)
	require.Equal(t, command.Stacks, stored.Stacks)
}

type nestedPlanMetrics struct {
	plans, resourceNodes             int
	maxResources, maxResourceUpdates float64
}

func admissionNestedPlanMetrics(notices []string) nestedPlanMetrics {
	var metrics nestedPlanMetrics
	for _, notice := range notices {
		start := strings.IndexByte(notice, '{')
		if start < 0 {
			continue
		}
		var explained map[string]any
		if json.Unmarshal([]byte(notice[start:]), &explained) != nil {
			continue
		}
		metrics.plans++
		walkAdmissionPlan(explained["Plan"], &metrics)
	}
	return metrics
}

func walkAdmissionPlan(value any, metrics *nestedPlanMetrics) {
	node, ok := value.(map[string]any)
	if !ok {
		return
	}
	relation, _ := node["Relation Name"].(string)
	work := admissionPlanNumber(node["Actual Rows"]) + admissionPlanNumber(node["Rows Removed by Filter"])
	work *= max(1, admissionPlanNumber(node["Actual Loops"]))
	switch relation {
	case "resources":
		metrics.resourceNodes++
		metrics.maxResources = max(metrics.maxResources, work)
	case "resource_updates":
		metrics.maxResourceUpdates = max(metrics.maxResourceUpdates, work)
	}
	children, _ := node["Plans"].([]any)
	for _, child := range children {
		walkAdmissionPlan(child, metrics)
	}
}

func admissionPlanNumber(value any) float64 {
	n, _ := value.(float64)
	return n
}
