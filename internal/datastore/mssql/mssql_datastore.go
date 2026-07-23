// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	json "github.com/goccy/go-json"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb/azuread"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

var mssqlTracer trace.Tracer

func init() {
	mssqlTracer = otel.Tracer("formae/datastore/mssql")
}

// DatastoreMSSQL implements datastore.Datastore against Microsoft SQL Server.
type DatastoreMSSQL struct {
	conn    *sql.DB
	agentID string
	cfg     *pkgmodel.DatastoreConfig
	ctx     context.Context
}

func NewDatastoreMSSQL(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
	driverName, dsn, err := BuildConnection(cfg.MSSQL)
	if err != nil {
		return nil, fmt.Errorf("mssql: build connection: %w", err)
	}

	conn, err := otelsql.Open(driverName, dsn,
		otelsql.WithAttributes(semconv.DBSystemMSSQL),
		otelsql.WithSpanOptions(otelsql.SpanOptions{DisableErrSkip: true}),
	)
	if err != nil {
		return nil, fmt.Errorf("mssql: open connection: %w", err)
	}

	// Pkl supplies these defaults (4 / 1h); the fallback catches Go-direct
	// callers (tests, programmatic configs) whose zero values would otherwise
	// mean "unlimited" in database/sql.
	maxOpen := cfg.MSSQL.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 4
	}
	connLifetime := cfg.MSSQL.ConnMaxLifetime
	if connLifetime <= 0 {
		connLifetime = time.Hour
	}
	conn.SetMaxOpenConns(maxOpen)
	conn.SetConnMaxLifetime(connLifetime)

	// Register pool stats (open / in-use / idle / wait counts) on the global meter
	if _, err := otelsql.RegisterDBStatsMetrics(conn,
		otelsql.WithAttributes(semconv.DBSystemMSSQL),
	); err != nil {
		slog.Warn("mssql: failed to register DB pool stats", "error", err)
	}

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mssql: ping: %w", err)
	}

	if err := datastore.RunMigrations(conn, "mssql"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mssql: run migrations: %w", err)
	}

	slog.Info("Started MSSQL datastore", "host", cfg.MSSQL.Host, "database", cfg.MSSQL.Database)

	return &DatastoreMSSQL{conn: conn, agentID: agentID, cfg: cfg, ctx: ctx}, nil
}

func (d *DatastoreMSSQL) Close() {
	if err := d.conn.Close(); err != nil {
		slog.Error("Error closing MSSQL connection", "error", err)
	}
}

// Conn returns the underlying *sql.DB. Intended for tests that need direct
// SQL access (e.g. to seed rows with specific version strings).
func (d *DatastoreMSSQL) Conn() *sql.DB { return d.conn }

// placeholders returns "@p<start>,@p<start+1>,...,@p<start+n-1>".
func placeholders(start, n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("@p%d", start+i)
	}
	return strings.Join(parts, ",")
}

// extendMSSQLQueryString appends a WHERE clause for the given query item.
//
// sqlPart takes a "%s" for the operator and "%d" for the ordinal placeholder
// (e.g. " AND label %s @p%d{esc}"). For multi-valued items (Item + ExtraItems)
// the inner clause is replicated once per value and joined with OR (or AND
// when the constraint is Excluded). String values may carry `*` wildcards;
// literal `_`/`%`/`\` are escaped and paired with the {esc} → `ESCAPE '\'`.
func extendMSSQLQueryString[T any](queryStr string, queryItem *datastore.QueryItem[T], sqlPart string, args *[]any) string {
	if queryItem == nil {
		return queryStr
	}

	values := allQueryItemValues(queryItem)
	if len(values) == 0 {
		return queryStr
	}

	isExcluded := queryItem.Constraint == datastore.Excluded

	if len(values) == 1 {
		op, operand, isLike := mssqlOpAndOperand(values[0], isExcluded)
		clause := resolveEscapeMarker(fmt.Sprintf(sqlPart, op, len(*args)+1), isLike)
		queryStr += clause
		*args = append(*args, operand)
		return queryStr
	}

	innerTemplate := strings.TrimPrefix(sqlPart, " AND ")
	clauses := make([]string, 0, len(values))
	for _, v := range values {
		op, operand, isLike := mssqlOpAndOperand(v, isExcluded)
		clause := resolveEscapeMarker(fmt.Sprintf(innerTemplate, op, len(*args)+1), isLike)
		clauses = append(clauses, clause)
		*args = append(*args, operand)
	}

	glue := " OR "
	if isExcluded {
		glue = " AND "
	}
	return queryStr + " AND (" + strings.Join(clauses, glue) + ")"
}

// allQueryItemValues flattens Item + ExtraItems into a single []any.
func allQueryItemValues[T any](qi *datastore.QueryItem[T]) []any {
	values := make([]any, 0, 1+len(qi.ExtraItems))
	values = append(values, any(qi.Item))
	for _, e := range qi.ExtraItems {
		values = append(values, any(e))
	}
	return values
}

// mssqlOpAndOperand resolves the operator and bound value for one term.
// Any `*` in a string flips the operator to LIKE; literal `_`/`%`/`\` are escaped.
func mssqlOpAndOperand(v any, isExcluded bool) (op string, operand any, isLike bool) {
	s, isString := v.(string)
	if !isString {
		if b, ok := v.(bool); ok {
			return mssqlEqOp(isExcluded), b, false
		}
		return mssqlEqOp(isExcluded), fmt.Sprintf("%v", v), false
	}
	if !strings.Contains(s, "*") {
		return mssqlEqOp(isExcluded), s, false
	}
	likeOp := "LIKE"
	if isExcluded {
		likeOp = "NOT LIKE"
	}
	return likeOp, mssqlLikePattern(s), true
}

func mssqlEqOp(isExcluded bool) string {
	if isExcluded {
		return "!="
	}
	return "="
}

// mssqlLikePattern translates `*` to `%` and escapes literal `_`/`%`/`\`.
// Emitted clauses must be paired with the ESCAPE suffix (see {esc} marker).
func mssqlLikePattern(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "%", "\\%")
	escaped = strings.ReplaceAll(escaped, "_", "\\_")
	return strings.ReplaceAll(escaped, "*", "%")
}

// MSSQL LIKE has no default escape character, so we attach `ESCAPE '\'`
// (matching the sqlite pattern) at the position the {esc} marker indicates.
const (
	escapeMarker          = "{esc}"
	mssqlLikeEscapeSuffix = ` ESCAPE '\'`
)

func resolveEscapeMarker(clause string, isLike bool) string {
	if isLike {
		return strings.ReplaceAll(clause, escapeMarker, mssqlLikeEscapeSuffix)
	}
	return strings.ReplaceAll(clause, escapeMarker, "")
}

// Column order is fixed and consumed by scanJoinedRow.
const formaCommandWithResourceUpdatesQueryBase = `
SELECT
	fc.command_id, fc.timestamp, fc.command, fc.state, fc.client_id,
	fc.description_text, fc.description_confirm, fc.config_mode, fc.config_force, fc.config_simulate,
	fc.target_updates, fc.stack_updates, fc.policy_updates, fc.modified_ts, fc.source,
	ru.ksuid, ru.operation, ru.state, ru.start_ts, ru.modified_ts,
	ru.retries, ru.remaining, ru.version, ru.stack_label, ru.group_id, ru.source,
	ru.resource, ru.resource_target, ru.existing_resource, ru.existing_target,
	ru.progress_result, ru.most_recent_progress,
	ru.remaining_resolvables, ru.reference_labels, ru.previous_properties,
	ru.is_cascade, ru.cascade_source
FROM forma_commands fc
LEFT JOIN resource_updates ru ON fc.command_id = ru.command_id`

const resourceUpdateOrderBy = " ORDER BY fc.timestamp DESC, ru.ksuid ASC"

// scanJoinedRow scans one row of the FormaCommand LEFT JOIN ResourceUpdate query.
func scanJoinedRow(rows *sql.Rows) (*forma_command.FormaCommand, *resource_update.ResourceUpdate, error) {
	var cmd forma_command.FormaCommand
	var commandID, fcCommand, fcState string
	var fcTimestamp time.Time
	var fcClientID *string
	var descriptionText *string
	var descriptionConfirm *bool
	var configMode *string
	var configForce, configSimulate *bool
	var targetUpdatesJSON, stackUpdatesJSON, policyUpdatesJSON []byte
	var fcModifiedTs *time.Time
	var fcSource *string

	var ruKsuid, ruOperation, ruState *string
	var ruStartTs, ruModifiedTs *time.Time
	var ruRetries, ruRemaining *int64
	var ruVersion, ruStackLabel, ruGroupID, ruSource *string
	var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
	var progressResultJSON, mostRecentProgressJSON []byte
	var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte
	var ruIsCascade *bool
	var ruCascadeSource *string

	err := rows.Scan(
		&commandID, &fcTimestamp, &fcCommand, &fcState, &fcClientID,
		&descriptionText, &descriptionConfirm, &configMode, &configForce, &configSimulate,
		&targetUpdatesJSON, &stackUpdatesJSON, &policyUpdatesJSON, &fcModifiedTs, &fcSource,
		&ruKsuid, &ruOperation, &ruState, &ruStartTs, &ruModifiedTs,
		&ruRetries, &ruRemaining, &ruVersion, &ruStackLabel, &ruGroupID, &ruSource,
		&resourceJSON, &resourceTargetJSON, &existingResourceJSON, &existingTargetJSON,
		&progressResultJSON, &mostRecentProgressJSON,
		&remainingResolvablesJSON, &referenceLabelsJSON, &previousPropertiesJSON,
		&ruIsCascade, &ruCascadeSource,
	)
	if err != nil {
		return nil, nil, err
	}

	cmd.ID = commandID
	cmd.StartTs = fcTimestamp.UTC()
	cmd.Command = pkgmodel.Command(fcCommand)
	cmd.State = forma_command.CommandState(fcState)
	if fcClientID != nil {
		cmd.ClientID = *fcClientID
	}
	if descriptionText != nil {
		cmd.Description.Text = *descriptionText
	}
	cmd.Description.Confirm = descriptionConfirm != nil && *descriptionConfirm
	if configMode != nil {
		cmd.Config.Mode = pkgmodel.FormaApplyMode(*configMode)
	}
	cmd.Config.Force = configForce != nil && *configForce
	cmd.Config.Simulate = configSimulate != nil && *configSimulate
	if fcModifiedTs != nil {
		cmd.ModifiedTs = fcModifiedTs.UTC()
	}
	if fcSource != nil {
		cmd.Source = forma_command.Source(*fcSource)
	}

	if len(targetUpdatesJSON) > 0 {
		if err := json.Unmarshal(targetUpdatesJSON, &cmd.TargetUpdates); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal target updates: %w", err)
		}
	}
	if len(stackUpdatesJSON) > 0 {
		if err := json.Unmarshal(stackUpdatesJSON, &cmd.StackUpdates); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal stack updates: %w", err)
		}
	}
	if len(policyUpdatesJSON) > 0 {
		if err := json.Unmarshal(policyUpdatesJSON, &cmd.PolicyUpdates); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal policy updates: %w", err)
		}
	}

	// LEFT JOIN may yield no ResourceUpdate for a command.
	if ruKsuid == nil {
		return &cmd, nil, nil
	}

	var ru resource_update.ResourceUpdate
	if ruOperation != nil {
		ru.Operation = types.OperationType(*ruOperation)
	}
	if ruState != nil {
		ru.State = resource_update.ResourceUpdateState(*ruState)
	}
	if ruStartTs != nil {
		ru.StartTs = ruStartTs.UTC()
	}
	if ruModifiedTs != nil {
		ru.ModifiedTs = ruModifiedTs.UTC()
	}
	if ruRetries != nil {
		ru.Retries = uint16(*ruRetries)
	}
	if ruRemaining != nil {
		ru.Remaining = int16(*ruRemaining)
	}
	if ruVersion != nil {
		ru.Version = *ruVersion
	}
	if ruStackLabel != nil {
		ru.StackLabel = *ruStackLabel
	}
	if ruGroupID != nil {
		ru.GroupID = *ruGroupID
	}
	if ruSource != nil {
		ru.Source = resource_update.FormaCommandSource(*ruSource)
	}

	if len(resourceJSON) > 0 {
		if err := json.Unmarshal(resourceJSON, &ru.DesiredState); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal resource: %w", err)
		}
		ru.DesiredState.Ksuid = *ruKsuid
	}
	if len(resourceTargetJSON) > 0 {
		if err := json.Unmarshal(resourceTargetJSON, &ru.ResourceTarget); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal resource target: %w", err)
		}
	}
	if len(existingResourceJSON) > 0 {
		if err := json.Unmarshal(existingResourceJSON, &ru.PriorState); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal existing resource: %w", err)
		}
	}
	if len(existingTargetJSON) > 0 {
		if err := json.Unmarshal(existingTargetJSON, &ru.ExistingTarget); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal existing target: %w", err)
		}
	}
	if len(progressResultJSON) > 0 {
		if err := json.Unmarshal(progressResultJSON, &ru.ProgressResult); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal progress result: %w", err)
		}
	}
	if len(mostRecentProgressJSON) > 0 {
		if err := json.Unmarshal(mostRecentProgressJSON, &ru.MostRecentProgressResult); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal most recent progress: %w", err)
		}
	}
	if len(remainingResolvablesJSON) > 0 {
		if err := json.Unmarshal(remainingResolvablesJSON, &ru.RemainingResolvables); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal remaining resolvables: %w", err)
		}
	}
	if len(referenceLabelsJSON) > 0 {
		if err := json.Unmarshal(referenceLabelsJSON, &ru.ReferenceLabels); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal reference labels: %w", err)
		}
	}

	ru.PreviousProperties = previousPropertiesJSON

	if ruIsCascade != nil {
		ru.IsCascade = *ruIsCascade
	}
	if ruCascadeSource != nil {
		ru.CascadeSource = *ruCascadeSource
	}

	return &cmd, &ru, nil
}

// loadFormaCommandsFromJoinedRows groups ResourceUpdates by command, preserving
// row order.
func loadFormaCommandsFromJoinedRows(rows *sql.Rows) ([]*forma_command.FormaCommand, error) {
	defer func() { _ = rows.Close() }()

	commandMap := make(map[string]*forma_command.FormaCommand)
	var commandOrder []string

	for rows.Next() {
		cmd, ru, err := scanJoinedRow(rows)
		if err != nil {
			return nil, err
		}

		existing, found := commandMap[cmd.ID]
		if !found {
			commandMap[cmd.ID] = cmd
			commandOrder = append(commandOrder, cmd.ID)
			existing = cmd
		}

		if ru != nil {
			existing.ResourceUpdates = append(existing.ResourceUpdates, *ru)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]*forma_command.FormaCommand, 0, len(commandOrder))
	for _, id := range commandOrder {
		result = append(result, commandMap[id])
	}
	return result, nil
}

// StoreFormaCommand upserts the command row atomically via
// UPDATE WITH (UPDLOCK, SERIALIZABLE); IF @@ROWCOUNT=0 INSERT — MSSQL's
// stand-in for Postgres's ON CONFLICT.
func (d *DatastoreMSSQL) StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	ctx, span := mssqlTracer.Start(context.Background(), "StoreFormaCommand")
	defer span.End()

	for _, r := range fa.ResourceUpdates {
		if r.DesiredState.Properties == nil {
			slog.Debug("Resource properties are empty for resource", "resourceLabel", r.DesiredState.Label, "commandID", commandID)
		}
	}

	targetUpdatesJSON, err := json.Marshal(fa.TargetUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal target updates: %w", err)
	}
	stackUpdatesJSON, err := json.Marshal(fa.StackUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal stack updates: %w", err)
	}
	policyUpdatesJSON, err := json.Marshal(fa.PolicyUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal policy updates: %w", err)
	}

	args := []any{
		commandID, fa.StartTs.UTC(), string(fa.Command), string(fa.State), formae.Version,
		fa.ClientID, d.agentID, fa.Description.Text, fa.Description.Confirm,
		string(fa.Config.Mode), fa.Config.Force, fa.Config.Simulate,
		string(targetUpdatesJSON), string(stackUpdatesJSON), string(policyUpdatesJSON), fa.ModifiedTs.UTC(),
		string(fa.Source),
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin upsert tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	updateQuery := fmt.Sprintf(`
	UPDATE %[1]s WITH (UPDLOCK, SERIALIZABLE) SET
		timestamp = @p2, command = @p3, state = @p4, agent_version = @p5,
		client_id = @p6, agent_id = @p7, description_text = @p8,
		description_confirm = @p9, config_mode = @p10, config_force = @p11,
		config_simulate = @p12, target_updates = @p13, stack_updates = @p14,
		policy_updates = @p15, modified_ts = @p16, source = @p17
	WHERE command_id = @p1`, datastore.CommandsTable)

	res, err := tx.ExecContext(ctx, updateQuery, args...)
	if err != nil {
		slog.Error("failed to store FormaCommand (update)", "error", err)
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		insertQuery := fmt.Sprintf(`
		INSERT INTO %[1]s
			(command_id, timestamp, command, state, agent_version, client_id, agent_id,
			 description_text, description_confirm, config_mode, config_force, config_simulate,
			 target_updates, stack_updates, policy_updates, modified_ts, source)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13, @p14, @p15, @p16, @p17)`,
			datastore.CommandsTable)
		if _, err := tx.ExecContext(ctx, insertQuery, args...); err != nil {
			slog.Error("failed to store FormaCommand (insert)", "error", err)
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert tx: %w", err)
	}
	committed = true

	if len(fa.ResourceUpdates) > 0 && fa.Command != pkgmodel.CommandSync {
		if err := d.BulkStoreResourceUpdates(commandID, fa.ResourceUpdates); err != nil {
			return fmt.Errorf("failed to store resource updates: %w", err)
		}
	}

	return nil
}

func (d *DatastoreMSSQL) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadFormaCommands")
	defer span.End()

	rows, err := d.conn.QueryContext(ctx, formaCommandWithResourceUpdatesQueryBase+resourceUpdateOrderBy)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRows(rows)
}

func (d *DatastoreMSSQL) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadIncompleteFormaCommands")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBase + " WHERE fc.command != 'sync' AND fc.state IN (@p1, @p2)" + resourceUpdateOrderBy
	rows, err := d.conn.QueryContext(ctx, query,
		string(forma_command.CommandStateNotStarted), string(forma_command.CommandStateInProgress))
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRows(rows)
}

func (d *DatastoreMSSQL) DeleteFormaCommand(_ *forma_command.FormaCommand, commandID string) error {
	ctx, span := mssqlTracer.Start(context.Background(), "DeleteFormaCommand")
	defer span.End()

	if _, err := d.conn.ExecContext(ctx, "DELETE FROM resource_updates WHERE command_id = @p1", commandID); err != nil {
		return fmt.Errorf("failed to delete resource_updates: %w", err)
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE command_id = @p1", datastore.CommandsTable)
	_, err := d.conn.ExecContext(ctx, query, commandID)
	return err
}

func (d *DatastoreMSSQL) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetFormaCommandByCommandID")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBase + " WHERE fc.command_id = @p1" + resourceUpdateOrderBy
	rows, err := d.conn.QueryContext(ctx, query, commandID)
	if err != nil {
		return nil, err
	}
	commands, err := loadFormaCommandsFromJoinedRows(rows)
	if err != nil {
		return nil, err
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("forma command not found: %v", commandID)
	}
	return commands[0], nil
}

func (d *DatastoreMSSQL) GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetMostRecentFormaCommandByClientID")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBase + `
		WHERE fc.command_id = (
			SELECT TOP (1) command_id FROM forma_commands
			WHERE client_id = @p1
			ORDER BY timestamp DESC
		)
		ORDER BY ru.ksuid ASC`
	rows, err := d.conn.QueryContext(ctx, query, clientID)
	if err != nil {
		return nil, err
	}
	commands, err := loadFormaCommandsFromJoinedRows(rows)
	if err != nil {
		return nil, err
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("no forma commands found for client: %v", clientID)
	}
	return commands[0], nil
}

func (d *DatastoreMSSQL) GetResourceModificationsSinceLastReconcile(stack string) ([]datastore.ResourceModification, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetResourceModificationsSinceLastReconcile")
	defer span.End()

	query := fmt.Sprintf(`
SELECT DISTINCT
  T2.type, T2.label, T2.operation, T2.ksuid
FROM %[1]s AS T1
JOIN resources AS T2 ON T1.command_id = T2.command_id
WHERE EXISTS (
    SELECT 1 FROM resources AS r1
    WHERE r1.stack = @p1 AND NOT EXISTS (
        SELECT 1 FROM resources AS r2
        WHERE r1.ksuid = r2.ksuid AND r2.version %[2]s > r1.version %[2]s
    ) AND r1.operation != 'delete' AND r1.operation != 'reaped'
  ) AND T1.timestamp > (
    SELECT TOP (1) fc.timestamp FROM forma_commands fc
    WHERE fc.config_mode = 'reconcile'
      AND EXISTS (SELECT 1 FROM resources r WHERE r.command_id = fc.command_id AND r.stack = @p2)
    ORDER BY fc.timestamp DESC
  ) AND T2.stack = @p3`, datastore.CommandsTable, binColl)

	rows, err := d.conn.QueryContext(ctx, query, stack, stack, stack)
	if err != nil {
		return nil, err
	}

	// Phase 1: fully drain and close rows before issuing secondary queries.
	type rawRow struct {
		resourceType string
		label        string
		operation    string
		ksuid        string
	}
	var raw []rawRow
	for rows.Next() {
		var r rawRow
		if err := rows.Scan(&r.resourceType, &r.label, &r.operation, &r.ksuid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	// Phase 2: for update ops, fetch the current and at-last-reconcile properties.
	var out []datastore.ResourceModification
	for _, r := range raw {
		mod := datastore.ResourceModification{Stack: stack, Type: r.resourceType, Label: r.label, Operation: r.operation}
		if r.operation == "update" {
			curProps, propErr := d.fetchCurrentProperties(ctx, r.ksuid)
			if propErr != nil {
				return nil, fmt.Errorf("failed to fetch current properties for %s: %w", r.ksuid, propErr)
			}
			oldProps, propErr := d.fetchReconcileProperties(ctx, r.ksuid, stack)
			if propErr != nil {
				return nil, fmt.Errorf("failed to fetch reconcile properties for %s: %w", r.ksuid, propErr)
			}
			mod.Properties = curProps
			mod.OldProperties = oldProps
		}
		out = append(out, mod)
	}

	return out, nil
}

// fetchCurrentProperties returns the Properties JSON from the latest resource
// version for the given ksuid.
func (d *DatastoreMSSQL) fetchCurrentProperties(ctx context.Context, ksuid string) (json.RawMessage, error) {
	query := fmt.Sprintf(`
SELECT TOP (1) JSON_QUERY(data, '$.Properties')
FROM resources
WHERE ksuid = @p1
ORDER BY version %s DESC`, binColl)

	var props sql.NullString
	if err := d.conn.QueryRowContext(ctx, query, ksuid).Scan(&props); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !props.Valid || props.String == "" {
		return nil, nil
	}
	return json.RawMessage(props.String), nil
}

// fetchReconcileProperties returns the Properties JSON of the resource version
// that was current as of the most recent reconcile command for the given stack:
// the latest version whose owning command does not postdate that reconcile.
// A resource untouched by the last reconcile (no new version row) still
// resolves to the version it had when that reconcile ran.
func (d *DatastoreMSSQL) fetchReconcileProperties(ctx context.Context, ksuid, stack string) (json.RawMessage, error) {
	query := fmt.Sprintf(`
SELECT TOP (1) JSON_QUERY(r.data, '$.Properties')
FROM resources r
JOIN forma_commands fc_r ON fc_r.command_id = r.command_id
WHERE r.ksuid = @p1
  AND fc_r.timestamp <= (
    SELECT TOP (1) fc.timestamp FROM forma_commands fc
    WHERE fc.config_mode = 'reconcile'
      AND EXISTS (SELECT 1 FROM resources rr WHERE rr.command_id = fc.command_id AND rr.stack = @p2)
    ORDER BY fc.timestamp DESC
  )
ORDER BY r.version %s DESC`, binColl)

	var props sql.NullString
	if err := d.conn.QueryRowContext(ctx, query, ksuid, stack).Scan(&props); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !props.Valid || props.String == "" {
		return nil, nil
	}
	return json.RawMessage(props.String), nil
}

func (d *DatastoreMSSQL) QueryFormaCommands(query *datastore.StatusQuery) ([]*forma_command.FormaCommand, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "QueryFormaCommands")
	defer span.End()

	limit := datastore.DefaultFormaCommandsQueryLimit
	if query.N > 0 {
		limit = min(datastore.DefaultFormaCommandsQueryLimit, query.N)
	}

	subqueryStr := fmt.Sprintf("SELECT TOP (%d) command_id FROM forma_commands WHERE 1=1", limit)
	args := []any{}

	subqueryStr = extendMSSQLQueryString(subqueryStr, query.CommandID, " AND command_id %s @p%d{esc}", &args)
	subqueryStr = extendMSSQLQueryString(subqueryStr, query.ClientID, " AND client_id %s @p%d{esc}", &args)
	subqueryStr = extendMSSQLQueryString(subqueryStr, query.Command, " AND LOWER(command) %s LOWER(@p%d){esc}", &args)
	if query.Command == nil {
		subqueryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}
	subqueryStr = extendMSSQLQueryString(subqueryStr, query.Stack, " AND EXISTS (SELECT 1 FROM resource_updates ru WHERE ru.command_id = forma_commands.command_id AND ru.stack_label %s @p%d{esc})", &args)
	subqueryStr = extendMSSQLQueryString(subqueryStr, query.Status, " AND LOWER(state) %s LOWER(@p%d){esc}", &args)
	subqueryStr += " ORDER BY timestamp DESC"

	queryStr := formaCommandWithResourceUpdatesQueryBase + fmt.Sprintf(`
		WHERE fc.command_id IN (%s)
		ORDER BY fc.timestamp DESC, ru.ksuid ASC`, subqueryStr)

	rows, err := d.conn.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRows(rows)
}

// BulkStoreResourceUpdates DELETEs the command's existing rows then bulk
// INSERTs in one transaction (MSSQL has no INSERT OR REPLACE).
func (d *DatastoreMSSQL) BulkStoreResourceUpdates(commandID string, updates []resource_update.ResourceUpdate) error {
	ctx, span := mssqlTracer.Start(context.Background(), "BulkStoreResourceUpdates")
	defer span.End()

	if len(updates) == 0 {
		return nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, "DELETE FROM resource_updates WHERE command_id = @p1", commandID); err != nil {
		return fmt.Errorf("failed to clear existing resource updates: %w", err)
	}

	const colsPerRow = 23
	insertPrefix := `INSERT INTO resource_updates (
		command_id, ksuid, operation, state, start_ts, modified_ts,
		retries, remaining, version, stack_label, group_id, source,
		resource, resource_target, existing_resource, existing_target,
		progress_result, most_recent_progress,
		remaining_resolvables, reference_labels, previous_properties,
		is_cascade, cascade_source
	) VALUES `

	// Dedupe by (ksuid, operation) keeping the last — last-wins, matching the
	// other backends' upsert semantics and avoiding PK collisions on insert.
	seen := make(map[string]int, len(updates))
	deduped := make([]resource_update.ResourceUpdate, 0, len(updates))
	for _, ru := range updates {
		key := ru.DesiredState.Ksuid + "\x00" + string(ru.Operation)
		if idx, ok := seen[key]; ok {
			deduped[idx] = ru
		} else {
			seen[key] = len(deduped)
			deduped = append(deduped, ru)
		}
	}

	for _, ru := range deduped {
		resourceJSON, err := json.Marshal(ru.DesiredState)
		if err != nil {
			return fmt.Errorf("failed to marshal resource: %w", err)
		}
		resourceTargetJSON, err := json.Marshal(ru.ResourceTarget)
		if err != nil {
			return fmt.Errorf("failed to marshal resource target: %w", err)
		}
		existingResourceJSON, err := json.Marshal(ru.PriorState)
		if err != nil {
			return fmt.Errorf("failed to marshal existing resource: %w", err)
		}
		existingTargetJSON, err := json.Marshal(ru.ExistingTarget)
		if err != nil {
			return fmt.Errorf("failed to marshal existing target: %w", err)
		}
		progressResultJSON, err := json.Marshal(ru.ProgressResult)
		if err != nil {
			return fmt.Errorf("failed to marshal progress result: %w", err)
		}
		mostRecentProgressJSON, err := json.Marshal(ru.MostRecentProgressResult)
		if err != nil {
			return fmt.Errorf("failed to marshal most recent progress: %w", err)
		}
		remainingResolvablesJSON, err := json.Marshal(ru.RemainingResolvables)
		if err != nil {
			return fmt.Errorf("failed to marshal remaining resolvables: %w", err)
		}
		referenceLabelsJSON, err := json.Marshal(ru.ReferenceLabels)
		if err != nil {
			return fmt.Errorf("failed to marshal reference labels: %w", err)
		}

		stackLabel := ru.StackLabel
		if stackLabel == "" {
			stackLabel = ru.DesiredState.Stack
		}

		var previousProperties any
		if len(ru.PreviousProperties) > 0 {
			previousProperties = string(ru.PreviousProperties)
		}

		// One INSERT per row keeps us under MSSQL's 2100-param cap on big batches.
		args := []any{
			commandID,
			ru.DesiredState.Ksuid,
			string(ru.Operation),
			string(ru.State),
			ru.StartTs.UTC(),
			ru.ModifiedTs.UTC(),
			int(ru.Retries),
			int(ru.Remaining),
			ru.Version,
			stackLabel,
			ru.GroupID,
			string(ru.Source),
			string(resourceJSON),
			string(resourceTargetJSON),
			string(existingResourceJSON),
			string(existingTargetJSON),
			string(progressResultJSON),
			string(mostRecentProgressJSON),
			string(remainingResolvablesJSON),
			string(referenceLabelsJSON),
			previousProperties,
			ru.IsCascade,
			ru.CascadeSource,
		}
		stmt := insertPrefix + "(" + placeholders(1, colsPerRow) + ")"
		if _, err = tx.ExecContext(ctx, stmt, args...); err != nil {
			return fmt.Errorf("failed to insert resource update: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true
	return nil
}

func (d *DatastoreMSSQL) UpdateFormaCommandProgress(commandID string, state forma_command.CommandState, modifiedTs time.Time) error {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateFormaCommandProgress")
	defer span.End()

	result, err := d.conn.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET state = @p1, modified_ts = @p2 WHERE command_id = @p3", datastore.CommandsTable),
		string(state), modifiedTs.UTC(), commandID,
	)
	if err != nil {
		return fmt.Errorf("failed to update forma command meta: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("forma command not found: %s", commandID)
	}
	return nil
}

func (d *DatastoreMSSQL) UpdateFormaCommandTargetUpdates(commandID string, targetUpdatesJSON json.RawMessage, state forma_command.CommandState, modifiedTs time.Time) error {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateFormaCommandTargetUpdates")
	defer span.End()

	result, err := d.conn.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET target_updates = @p1, state = @p2, modified_ts = @p3 WHERE command_id = @p4", datastore.CommandsTable),
		string(targetUpdatesJSON), string(state), modifiedTs.UTC(), commandID,
	)
	if err != nil {
		return fmt.Errorf("failed to update forma command target updates: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("forma command not found: %s", commandID)
	}
	return nil
}
