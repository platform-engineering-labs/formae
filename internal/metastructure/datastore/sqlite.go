// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	"github.com/demula/mksuid/v2"
	json "github.com/goccy/go-json"
	"github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

var sqliteTracer trace.Tracer

const sqliteOtelDriverName = "sqlite3-otel"

func init() {
	sqliteTracer = otel.Tracer("formae/datastore/sqlite")

	// Register otelsql-instrumented SQLite driver for automatic query tracing
	sql.Register(sqliteOtelDriverName, otelsql.WrapDriver(&sqlite3.SQLiteDriver{},
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
		),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			DisableErrSkip: true,
		}),
	))
}

type DatastoreSQLite struct {
	conn    *sql.DB
	agentID string
	ctx     context.Context
}

type TestDatastoreSQLite interface {
	ClearCommandsTable() error
}

func NewDatastoreSQLite(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (Datastore, error) {
	// Skip directory creation for in-memory databases
	isMemoryDb := cfg.Sqlite.FilePath == ":memory:" ||
		strings.HasPrefix(cfg.Sqlite.FilePath, "file::memory:")

	if cfg.Sqlite.FilePath != "" && !isMemoryDb {
		if err := util.EnsureFileFolderHierarchy(cfg.Sqlite.FilePath); err != nil {
			slog.Error("failed to create log folder hierarchy", "error", err)
			return nil, err
		}
	}

	conn, err := sql.Open(sqliteOtelDriverName, cfg.Sqlite.FilePath)
	if err != nil {
		slog.Error("Failed to connect to sqlite database", "error", err)
		return nil, err
	}

	// Enable WAL mode for better concurrency - allows readers to proceed during writes.
	// This prevents timeouts when concurrent operations (like CleanupEmptyStacks) need
	// to read while bulk operations are writing.
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		slog.Error("Failed to enable WAL mode", "error", err)
		return nil, err
	}

	// Set a busy timeout so SQLite waits instead of immediately failing when locked.
	// 10 seconds should be more than enough for any operation to complete.
	if _, err := conn.Exec("PRAGMA busy_timeout=10000"); err != nil {
		slog.Error("Failed to set busy timeout", "error", err)
		return nil, err
	}

	// SQLite doesn't handle concurrent writes well - limit to a single connection
	// to avoid "database is locked" errors during concurrent operations.
	conn.SetMaxOpenConns(1)

	d := DatastoreSQLite{conn: conn, agentID: agentID, ctx: ctx}

	if err = runMigrations(conn, "sqlite3"); err != nil {
		return nil, err
	}

	slog.Info("Started SQLite datastore", "filePath", cfg.Sqlite.FilePath)

	return d, nil
}

func (d DatastoreSQLite) ClearCommandsTable() error {
	_, err := d.conn.Exec(fmt.Sprintf("DELETE FROM %s", CommandsTable))
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to clear %s table", CommandsTable), "error", err)
		return err
	}
	return nil
}

func (d DatastoreSQLite) StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	_, span := sqliteTracer.Start(context.Background(), "StoreFormaCommand")
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

	// We no longer store the forma JSON - Description and Config are stored as normalized columns
	// Normalize timestamps to UTC for consistent TEXT-based sorting in SQLite
	startTsUTC := fa.StartTs.UTC()
	modifiedTsUTC := fa.ModifiedTs.UTC()

	// Convert booleans to integers for SQLite
	descriptionConfirm := 0
	if fa.Description.Confirm {
		descriptionConfirm = 1
	}
	configForce := 0
	if fa.Config.Force {
		configForce = 1
	}
	configSimulate := 0
	if fa.Config.Simulate {
		configSimulate = 1
	}

	query := fmt.Sprintf(`INSERT OR REPLACE INTO %s
		(command_id, timestamp, command, state, agent_version, client_id, agent_id,
		 description_text, description_confirm, config_mode, config_force, config_simulate,
		 target_updates, stack_updates, policy_updates, modified_ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, CommandsTable)

	_, err = d.conn.Exec(query, commandID, startTsUTC, fa.Command, fa.State, formae.Version, fa.ClientID, d.agentID,
		fa.Description.Text, descriptionConfirm, fa.Config.Mode, configForce, configSimulate,
		targetUpdatesJSON, stackUpdatesJSON, policyUpdatesJSON, modifiedTsUTC)
	if err != nil {
		slog.Error("Query", "query", query, "error", err)
		return err
	}

	// Store ResourceUpdates in the normalized table
	// Skip for sync commands - they don't need upfront storage since:
	// 1. Sync commands are never resumed after restart (excluded from LoadIncompleteFormaCommands)
	// 2. Progress is tracked in-memory via the FormaCommandPersister cache
	// 3. Only resource updates with actual changes (Version set) are inserted on completion
	if len(fa.ResourceUpdates) > 0 && fa.Command != pkgmodel.CommandSync {
		if err := d.BulkStoreResourceUpdates(commandID, fa.ResourceUpdates); err != nil {
			return fmt.Errorf("failed to store resource updates: %w", err)
		}
	}

	return nil
}

// formaCommandWithResourceUpdatesQueryBase is the base LEFT JOIN query without WHERE or ORDER BY
const formaCommandWithResourceUpdatesQueryBase = `
SELECT
	fc.command_id, fc.timestamp, fc.command, fc.state, fc.client_id,
	fc.description_text, fc.description_confirm, fc.config_mode, fc.config_force, fc.config_simulate,
	fc.target_updates, fc.stack_updates, fc.policy_updates, fc.modified_ts,
	ru.ksuid, ru.operation, ru.state, ru.start_ts, ru.modified_ts,
	ru.retries, ru.remaining, ru.version, ru.stack_label, ru.group_id, ru.source,
	ru.resource, ru.resource_target, ru.existing_resource, ru.existing_target,
	ru.progress_result, ru.most_recent_progress,
	ru.remaining_resolvables, ru.reference_labels, ru.previous_properties
FROM forma_commands fc
LEFT JOIN resource_updates ru ON fc.command_id = ru.command_id`

// resourceUpdateOrderBy ensures deterministic ordering of resource updates (KSUIDs are time-sortable)
const resourceUpdateOrderBy = " ORDER BY fc.timestamp DESC, ru.ksuid ASC"

// scanJoinedRow scans a row from a FormaCommand LEFT JOIN ResourceUpdate query.
// Returns the command fields and optionally a ResourceUpdate (nil if no resource update for this row).
func scanJoinedRow(rows *sql.Rows) (*forma_command.FormaCommand, *resource_update.ResourceUpdate, error) {
	var cmd forma_command.FormaCommand
	var commandID, fcTimestamp, command, fcState string
	var clientID sql.NullString
	var descriptionText sql.NullString
	var descriptionConfirm sql.NullInt64
	var configMode sql.NullString
	var configForce, configSimulate sql.NullInt64
	var targetUpdatesJSON []byte
	var stackUpdatesJSON []byte
	var policyUpdatesJSON []byte
	var fcModifiedTs sql.NullString

	// ResourceUpdate fields (all nullable due to LEFT JOIN)
	var ruKsuid, ruOperation, ruState sql.NullString
	var ruStartTs, ruModifiedTs sql.NullString
	var ruRetries, ruRemaining sql.NullInt64
	var ruVersion, ruStackLabel, ruGroupID, ruSource sql.NullString
	var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
	var progressResultJSON, mostRecentProgressJSON []byte
	var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte

	err := rows.Scan(
		// FormaCommand columns
		&commandID, &fcTimestamp, &command, &fcState, &clientID,
		&descriptionText, &descriptionConfirm, &configMode, &configForce, &configSimulate,
		&targetUpdatesJSON, &stackUpdatesJSON, &policyUpdatesJSON, &fcModifiedTs,
		// ResourceUpdate columns
		&ruKsuid, &ruOperation, &ruState, &ruStartTs, &ruModifiedTs,
		&ruRetries, &ruRemaining, &ruVersion, &ruStackLabel, &ruGroupID, &ruSource,
		&resourceJSON, &resourceTargetJSON, &existingResourceJSON, &existingTargetJSON,
		&progressResultJSON, &mostRecentProgressJSON,
		&remainingResolvablesJSON, &referenceLabelsJSON, &previousPropertiesJSON,
	)
	if err != nil {
		return nil, nil, err
	}

	// Populate FormaCommand
	cmd.ID = commandID
	cmd.Command = pkgmodel.Command(command)
	cmd.State = forma_command.CommandState(fcState)
	if clientID.Valid {
		cmd.ClientID = clientID.String
	}

	// Read description from normalized columns
	if descriptionText.Valid {
		cmd.Description.Text = descriptionText.String
	}
	cmd.Description.Confirm = descriptionConfirm.Valid && descriptionConfirm.Int64 == 1

	// Read config from normalized columns
	if configMode.Valid {
		cmd.Config.Mode = pkgmodel.FormaApplyMode(configMode.String)
	}
	cmd.Config.Force = configForce.Valid && configForce.Int64 == 1
	cmd.Config.Simulate = configSimulate.Valid && configSimulate.Int64 == 1

	// Parse timestamp - convert to UTC
	// SQLite stores time.Time as "2006-01-02 15:04:05.999999999-07:00" format
	if ts, err := time.Parse(time.RFC3339Nano, fcTimestamp); err == nil {
		cmd.StartTs = ts.UTC()
	} else if ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", fcTimestamp); err == nil {
		cmd.StartTs = ts.UTC()
	}

	// Parse modified_ts (TIMESTAMP column)
	if fcModifiedTs.Valid && fcModifiedTs.String != "" {
		if ts, err := time.Parse(time.RFC3339Nano, fcModifiedTs.String); err == nil {
			cmd.ModifiedTs = ts.UTC()
		} else if ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", fcModifiedTs.String); err == nil {
			cmd.ModifiedTs = ts.UTC()
		}
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

	// Check if there's a ResourceUpdate (LEFT JOIN may return NULL)
	if !ruKsuid.Valid {
		return &cmd, nil, nil
	}

	// Populate ResourceUpdate
	var ru resource_update.ResourceUpdate
	ru.Operation = types.OperationType(ruOperation.String)
	ru.State = resource_update.ResourceUpdateState(ruState.String)

	// Parse ResourceUpdate timestamps (TIMESTAMP columns)
	if ruStartTs.Valid && ruStartTs.String != "" {
		if ts, err := time.Parse(time.RFC3339Nano, ruStartTs.String); err == nil {
			ru.StartTs = ts.UTC()
		} else if ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", ruStartTs.String); err == nil {
			ru.StartTs = ts.UTC()
		}
	}
	if ruModifiedTs.Valid && ruModifiedTs.String != "" {
		if ts, err := time.Parse(time.RFC3339Nano, ruModifiedTs.String); err == nil {
			ru.ModifiedTs = ts.UTC()
		} else if ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", ruModifiedTs.String); err == nil {
			ru.ModifiedTs = ts.UTC()
		}
	}

	if ruRetries.Valid {
		ru.Retries = uint16(ruRetries.Int64)
	}
	if ruRemaining.Valid {
		ru.Remaining = int16(ruRemaining.Int64)
	}
	ru.Version = ruVersion.String
	ru.StackLabel = ruStackLabel.String
	ru.GroupID = ruGroupID.String
	ru.Source = resource_update.FormaCommandSource(ruSource.String)

	if len(resourceJSON) > 0 {
		if err := json.Unmarshal(resourceJSON, &ru.DesiredState); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal resource: %w", err)
		}
		ru.DesiredState.Ksuid = ruKsuid.String
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

	return &cmd, &ru, nil
}

// loadFormaCommandsFromJoinedRows processes rows from a LEFT JOIN query and groups ResourceUpdates by command.
// This is the preferred method as it uses a single query and allows safe use of defer rows.Close().
func loadFormaCommandsFromJoinedRows(rows *sql.Rows) ([]*forma_command.FormaCommand, error) {
	defer func() { _ = rows.Close() }()

	// Use a map to collect ResourceUpdates for each command
	commandMap := make(map[string]*forma_command.FormaCommand)
	var commandOrder []string // Preserve order

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

	// Return commands in original order
	result := make([]*forma_command.FormaCommand, 0, len(commandOrder))
	for _, id := range commandOrder {
		result = append(result, commandMap[id])
	}

	return result, nil
}

func (d DatastoreSQLite) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadFormaCommands")
	defer span.End()

	rows, err := d.conn.Query(formaCommandWithResourceUpdatesQueryBase + resourceUpdateOrderBy)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRows(rows)
}

func (d DatastoreSQLite) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadIncompleteFormaCommands")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBase + " WHERE fc.command != 'sync' AND fc.state = ?" + resourceUpdateOrderBy
	rows, err := d.conn.Query(query, forma_command.CommandStateInProgress)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRows(rows)
}

func (d DatastoreSQLite) DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	_, span := sqliteTracer.Start(context.Background(), "DeleteFormaCommand")
	defer span.End()

	// Delete resource_updates first (no FK constraint, so we must do this manually)
	_, err := d.conn.Exec("DELETE FROM resource_updates WHERE command_id = ?", commandID)
	if err != nil {
		return fmt.Errorf("failed to delete resource_updates: %w", err)
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE command_id = ?", CommandsTable)
	_, err = d.conn.Exec(query, commandID)
	return err
}

func (d DatastoreSQLite) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetFormaCommandByCommandID")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBase + " WHERE fc.command_id = ?" + resourceUpdateOrderBy
	rows, err := d.conn.Query(query, commandID)
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

func (d DatastoreSQLite) GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetMostRecentFormaCommandByClientID")
	defer span.End()

	// Use a subquery to find the most recent command_id, then join with resource_updates
	query := `
		SELECT
			fc.command_id, fc.timestamp, fc.command, fc.state, fc.client_id,
			fc.description_text, fc.description_confirm, fc.config_mode, fc.config_force, fc.config_simulate,
			fc.target_updates, fc.stack_updates, fc.policy_updates, fc.modified_ts,
			ru.ksuid, ru.operation, ru.state, ru.start_ts, ru.modified_ts,
			ru.retries, ru.remaining, ru.version, ru.stack_label, ru.group_id, ru.source,
			ru.resource, ru.resource_target, ru.existing_resource, ru.existing_target,
			ru.progress_result, ru.most_recent_progress,
			ru.remaining_resolvables, ru.reference_labels, ru.previous_properties
		FROM forma_commands fc
		LEFT JOIN resource_updates ru ON fc.command_id = ru.command_id
		WHERE fc.command_id = (
			SELECT command_id FROM forma_commands
			WHERE client_id = ?
			ORDER BY timestamp DESC
			LIMIT 1
		)
		ORDER BY ru.ksuid ASC
	`
	rows, err := d.conn.Query(query, clientID)
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

func (d DatastoreSQLite) GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetResourceModificationsSinceLastReconcile")
	defer span.End()

	query := fmt.Sprintf(`
SELECT DISTINCT
  T2.type,
  T2.label,
  T2.operation
FROM %s AS T1
JOIN resources AS T2
  ON T1.command_id = T2.command_id
WHERE
  EXISTS (
    SELECT
      1
    FROM resources AS r1
    WHERE
      r1.stack = ? AND NOT EXISTS (
        SELECT
          1
        FROM resources AS r2
        WHERE
          r1.ksuid = r2.ksuid AND r2.version > r1.version
      ) AND r1.operation != 'delete'
  ) AND T1.timestamp > (
    SELECT
      fc.timestamp
    FROM forma_commands fc
    WHERE
      fc.config_mode = 'reconcile'
      AND EXISTS (
        SELECT 1
        FROM resources r
        WHERE r.command_id = fc.command_id
        AND r.stack = ?
      )
    ORDER BY
      fc.timestamp DESC
    LIMIT 1
  ) AND T2.stack = ?;
		`, CommandsTable)
	rows, err := d.conn.Query(query, stack, stack, stack)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	modifications := make(map[ResourceModification]struct{})
	for rows.Next() {
		var resourceType string
		var label string
		var operation string
		if err := rows.Scan(&resourceType, &label, &operation); err != nil {
			return nil, err
		}
		modifications[ResourceModification{Stack: stack, Type: resourceType, Label: label, Operation: operation}] = struct{}{}
	}

	return slices.Collect(maps.Keys(modifications)), nil
}

func (d DatastoreSQLite) Close() {
	if err := d.conn.Close(); err != nil {
		slog.Error("Error closing database connection", "error", err)
	}
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		slog.Error("Error closing database rows", "error", err)
	}
}

func extendSQLiteQueryString[T any](queryStr string, queryItem *QueryItem[T], sqlPart string, args *[]any) string {
	if queryItem != nil {
		var operator string

		if queryItem.Constraint == Excluded {
			operator = "!="
		} else if queryItem.Constraint == Required || queryItem.Constraint == Optional {
			operator = "="
		}

		queryStr += fmt.Sprintf(sqlPart, operator)
		operand := ""
		switch v := any(queryItem.Item).(type) {
		case bool:
			if v {
				operand = "1"
			} else {
				operand = "0"
			}
		case string:
			operand = v
		default:
			operand = fmt.Sprintf("%v", v)
		}

		*args = append(*args, operand)
	}

	return queryStr
}

func (d DatastoreSQLite) QueryFormaCommands(query *StatusQuery) ([]*forma_command.FormaCommand, error) {
	_, span := sqliteTracer.Start(context.Background(), "QueryFormaCommands")
	defer span.End()

	// Build subquery to find matching command IDs with filtering and LIMIT
	subqueryStr := "SELECT command_id FROM forma_commands WHERE 1=1"
	args := []any{}

	subqueryStr = extendSQLiteQueryString(subqueryStr, query.CommandID, " AND command_id %s ?", &args)
	subqueryStr = extendSQLiteQueryString(subqueryStr, query.ClientID, " AND client_id %s ?", &args)
	subqueryStr = extendSQLiteQueryString(subqueryStr, query.Command, " AND LOWER(command) %s LOWER(?)", &args)
	if query.Command == nil {
		subqueryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}

	// Stack filter uses the normalized resource_updates table
	subqueryStr = extendSQLiteQueryString(subqueryStr, query.Stack, " AND EXISTS (SELECT 1 FROM resource_updates ru WHERE ru.command_id = forma_commands.command_id AND ru.stack_label %s ?)", &args)
	subqueryStr = extendSQLiteQueryString(subqueryStr, query.Status, " AND LOWER(state) %s LOWER(?)", &args)

	subqueryStr += " ORDER BY timestamp DESC"
	if query.N > 0 {
		subqueryStr += " LIMIT ?"
		args = append(args, min(DefaultFormaCommandsQueryLimit, query.N))
	} else {
		subqueryStr += fmt.Sprintf(" LIMIT %d", DefaultFormaCommandsQueryLimit)
	}

	// Main query joins with resource_updates for commands matching the subquery
	queryStr := fmt.Sprintf(`
		SELECT
			fc.command_id, fc.timestamp, fc.command, fc.state, fc.client_id,
			fc.description_text, fc.description_confirm, fc.config_mode, fc.config_force, fc.config_simulate,
			fc.target_updates, fc.stack_updates, fc.policy_updates, fc.modified_ts,
			ru.ksuid, ru.operation, ru.state, ru.start_ts, ru.modified_ts,
			ru.retries, ru.remaining, ru.version, ru.stack_label, ru.group_id, ru.source,
			ru.resource, ru.resource_target, ru.existing_resource, ru.existing_target,
			ru.progress_result, ru.most_recent_progress,
			ru.remaining_resolvables, ru.reference_labels, ru.previous_properties
		FROM forma_commands fc
		LEFT JOIN resource_updates ru ON fc.command_id = ru.command_id
		WHERE fc.command_id IN (%s)
		ORDER BY fc.timestamp DESC, ru.ksuid ASC
	`, subqueryStr)

	rows, err := d.conn.Query(queryStr, args...)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRows(rows)
}

func (d DatastoreSQLite) QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "QueryResources")
	defer span.End()

	queryStr := fmt.Sprintf(`
		SELECT data, ksuid
		FROM resources r1
		WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
		)
		AND r1.operation != '%s'`, string(resource_update.OperationDelete))
	args := []any{}

	queryStr = extendSQLiteQueryString(queryStr, query.NativeID, " AND native_id %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Stack, " AND stack %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Type, " AND LOWER(type) %s LOWER(?)", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Label, " AND label %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Target, " AND target %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Managed, " AND managed %s ?", &args)
	queryStr += " ORDER BY type, label"

	rows, err := d.conn.Query(queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var resources []*pkgmodel.Resource
	for rows.Next() {
		var jsonData, ksuid string
		if err := rows.Scan(&jsonData, &ksuid); err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		// Set the KSUID field from the database
		resource.Ksuid = ksuid

		resources = append(resources, &resource)
	}

	return resources, rows.Err()
}

func (d DatastoreSQLite) storeResource(resource *pkgmodel.Resource, data []byte, commandID string, operation string) (string, error) {
	if resource.Ksuid == "" {
		existingResource, err := d.LoadResource(resource.URI())
		if err != nil {
			return "", err
		}

		if existingResource != nil {
			resource.Ksuid = existingResource.Ksuid
		} else {
			resource.Ksuid = metautil.NewID()
		}
	}

	// Check if this resource already exists using native_id and type
	query := `SELECT ksuid, data, uri, version, managed FROM resources WHERE native_id = ? AND type = ? ORDER BY version DESC LIMIT 1`
	row := d.conn.QueryRow(query, resource.NativeID, resource.Type)

	var ksuid string
	var existingData string
	var uri string
	var version string
	var managed int
	err := row.Scan(&ksuid, &existingData, &uri, &version, &managed)
	if err == sql.ErrNoRows {
		// Resource does not exist, create the initial version
		newVersion := mksuid.New()

		query = `
			INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err = d.conn.Exec(
			query,
			resource.URI(),
			newVersion,
			commandID,
			operation,
			resource.NativeID,
			resource.Stack,
			resource.Type,
			resource.Label,
			resource.Target,
			data,
			boolToInt(resource.Managed),
			resource.Ksuid)
		if err != nil {
			slog.Error("Failed to store resource", "error", err, "resourceURI", resource.URI())
			return "", err
		}
		return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
	} else if err != nil {
		return "", err
	}

	// It can happen in rare cases that the resource existed on the unmanaged stack. This happens when a resource is discovered
	// before the create operation completes. In this case, we preserve the discovered KSUID to maintain referential integrity
	// for any resources that may have already created references to it.
	if managed == 0 && operation != string(resource_update.OperationDelete) {
		// Preserve the discovered KSUID instead of generating a new one
		slog.Debug("Resource discovered before apply completed, adopting discovered KSUID",
			"native_id", resource.NativeID,
			"type", resource.Type,
			"discovered_ksuid", ksuid,
			"original_ksuid", resource.Ksuid)
		resource.Ksuid = ksuid
		// Don't delete - we'll update the existing resource to managed status
		// The rest of the function will handle creating a new version with the preserved KSUID
	}

	// Resource exists, compare with existing data
	var existingResource pkgmodel.Resource
	if err = json.Unmarshal([]byte(existingData), &existingResource); err != nil {
		return "", err
	}

	readWriteEqual, readOnlyEqual := resourcesAreEqual(resource, &existingResource)

	if operation == string(resource_update.OperationDelete) {
		// For delete operations, check if the latest version is already a delete
		var latestOperation string
		query = `SELECT operation FROM resources WHERE uri = ? ORDER BY version DESC LIMIT 1`
		row = d.conn.QueryRow(query, resource.URI())
		err = row.Scan(&latestOperation)
		if err != nil {
			return "", err
		}

		if latestOperation == string(resource_update.OperationDelete) {
			// Already deleted, return existing version ID
			return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
		}
	} else {
		// For non-delete operations, compare resources
		if readWriteEqual && readOnlyEqual {
			// Resource data is identical, return existing version ID
			return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
		}
	}

	// We only create a new version if the read-write properties have changed. Read-only property changes do not
	// trigger a new version but instead update the existing.
	var newVersion string
	if readWriteEqual && !readOnlyEqual {
		newVersion = version
	} else {
		newVersion = mksuid.New().String()
	}

	query = `
		INSERT OR REPLACE INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = d.conn.Exec(
		query,
		resource.URI(),
		newVersion,
		commandID,
		operation,
		resource.NativeID,
		resource.Stack,
		resource.Type,
		resource.Label,
		resource.Target,
		data,
		boolToInt(resource.Managed),
		resource.Ksuid)
	if err != nil {
		slog.Error("Failed to store resource", "error", err, "resourceURI", resource.URI())
		return "", err
	}

	return fmt.Sprintf("%s_%s", ksuid, newVersion), nil
}

func (d DatastoreSQLite) StoreResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "StoreResource")
	defer span.End()

	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}

	return d.storeResource(resource, jsonData, commandID, string(resource_update.OperationUpdate))
}

func (d DatastoreSQLite) DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "DeleteResource")
	defer span.End()

	return d.storeResource(resource, []byte("{}"), commandID, string(resource_update.OperationDelete))
}

func (d DatastoreSQLite) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadResource")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources
	WHERE uri = ?
	AND operation != ?
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.conn.QueryRow(query, uri, resource_update.OperationDelete)

	var jsonData string
	var ksuid string
	if err := row.Scan(&jsonData, &ksuid); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Resource not found, return nil without error
		}
		return nil, err
	}

	var loadedResource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &loadedResource); err != nil {
		return nil, err
	}

	loadedResource.Ksuid = ksuid

	return &loadedResource, nil
}

func (d DatastoreSQLite) LoadResourceByNativeID(nativeID string, resourceType string) (*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadResourceByNativeID")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE native_id = ? AND type = ?
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	AND r1.operation != ?
	LIMIT 1
	`
	row := d.conn.QueryRow(query, nativeID, resourceType, resource_update.OperationDelete)

	var jsonData string
	var ksuid string
	if err := row.Scan(&jsonData, &ksuid); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	var loadedResource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &loadedResource); err != nil {
		return nil, err
	}

	loadedResource.Ksuid = ksuid
	return &loadedResource, nil
}

func (d DatastoreSQLite) LoadAllResources() ([]*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadAllResources")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
	SELECT 1
	FROM resources r2
	WHERE r1.uri = r2.uri
	AND r2.version > r1.version
	)
	AND operation != ?
	`
	rows, err := d.conn.Query(query, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var resources []*pkgmodel.Resource
	for rows.Next() {
		var jsonData string
		var ksuid string
		if err := rows.Scan(&jsonData, &ksuid); err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		resources = append(resources, &resource)
	}

	return resources, rows.Err()
}

func (d DatastoreSQLite) BulkStoreResources(resources []pkgmodel.Resource, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "BulkStoreResources")
	defer span.End()

	var ret string
	var err error
	for _, resource := range resources {
		if ret, err = d.StoreResource(&resource, commandID); err != nil {
			slog.Error("Failed to store resource", "error", err, "resourceURI", resource.URI())
			return "", err
		}
	}
	return ret, nil
}

func (d DatastoreSQLite) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadAllResourcesByStack")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
	SELECT 1
	FROM resources r2
	WHERE r1.uri = r2.uri
	AND r2.version > r1.version
	)
	AND operation != ?
	`

	rows, err := d.conn.Query(query, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var allResources []*pkgmodel.Resource
	for rows.Next() {
		var jsonData, ksuid string
		if err := rows.Scan(&jsonData, &ksuid); err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		allResources = append(allResources, &resource)
	}

	// Group resources by stack label
	stackResourcesMap := make(map[string][]*pkgmodel.Resource)
	for _, resource := range allResources {
		if resource.Stack != "" {
			stackResourcesMap[resource.Stack] = append(stackResourcesMap[resource.Stack], resource)
		}
	}

	return stackResourcesMap, nil
}

func (d DatastoreSQLite) LoadResourcesByStack(stackLabel string) ([]*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadResourcesByStack")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE stack = ?
	AND NOT EXISTS (
	SELECT 1
	FROM resources r2
	WHERE r1.uri = r2.uri
	AND r2.version > r1.version
	)
	AND operation != ?
	`

	rows, err := d.conn.Query(query, stackLabel, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var resources []*pkgmodel.Resource
	for rows.Next() {
		var jsonData, ksuid string
		if err := rows.Scan(&jsonData, &ksuid); err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		resources = append(resources, &resource)
	}

	return resources, nil
}

// Stack metadata operations

func (d DatastoreSQLite) CreateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "CreateStack")
	defer span.End()

	// Check if a non-deleted stack with this label already exists
	existing, err := d.GetStackByLabel(stack.Label)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return "", fmt.Errorf("stack already exists: %s", stack.Label)
	}

	// Use pre-generated ID if available, otherwise generate one
	id := stack.ID
	if id == "" {
		id = mksuid.New().String()
		stack.ID = id
	}
	version := mksuid.New().String()

	query := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (?, ?, ?, ?, ?, ?)`
	_, err = d.conn.Exec(query, id, version, commandID, "create", stack.Label, stack.Description)
	if err != nil {
		slog.Error("Failed to create stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d DatastoreSQLite) UpdateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "UpdateStack")
	defer span.End()

	// Get the existing stack to find its id (most recent version of any operation)
	query := `
		SELECT id, operation FROM stacks
		WHERE label = ?
		ORDER BY version DESC
		LIMIT 1
	`
	row := d.conn.QueryRow(query, stack.Label)

	var id, operation string
	if err := row.Scan(&id, &operation); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("stack not found: %s", stack.Label)
		}
		return "", err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", stack.Label)
	}

	// Insert new version with same id
	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := d.conn.Exec(insertQuery, id, version, commandID, "update", stack.Label, stack.Description)
	if err != nil {
		slog.Error("Failed to update stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d DatastoreSQLite) DeleteStack(label string, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "DeleteStack")
	defer span.End()

	// Get the existing stack to find its id (most recent version of any operation)
	query := `
		SELECT id, operation FROM stacks
		WHERE label = ?
		ORDER BY version DESC
		LIMIT 1
	`
	row := d.conn.QueryRow(query, label)

	var id, operation string
	if err := row.Scan(&id, &operation); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("stack not found: %s", label)
		}
		return "", err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", label)
	}

	// Cascade delete: delete all policies associated with this stack
	if err := d.DeletePoliciesForStack(id, commandID); err != nil {
		slog.Warn("Failed to delete policies for stack", "error", err, "stackID", id, "label", label)
		// Continue with stack deletion even if policy deletion fails
	}

	// Insert tombstone version
	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (?, ?, ?, ?, ?, ?)`
	_, execErr := d.conn.Exec(insertQuery, id, version, commandID, "delete", label, "")
	if execErr != nil {
		slog.Error("Failed to delete stack", "error", execErr, "label", label)
		return "", execErr
	}

	return version, nil
}

func (d DatastoreSQLite) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetStackByLabel")
	defer span.End()

	// Get the latest version of the stack, return nil if deleted
	// We need to check if the MOST RECENT version is a delete operation
	query := `
		SELECT id, description, operation FROM stacks
		WHERE label = ?
		ORDER BY version DESC
		LIMIT 1
	`
	row := d.conn.QueryRow(query, label)

	var id, description, operation string
	if err := row.Scan(&id, &description, &operation); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Stack not found, return nil without error
		}
		return nil, err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return nil, nil
	}

	stack := &pkgmodel.Stack{
		ID:          id,
		Label:       label,
		Description: description,
	}

	// Load policies for this stack (both inline and standalone references)
	policies, err := d.loadPoliciesForStackAsJSON(id)
	if err != nil {
		slog.Warn("Failed to load policies for stack", "label", label, "error", err)
		// Don't fail the whole operation, just return stack without policies
	} else {
		stack.Policies = policies
	}

	return stack, nil
}

// loadPoliciesForStackAsJSON loads all policies for a stack and returns them as JSON.
// For inline policies, returns the full policy JSON including Type and Label.
// For standalone policies, returns {"$ref": "policy://label"} format.
func (d DatastoreSQLite) loadPoliciesForStackAsJSON(stackID string) ([]json.RawMessage, error) {
	var policies []json.RawMessage

	// 1. Load inline policies (stack_id set directly on the policy)
	inlineQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE stack_id = ?
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	rows, err := d.conn.Query(inlineQuery, stackID)
	if err != nil {
		return nil, fmt.Errorf("failed to query inline policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var label, policyType, policyData string
		if err := rows.Scan(&label, &policyType, &policyData); err != nil {
			continue
		}
		// Reconstruct full policy JSON by merging stored data with type and label
		fullPolicyJSON, err := reconstructPolicyJSON(label, policyType, policyData)
		if err != nil {
			slog.Warn("Failed to reconstruct policy JSON", "label", label, "error", err)
			continue
		}
		policies = append(policies, fullPolicyJSON)
	}

	// 2. Load standalone policy references (via stack_policies junction table)
	standaloneQuery := `
		WITH latest_policies AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT p.label FROM stack_policies sp
		JOIN latest_policies p ON p.id = sp.policy_id
		WHERE sp.stack_id = ?
		AND p.rn = 1 AND p.operation != 'delete'
	`
	rows2, err := d.conn.Query(standaloneQuery, stackID)
	if err != nil {
		return policies, fmt.Errorf("failed to query standalone policies: %w", err)
	}
	defer func() { _ = rows2.Close() }()

	for rows2.Next() {
		var policyLabel string
		if err := rows2.Scan(&policyLabel); err != nil {
			continue
		}
		// Create a $ref entry for the standalone policy
		refJSON := fmt.Sprintf(`{"$ref":"policy://%s"}`, policyLabel)
		policies = append(policies, json.RawMessage(refJSON))
	}

	return policies, nil
}

// reconstructPolicyJSON merges policy_data with type and label to create full policy JSON
func reconstructPolicyJSON(label, policyType, policyData string) (json.RawMessage, error) {
	// Parse the stored policy data
	var data map[string]any
	if err := json.Unmarshal([]byte(policyData), &data); err != nil {
		return nil, fmt.Errorf("failed to parse policy data: %w", err)
	}

	// Add Type and Label
	data["Type"] = policyType
	data["Label"] = label

	// Marshal back to JSON
	result, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal full policy: %w", err)
	}
	return result, nil
}

func (d DatastoreSQLite) CountResourcesInStack(label string) (int, error) {
	_, span := sqliteTracer.Start(context.Background(), "CountResourcesInStack")
	defer span.End()

	// Count only latest version of resources that haven't been deleted
	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE stack = ?
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		AND operation != ?
	`
	row := d.conn.QueryRow(query, label, resource_update.OperationDelete)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func (d DatastoreSQLite) ListAllStacks() ([]*pkgmodel.Stack, error) {
	_, span := sqliteTracer.Start(context.Background(), "ListAllStackMetadata")
	defer span.End()

	// Get all stacks at their latest version that aren't deleted
	// Uses window function to reliably get the most recent version per stack id
	query := `
		SELECT id, label, description, valid_from FROM (
			SELECT id, label, description, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM stacks
		) sub
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`
	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var stacks []*pkgmodel.Stack
	for rows.Next() {
		var id, label, description string
		var validFrom time.Time
		if err := rows.Scan(&id, &label, &description, &validFrom); err != nil {
			return nil, err
		}
		stacks = append(stacks, &pkgmodel.Stack{
			ID:          id,
			Label:       label,
			Description: description,
			CreatedAt:   validFrom,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return stacks, nil
}

// Policy operations

func (d DatastoreSQLite) CreatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "CreatePolicy")
	defer span.End()

	// Generate KSUIDs for both id and version
	id := mksuid.New().String()
	version := mksuid.New().String()

	// Serialize policy-specific data as JSON
	var policyData []byte
	var err error
	switch p := policy.(type) {
	case *pkgmodel.TTLPolicy:
		policyData, err = json.Marshal(map[string]any{
			"TTLSeconds":   p.TTLSeconds,
			"OnDependents": p.OnDependents,
		})
	default:
		return "", fmt.Errorf("unsupported policy type: %T", policy)
	}
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy data: %w", err)
	}

	query := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = d.conn.Exec(query, id, version, commandID, "create", policy.GetLabel(), policy.GetType(), policy.GetStackID(), string(policyData))
	if err != nil {
		slog.Error("Failed to create policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d DatastoreSQLite) UpdatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "UpdatePolicy")
	defer span.End()

	// Get the existing policy to find its id
	// For standalone policies (empty stack_id), check for both NULL and empty string
	var query string
	var id string
	var err error

	if policy.GetStackID() == "" {
		// Standalone policy - check for NULL or empty string
		query = `
			SELECT id FROM policies
			WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
			ORDER BY version DESC
			LIMIT 1
		`
		err = d.conn.QueryRow(query, policy.GetLabel()).Scan(&id)
	} else {
		// Inline policy - exact match on stack_id
		query = `
			SELECT id FROM policies
			WHERE label = ? AND stack_id = ?
			ORDER BY version DESC
			LIMIT 1
		`
		err = d.conn.QueryRow(query, policy.GetLabel(), policy.GetStackID()).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("failed to find existing policy: %w", err)
	}

	// Generate new version
	version := mksuid.New().String()

	// Serialize policy-specific data as JSON
	var policyData []byte
	switch p := policy.(type) {
	case *pkgmodel.TTLPolicy:
		policyData, err = json.Marshal(map[string]any{
			"TTLSeconds":   p.TTLSeconds,
			"OnDependents": p.OnDependents,
		})
	default:
		return "", fmt.Errorf("unsupported policy type: %T", policy)
	}
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy data: %w", err)
	}

	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	                VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = d.conn.Exec(insertQuery, id, version, commandID, "update", policy.GetLabel(), policy.GetType(), policy.GetStackID(), string(policyData))
	if err != nil {
		slog.Error("Failed to update policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d DatastoreSQLite) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetPoliciesForStack")
	defer span.End()

	// Get the latest non-deleted version of each policy for the given stack
	// This includes both:
	// 1. Inline policies (stack_id set directly on the policy)
	// 2. Standalone policies (attached via stack_policies junction table)
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, stack_id, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
		),
		-- Inline policies: stack_id is set directly on the policy
		inline_policies AS (
			SELECT label, policy_type, policy_data, stack_id
			FROM latest_policies
			WHERE stack_id = ? AND rn = 1 AND operation != 'delete'
		),
		-- Standalone policies: attached via stack_policies junction table
		standalone_policies AS (
			SELECT lp.label, lp.policy_type, lp.policy_data, lp.stack_id
			FROM latest_policies lp
			JOIN stack_policies sp ON sp.policy_id = lp.id
			WHERE sp.stack_id = ? AND lp.rn = 1 AND lp.operation != 'delete'
			AND (lp.stack_id IS NULL OR lp.stack_id = '')
		)
		SELECT label, policy_type, policy_data, stack_id FROM inline_policies
		UNION
		SELECT label, policy_type, policy_data, stack_id FROM standalone_policies
	`

	rows, err := d.conn.Query(query, stackID, stackID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var policies []pkgmodel.Policy
	for rows.Next() {
		var label, policyType, policyDataStr string
		var policyStackID sql.NullString
		if err := rows.Scan(&label, &policyType, &policyDataStr, &policyStackID); err != nil {
			return nil, err
		}

		// For standalone policies, use the queried stackID for display purposes
		effectiveStackID := stackID
		if policyStackID.Valid && policyStackID.String != "" {
			effectiveStackID = policyStackID.String
		}
		policy, err := deserializePolicy(label, policyType, policyDataStr, effectiveStackID)
		if err != nil {
			slog.Warn("Failed to deserialize policy, skipping", "error", err, "label", label, "type", policyType)
			continue
		}
		policies = append(policies, policy)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return policies, nil
}

func (d DatastoreSQLite) GetStandalonePolicy(label string) (pkgmodel.Policy, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetStandalonePolicy")
	defer span.End()

	// Get the latest non-deleted version of the standalone policy with this label
	// Check for both NULL and empty string since Go driver may store either
	query := `
		WITH latest_policy AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'
	`

	var policyLabel, policyType, policyDataStr string
	err := d.conn.QueryRow(query, label).Scan(&policyLabel, &policyType, &policyDataStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return deserializePolicy(policyLabel, policyType, policyDataStr, "")
}

func (d DatastoreSQLite) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) {
	_, span := sqliteTracer.Start(context.Background(), "ListAllStandalonePolicies")
	defer span.End()

	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`
	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list standalone policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var policies []pkgmodel.Policy
	for rows.Next() {
		var label, policyType, policyDataStr string
		if err := rows.Scan(&label, &policyType, &policyDataStr); err != nil {
			return nil, fmt.Errorf("failed to scan policy: %w", err)
		}
		policy, err := deserializePolicy(label, policyType, policyDataStr, "")
		if err != nil {
			slog.Warn("Failed to deserialize policy", "label", label, "error", err)
			continue
		}
		policies = append(policies, policy)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating policies: %w", err)
	}

	return policies, nil
}

func (d DatastoreSQLite) AttachPolicyToStack(stackID, policyLabel string) error {
	_, span := sqliteTracer.Start(context.Background(), "AttachPolicyToStack")
	defer span.End()

	// First, get the policy ID from the label
	// Check for both NULL and empty string since Go driver may store either
	policyQuery := `
		WITH latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'
	`
	var policyID string
	err := d.conn.QueryRow(policyQuery, policyLabel).Scan(&policyID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("standalone policy not found: %s", policyLabel)
		}
		return fmt.Errorf("failed to get policy ID: %w", err)
	}

	// Insert into stack_policies (ignore if already exists)
	insertQuery := `INSERT OR IGNORE INTO stack_policies (stack_id, policy_id) VALUES (?, ?)`
	_, err = d.conn.Exec(insertQuery, stackID, policyID)
	if err != nil {
		return fmt.Errorf("failed to attach policy to stack: %w", err)
	}

	slog.Debug("Attached standalone policy to stack",
		"stackID", stackID,
		"policyLabel", policyLabel,
		"policyID", policyID)

	return nil
}

func (d DatastoreSQLite) IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error) {
	_, span := sqliteTracer.Start(context.Background(), "IsPolicyAttachedToStack")
	defer span.End()

	// Check if the attachment exists in stack_policies by looking up stack and policy by label
	query := `
		WITH latest_stack AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM stacks
			WHERE label = ?
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT 1 FROM stack_policies sp
		JOIN latest_stack s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		LIMIT 1
	`
	var exists int
	err := d.conn.QueryRow(query, stackLabel, policyLabel).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to check policy attachment: %w", err)
	}
	return true, nil
}

func (d DatastoreSQLite) GetStacksReferencingPolicy(policyLabel string) ([]string, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetStacksReferencingPolicy")
	defer span.End()

	// Get all stack labels that reference this standalone policy via the junction table
	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM stacks
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT s.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY s.label
	`
	rows, err := d.conn.Query(query, policyLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to get stacks referencing policy: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var stackLabels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, fmt.Errorf("failed to scan stack label: %w", err)
		}
		stackLabels = append(stackLabels, label)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stacks: %w", err)
	}

	return stackLabels, nil
}

func (d DatastoreSQLite) GetAttachedPolicyLabelsForStack(stackLabel string) ([]string, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetAttachedPolicyLabelsForStack")
	defer span.End()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM stacks
			WHERE label = ?
		),
		latest_policies AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT p.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policies p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY p.label
	`
	rows, err := d.conn.Query(query, stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to get attached policies for stack: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var policyLabels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, fmt.Errorf("failed to scan policy label: %w", err)
		}
		policyLabels = append(policyLabels, label)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating policies: %w", err)
	}

	return policyLabels, nil
}

func (d DatastoreSQLite) DetachPolicyFromStack(stackLabel, policyLabel string) error {
	_, span := sqliteTracer.Start(context.Background(), "DetachPolicyFromStack")
	defer span.End()

	query := `
		DELETE FROM stack_policies
		WHERE stack_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
				FROM stacks
				WHERE label = ?
			) WHERE rn = 1 AND operation != 'delete'
		)
		AND policy_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
				FROM policies
				WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
			) WHERE rn = 1 AND operation != 'delete'
		)
	`
	_, err := d.conn.Exec(query, stackLabel, policyLabel)
	if err != nil {
		return fmt.Errorf("failed to detach policy from stack: %w", err)
	}

	slog.Debug("Detached standalone policy from stack",
		"stackLabel", stackLabel,
		"policyLabel", policyLabel)

	return nil
}

func (d DatastoreSQLite) DeletePolicy(policyLabel string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "DeletePolicy")
	defer span.End()

	// Get the current policy to get its ID and type
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE label = ? AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	var id, policyType string
	err := d.conn.QueryRow(query, policyLabel).Scan(&id, &policyType)
	if err != nil {
		return "", fmt.Errorf("failed to get policy for deletion: %w", err)
	}

	// Insert tombstone version
	version := mksuid.New().String()
	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = d.conn.Exec(insertQuery, id, version, "", "delete", policyLabel, policyType, nil, "{}")
	if err != nil {
		return "", fmt.Errorf("failed to delete policy: %w", err)
	}

	slog.Debug("Deleted standalone policy", "label", policyLabel, "id", id)

	return version, nil
}

func (d DatastoreSQLite) DeletePoliciesForStack(stackID string, commandID string) error {
	_, span := sqliteTracer.Start(context.Background(), "DeletePoliciesForStack")
	defer span.End()

	// First, remove any standalone policy attachments from the junction table
	// This doesn't delete the standalone policies themselves, just the association
	deleteAttachmentsQuery := `DELETE FROM stack_policies WHERE stack_id = ?`
	_, err := d.conn.Exec(deleteAttachmentsQuery, stackID)
	if err != nil {
		slog.Warn("Failed to delete policy attachments for stack", "error", err, "stackID", stackID)
	}

	// Now get and delete only inline policies (policies with stack_id set to this stack)
	// We query directly for inline policies rather than using GetPoliciesForStack
	// since that also returns standalone policies which shouldn't be deleted
	inlinePoliciesQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
			WHERE stack_id = ?
		)
		SELECT id, label, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	rows, err := d.conn.Query(inlinePoliciesQuery, stackID)
	if err != nil {
		return fmt.Errorf("failed to get inline policies for stack: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, label, policyType string
		if err := rows.Scan(&id, &label, &policyType); err != nil {
			slog.Warn("Failed to scan policy for deletion", "error", err)
			continue
		}

		// Insert tombstone version for inline policy
		version := mksuid.New().String()
		insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		_, err = d.conn.Exec(insertQuery, id, version, commandID, "delete", label, policyType, stackID, "{}")
		if err != nil {
			slog.Error("Failed to delete inline policy", "error", err, "label", label)
			continue
		}
		slog.Debug("Deleted inline policy as part of stack deletion", "label", label, "stackID", stackID)
	}

	return nil
}

// deserializePolicy creates a Policy from stored data
func deserializePolicy(label, policyType, policyDataStr, stackID string) (pkgmodel.Policy, error) {
	switch policyType {
	case "ttl":
		var data struct {
			TTLSeconds   int64  `json:"TTLSeconds"`
			OnDependents string `json:"OnDependents"`
		}
		if err := json.Unmarshal([]byte(policyDataStr), &data); err != nil {
			return nil, fmt.Errorf("failed to unmarshal TTL policy data: %w", err)
		}
		return &pkgmodel.TTLPolicy{
			Type:         "ttl",
			Label:        label,
			TTLSeconds:   data.TTLSeconds,
			OnDependents: data.OnDependents,
			StackID:      stackID,
		}, nil
	default:
		return nil, fmt.Errorf("unknown policy type: %s", policyType)
	}
}

func (d DatastoreSQLite) GetExpiredStacks() ([]ExpiredStackInfo, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetExpiredStacks")
	defer span.End()

	// Get stacks with TTL policies that have expired:
	// - Handles both inline policies (stack_id set) and standalone policies (via stack_policies junction)
	// - Calculate expiration as stack.valid_from + policy.ttl_seconds
	// - Exclude stacks with active forma commands
	// - Only consider latest non-deleted versions of both stacks and policies
	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
			FROM policies
		),
		-- Inline policies: stack_id is set directly on the policy
		inline_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       json_extract(p.policy_data, '$.OnDependents') as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND datetime(s.valid_from, '+' || json_extract(p.policy_data, '$.TTLSeconds') || ' seconds') < datetime('now')
		),
		-- Standalone policies: attached via stack_policies junction table
		standalone_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       json_extract(p.policy_data, '$.OnDependents') as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN stack_policies sp ON sp.stack_id = s.id
			JOIN latest_policies p ON p.id = sp.policy_id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND (p.stack_id IS NULL OR p.stack_id = '')  -- standalone policies have NULL or empty stack_id
			AND datetime(s.valid_from, '+' || json_extract(p.policy_data, '$.TTLSeconds') || ' seconds') < datetime('now')
		),
		-- Combine both inline and standalone expired stacks
		all_expired AS (
			SELECT * FROM inline_expired
			UNION
			SELECT * FROM standalone_expired
		)
		SELECT stack_label, stack_id, on_dependents
		FROM all_expired
		WHERE NOT EXISTS (
			SELECT 1 FROM resource_updates ru
			JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE ru.stack_label = all_expired.stack_label
			AND fc.state NOT IN ('Success', 'Failed', 'Canceled')
		)
		ORDER BY valid_from
	`

	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []ExpiredStackInfo
	for rows.Next() {
		var info ExpiredStackInfo
		var onDependents sql.NullString
		if err := rows.Scan(&info.StackLabel, &info.StackID, &onDependents); err != nil {
			return nil, err
		}
		if onDependents.Valid {
			info.OnDependents = onDependents.String
		} else {
			info.OnDependents = "abort" // default
		}
		result = append(result, info)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (d DatastoreSQLite) CreateTarget(target *pkgmodel.Target) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "CreateTarget")
	defer span.End()

	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO targets (label, version, namespace, config, discoverable) VALUES (?, 1, ?, ?, ?)`
	_, err = d.conn.Exec(query, target.Label, target.Namespace, cfg, boolToInt(target.Discoverable))
	if err != nil {
		slog.Error("Failed to create target", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d DatastoreSQLite) UpdateTarget(target *pkgmodel.Target) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "UpdateTarget")
	defer span.End()

	query := `SELECT MAX(version) FROM targets WHERE label = ?`
	row := d.conn.QueryRow(query, target.Label)

	var maxVersion sql.NullInt64
	if err := row.Scan(&maxVersion); err != nil {
		return "", err
	}

	if !maxVersion.Valid {
		return "", fmt.Errorf("target %s does not exist, cannot update", target.Label)
	}

	newVersion := int(maxVersion.Int64) + 1

	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	insertQuery := `INSERT INTO targets (label, version, namespace, config, discoverable) VALUES (?, ?, ?, ?, ?)`
	_, err = d.conn.Exec(insertQuery, target.Label, newVersion, target.Namespace, cfg, boolToInt(target.Discoverable))
	if err != nil {
		slog.Error("Failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

func (d DatastoreSQLite) DeleteTarget(targetLabel string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "DeleteTarget")
	defer span.End()

	// Hard delete all versions of the target
	query := `DELETE FROM targets WHERE label = ?`
	result, err := d.conn.Exec(query, targetLabel)
	if err != nil {
		slog.Error("Failed to delete target", "error", err, "label", targetLabel)
		return "", err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}

	if rowsAffected == 0 {
		return "", fmt.Errorf("target %s does not exist, cannot delete", targetLabel)
	}

	return fmt.Sprintf("%s_deleted", targetLabel), nil
}

func (d DatastoreSQLite) CountResourcesInTarget(targetLabel string) (int, error) {
	_, span := sqliteTracer.Start(context.Background(), "CountResourcesInTarget")
	defer span.End()

	// Count only latest version of resources that haven't been deleted
	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE target = ?
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		AND operation != ?
	`
	row := d.conn.QueryRow(query, targetLabel, resource_update.OperationDelete)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func (d DatastoreSQLite) LoadTarget(label string) (*pkgmodel.Target, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadTarget")
	defer span.End()

	query := `SELECT version, namespace, config, discoverable FROM targets WHERE label = ? ORDER BY version DESC LIMIT 1`
	row := d.conn.QueryRow(query, label)

	var version int
	var namespace string
	var config json.RawMessage
	var discoverable int
	if err := row.Scan(&version, &namespace, &config, &discoverable); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Target not found, return nil without error
		}
		return nil, err
	}

	return &pkgmodel.Target{
		Label:        label,
		Namespace:    namespace,
		Config:       config,
		Discoverable: discoverable == 1,
		Version:      version,
	}, nil
}

func (d DatastoreSQLite) LoadAllTargets() ([]*pkgmodel.Target, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadAllTargets")
	defer span.End()

	var targets []*pkgmodel.Target

	query := `
		SELECT label, version, namespace, config, discoverable
		FROM targets t1
		WHERE NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)`
	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var discoverable int
		if err := rows.Scan(&label, &version, &namespace, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable == 1,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastoreSQLite) LoadTargetsByLabels(targetNames []string) ([]*pkgmodel.Target, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadTargetsByLabels")
	defer span.End()

	if len(targetNames) == 0 {
		return []*pkgmodel.Target{}, nil
	}

	placeholders := make([]string, len(targetNames))
	args := make([]any, len(targetNames))

	for i, name := range targetNames {
		placeholders[i] = "?"
		args[i] = name
	}

	query := fmt.Sprintf(`
		SELECT t1.label, t1.version, t1.namespace, t1.config, t1.discoverable
		FROM targets t1
		WHERE t1.label IN (%s)
		AND NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)`, strings.Join(placeholders, ","))

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var discoverable int
		if err := rows.Scan(&label, &version, &namespace, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable == 1,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastoreSQLite) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadDiscoverableTargets")
	defer span.End()

	// Get latest version per label where discoverable = true
	// Deduplicate by config across all namespaces
	query := `
		WITH latest_targets AS (
			SELECT label, version, namespace, config, discoverable
			FROM targets t1
			WHERE discoverable = 1
			AND NOT EXISTS (
				SELECT 1
				FROM targets t2
				WHERE t1.label = t2.label
				AND t2.version > t1.version
			)
		)
		SELECT label, version, namespace, config, discoverable
		FROM latest_targets
		GROUP BY config
		HAVING version = MAX(version)`

	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, ns string
		var version int
		var config json.RawMessage
		var discoverable int
		if err := rows.Scan(&label, &version, &ns, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    ns,
			Config:       config,
			Discoverable: discoverable == 1,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastoreSQLite) QueryTargets(query *TargetQuery) ([]*pkgmodel.Target, error) {
	_, span := sqliteTracer.Start(context.Background(), "QueryTargets")
	defer span.End()

	queryStr := `
		SELECT label, version, namespace, config, discoverable
		FROM targets t1
		WHERE NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)`
	args := []any{}

	queryStr = extendSQLiteQueryString(queryStr, query.Label, " AND label %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Namespace, " AND namespace %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Discoverable, " AND discoverable %s ?", &args)
	queryStr += " ORDER BY label"

	slog.Debug("QueryTargets", "queryStr", queryStr, "args", args)

	rows, err := d.conn.Query(queryStr, args...)
	if err != nil {
		slog.Error("QueryTargets failed", "error", err)
		return nil, err
	}
	defer closeRows(rows)

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var discoverable int
		if err := rows.Scan(&label, &version, &namespace, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable == 1,
			Version:      version,
		})
	}

	slog.Debug("QueryTargets results", "count", len(targets))
	return targets, rows.Err()
}

func (d DatastoreSQLite) LatestLabelForResource(label string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "LatestLabelForResource")
	defer span.End()

	query := `
    SELECT label FROM resources
    WHERE label = ? OR label LIKE ? || '-%'
    ORDER BY LENGTH(label) DESC, label DESC
    LIMIT 1;
    `
	row := d.conn.QueryRow(query, label, label)

	var latestLabel string
	if err := row.Scan(&latestLabel); err != nil {
		if err == sql.ErrNoRows {
			return "", nil // Resource not found, return empty string
		}
		return "", err
	}

	return latestLabel, nil
}

func (d DatastoreSQLite) Stats() (*stats.Stats, error) {
	_, span := sqliteTracer.Start(context.Background(), "Stats")
	defer span.End()

	res := stats.Stats{}

	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", CommandsTable)
	row := d.conn.QueryRow(clientsQuery)

	var clientCount int
	if err := row.Scan(&clientCount); err != nil {
		return nil, err
	}

	res.Clients = clientCount

	commandsQuery := fmt.Sprintf("SELECT command, COUNT(*) FROM %s WHERE command != ? GROUP BY command", CommandsTable)
	rows, err := d.conn.Query(commandsQuery, pkgmodel.CommandSync)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	res.Commands = make(map[string]int)
	for rows.Next() {
		var command string
		var count int
		if err = rows.Scan(&command, &count); err != nil {
			return nil, err
		}

		res.Commands[command] = count
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	statusQuery := fmt.Sprintf("SELECT state, COUNT(*) FROM %s WHERE command != ? GROUP BY state", CommandsTable)
	rows, err = d.conn.Query(statusQuery, pkgmodel.CommandSync)
	if err != nil {
		return nil, err
	}

	defer closeRows(rows)

	res.States = make(map[string]int)
	for rows.Next() {
		var state string
		var count int
		if err = rows.Scan(&state, &count); err != nil {
			return nil, err
		}

		res.States[state] = count
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	stacksQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT stack)
		FROM resources r1
		WHERE stack IS NOT NULL
		AND stack != '%s'
		AND operation != ?
		AND NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)`, constants.UnmanagedStack)
	row = d.conn.QueryRow(stacksQuery, resource_update.OperationDelete)

	var stackCount int
	if err = row.Scan(&stackCount); err != nil {
		return nil, err
	}

	res.Stacks = stackCount

	res.ManagedResources = make(map[string]int)
	managedResourcesQuery := fmt.Sprintf(`
		SELECT SUBSTR(type, 1, INSTR(type, '::') - 1) as namespace, COUNT(*)
		FROM resources r1
		WHERE stack IS NOT NULL
		AND stack != '%s'
		AND operation != ?
		AND NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		GROUP BY namespace`, constants.UnmanagedStack)
	rows, err = d.conn.Query(managedResourcesQuery, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var namespace string
		var count int
		if err = rows.Scan(&namespace, &count); err != nil {
			return nil, err
		}
		res.ManagedResources[namespace] = count
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	res.UnmanagedResources = make(map[string]int)
	unmanagedResourcesQuery := fmt.Sprintf(`
		SELECT SUBSTR(type, 1, INSTR(type, '::') - 1) as namespace, COUNT(*)
		FROM resources r1
		WHERE stack = '%s'
		AND operation != ?
		AND NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		GROUP BY namespace`, constants.UnmanagedStack)
	rows, err = d.conn.Query(unmanagedResourcesQuery, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var namespace string
		var count int
		if err = rows.Scan(&namespace, &count); err != nil {
			return nil, err
		}
		res.UnmanagedResources[namespace] = count
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	res.Targets = make(map[string]int)
	targetsQuery := `
		SELECT namespace, COUNT(*)
		FROM targets t1
		WHERE NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)
		GROUP BY namespace
	`
	rows, err = d.conn.Query(targetsQuery)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var namespace string
		var count int
		if err = rows.Scan(&namespace, &count); err != nil {
			return nil, err
		}
		res.Targets[namespace] = count
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	res.ResourceTypes = make(map[string]int)
	resourceTypesQuery := `
		SELECT type, COUNT(*)
		FROM resources r1
		WHERE operation != ?
		AND NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		GROUP BY type
	`
	rows, err = d.conn.Query(resourceTypesQuery, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var resourceType string
		var count int
		if err = rows.Scan(&resourceType, &count); err != nil {
			return nil, err
		}

		res.ResourceTypes[resourceType] = count
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	res.ResourceErrors = make(map[string]int)
	resourceErrorsQuery := `
		SELECT json_extract(resource, '$.Type') as resource_type, COUNT(*)
		FROM resource_updates
		WHERE state = ?
		AND resource IS NOT NULL
		GROUP BY resource_type
	`
	rows, err = d.conn.Query(resourceErrorsQuery, types.ResourceUpdateStateFailed)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	for rows.Next() {
		var resourceType string
		var count int
		if err = rows.Scan(&resourceType, &count); err != nil {
			return nil, err
		}
		if resourceType != "" {
			res.ResourceErrors[resourceType] = count
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return &res, nil
}

func (d DatastoreSQLite) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadResourceById")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources
	WHERE ksuid = ?
	AND operation != ?
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.conn.QueryRow(query, ksuid, resource_update.OperationDelete)

	var jsonData string
	var ksuidResult string
	if err := row.Scan(&jsonData, &ksuidResult); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Resource not found
		}
		return nil, err
	}

	var loadedResource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &loadedResource); err != nil {
		return nil, err
	}

	// Set the KSUID directly since we already have it
	loadedResource.Ksuid = ksuidResult

	return &loadedResource, nil
}

// FindResourcesDependingOn finds resources that reference the given KSUID via $ref in their properties.
// This is essential for referential integrity — without it we risk leaving orphaned resources in an
// inconsistent state. Currently this requires a full table scan (LIKE on the data column) which will
// be slow for users with large resource counts.
// TODO: make the dependency graph discoverable from the schema so we can query edges directly.
func (d DatastoreSQLite) FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error) {
	_, span := sqliteTracer.Start(context.Background(), "FindResourcesDependingOn")
	defer span.End()

	// Search for resources that contain a $ref to this KSUID in their properties
	// The format is: "formae://KSUID#/..." (JSON without spaces after colons)
	pattern := fmt.Sprintf("%%\"$ref\":\"formae://%s#%%", ksuid)

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE data LIKE ?
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	AND operation != ?
	`

	rows, err := d.conn.Query(query, pattern, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var resources []*pkgmodel.Resource
	for rows.Next() {
		var jsonData, ksuidResult string
		if err := rows.Scan(&jsonData, &ksuidResult); err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}
		resource.Ksuid = ksuidResult
		resources = append(resources, &resource)
	}

	return resources, nil
}

func (d DatastoreSQLite) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
	_, span := sqliteTracer.Start(context.Background(), "GetKSUIDByTriplet")
	defer span.End()

	query := `
	SELECT ksuid
	FROM resources
	WHERE stack = ? AND label = ? AND LOWER(type) = LOWER(?)
	AND operation != ?
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.conn.QueryRow(query, stack, label, resourceType, resource_update.OperationDelete)

	var ksuidResult string
	if err := row.Scan(&ksuidResult); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	return ksuidResult, nil
}

func (d DatastoreSQLite) BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	_, span := sqliteTracer.Start(context.Background(), "BatchGetKSUIDsByTriplets")
	defer span.End()

	if len(triplets) == 0 {
		return make(map[pkgmodel.TripletKey]string), nil
	}

	// Build the IN clause with triplet values
	placeholders := make([]string, len(triplets))
	args := make([]any, len(triplets)*3)

	for i, triplet := range triplets {
		placeholders[i] = "(?, ?, ?)"
		args[i*3] = triplet.Stack
		args[i*3+1] = triplet.Label
		args[i*3+2] = triplet.Type
	}

	query := fmt.Sprintf(`
		SELECT stack, label, type, ksuid
		FROM resources r1
		WHERE (stack, label, type) IN (%s)
		AND r1.operation != ?
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
			AND r2.version > r1.version
		)`, strings.Join(placeholders, ","))

	args = append(args, resource_update.OperationDelete)
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	result := make(map[pkgmodel.TripletKey]string)
	for rows.Next() {
		var stack, label, resourceType, ksuid string
		if err := rows.Scan(&stack, &label, &resourceType, &ksuid); err != nil {
			return nil, err
		}

		key := pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}
		result[key] = ksuid
	}

	return result, rows.Err()
}

func (d DatastoreSQLite) BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error) {
	_, span := sqliteTracer.Start(context.Background(), "BatchGetTripletsByKSUIDs")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string]pkgmodel.TripletKey), nil
	}

	// Build the IN clause with KSUID values
	placeholders := make([]string, len(ksuids))
	args := make([]any, len(ksuids))

	for i, ksuid := range ksuids {
		placeholders[i] = "?"
		args[i] = ksuid
	}

	query := fmt.Sprintf(`
		WITH latest_resources AS (
			SELECT ksuid, stack, label, type, managed, version,
			       ROW_NUMBER() OVER (PARTITION BY ksuid ORDER BY managed DESC, version DESC) as rn
			FROM resources
			WHERE ksuid IN (%s)
			AND operation != ?
		)
		SELECT ksuid, stack, label, type
		FROM latest_resources
		WHERE rn = 1`, strings.Join(placeholders, ","))

	// Add operation type to args for the delete check
	args = append(args, resource_update.OperationDelete)

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	result := make(map[string]pkgmodel.TripletKey)
	for rows.Next() {
		var ksuid, stack, label, resourceType string
		if err := rows.Scan(&ksuid, &stack, &label, &resourceType); err != nil {
			return nil, err
		}

		result[ksuid] = pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}
	}

	return result, rows.Err()
}

// BulkStoreResourceUpdates stores multiple ResourceUpdates in a single transaction
// This is the key performance optimization: insert all updates in one transaction
func (d DatastoreSQLite) BulkStoreResourceUpdates(commandID string, updates []resource_update.ResourceUpdate) error {
	_, span := sqliteTracer.Start(context.Background(), "BulkStoreResourceUpdates")
	defer span.End()

	if len(updates) == 0 {
		return nil
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO resource_updates (
			command_id, ksuid, operation, state, start_ts, modified_ts,
			retries, remaining, version, stack_label, group_id, source,
			resource, resource_target, existing_resource, existing_target,
			progress_result, most_recent_progress,
			remaining_resolvables, reference_labels, previous_properties
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, ru := range updates {
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

		// Use StackLabel if set, otherwise fallback to Resource.Stack
		stackLabel := ru.StackLabel
		if stackLabel == "" {
			stackLabel = ru.DesiredState.Stack
		}

		// Normalize timestamps to UTC for consistent TEXT-based sorting in SQLite
		startTsUTC := ru.StartTs.UTC()
		modifiedTsUTC := ru.ModifiedTs.UTC()

		_, err = stmt.Exec(
			commandID,
			ru.DesiredState.Ksuid,
			string(ru.Operation),
			string(ru.State),
			startTsUTC,
			modifiedTsUTC,
			ru.Retries,
			ru.Remaining,
			ru.Version,
			stackLabel,
			ru.GroupID,
			string(ru.Source),
			resourceJSON,
			resourceTargetJSON,
			existingResourceJSON,
			existingTargetJSON,
			progressResultJSON,
			mostRecentProgressJSON,
			remainingResolvablesJSON,
			referenceLabelsJSON,
			ru.PreviousProperties,
		)
		if err != nil {
			return fmt.Errorf("failed to insert resource update: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// LoadResourceUpdates loads all ResourceUpdates for a given command
func (d DatastoreSQLite) LoadResourceUpdates(commandID string) ([]resource_update.ResourceUpdate, error) {
	_, span := sqliteTracer.Start(context.Background(), "LoadResourceUpdates")
	defer span.End()

	query := `
		SELECT ksuid, operation, state, start_ts, modified_ts,
			retries, remaining, version, stack_label, group_id, source,
			resource, resource_target, existing_resource, existing_target,
			progress_result, most_recent_progress,
			remaining_resolvables, reference_labels, previous_properties
		FROM resource_updates
		WHERE command_id = ?
	`

	rows, err := d.conn.Query(query, commandID)
	if err != nil {
		return nil, fmt.Errorf("failed to query resource updates: %w", err)
	}
	defer closeRows(rows)

	var updates []resource_update.ResourceUpdate
	for rows.Next() {
		var ru resource_update.ResourceUpdate
		var ksuid, operation, state string
		var startTsStr, modifiedTsStr sql.NullString
		var stackLabel, groupID, source, version sql.NullString
		var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
		var progressResultJSON, mostRecentProgressJSON []byte
		var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte

		err := rows.Scan(
			&ksuid,
			&operation,
			&state,
			&startTsStr,
			&modifiedTsStr,
			&ru.Retries,
			&ru.Remaining,
			&version,
			&stackLabel,
			&groupID,
			&source,
			&resourceJSON,
			&resourceTargetJSON,
			&existingResourceJSON,
			&existingTargetJSON,
			&progressResultJSON,
			&mostRecentProgressJSON,
			&remainingResolvablesJSON,
			&referenceLabelsJSON,
			&previousPropertiesJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan resource update: %w", err)
		}

		ru.Operation = types.OperationType(operation)
		ru.State = resource_update.ResourceUpdateState(state)

		// Parse timestamps (TIMESTAMP columns)
		if startTsStr.Valid && startTsStr.String != "" {
			if ts, err := time.Parse(time.RFC3339Nano, startTsStr.String); err == nil {
				ru.StartTs = ts.UTC()
			} else if ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", startTsStr.String); err == nil {
				ru.StartTs = ts.UTC()
			}
		}
		if modifiedTsStr.Valid && modifiedTsStr.String != "" {
			if ts, err := time.Parse(time.RFC3339Nano, modifiedTsStr.String); err == nil {
				ru.ModifiedTs = ts.UTC()
			} else if ts, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", modifiedTsStr.String); err == nil {
				ru.ModifiedTs = ts.UTC()
			}
		}
		ru.Version = version.String
		ru.StackLabel = stackLabel.String
		ru.GroupID = groupID.String
		ru.Source = resource_update.FormaCommandSource(source.String)

		if err := json.Unmarshal(resourceJSON, &ru.DesiredState); err != nil {
			return nil, fmt.Errorf("failed to unmarshal resource: %w", err)
		}
		ru.DesiredState.Ksuid = ksuid

		if err := json.Unmarshal(resourceTargetJSON, &ru.ResourceTarget); err != nil {
			return nil, fmt.Errorf("failed to unmarshal resource target: %w", err)
		}

		if err := json.Unmarshal(existingResourceJSON, &ru.PriorState); err != nil {
			return nil, fmt.Errorf("failed to unmarshal existing resource: %w", err)
		}

		if err := json.Unmarshal(existingTargetJSON, &ru.ExistingTarget); err != nil {
			return nil, fmt.Errorf("failed to unmarshal existing target: %w", err)
		}

		if err := json.Unmarshal(progressResultJSON, &ru.ProgressResult); err != nil {
			return nil, fmt.Errorf("failed to unmarshal progress result: %w", err)
		}

		if err := json.Unmarshal(mostRecentProgressJSON, &ru.MostRecentProgressResult); err != nil {
			return nil, fmt.Errorf("failed to unmarshal most recent progress: %w", err)
		}

		if err := json.Unmarshal(remainingResolvablesJSON, &ru.RemainingResolvables); err != nil {
			return nil, fmt.Errorf("failed to unmarshal remaining resolvables: %w", err)
		}

		if err := json.Unmarshal(referenceLabelsJSON, &ru.ReferenceLabels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal reference labels: %w", err)
		}

		ru.PreviousProperties = previousPropertiesJSON

		updates = append(updates, ru)
	}

	return updates, rows.Err()
}

// UpdateResourceUpdateState updates the state of a single ResourceUpdate
// This is the key performance improvement: updating one row instead of re-serializing entire command
func (d DatastoreSQLite) UpdateResourceUpdateState(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	_, span := sqliteTracer.Start(context.Background(), "UpdateResourceUpdateState")
	defer span.End()

	query := `
		UPDATE resource_updates
		SET state = ?, modified_ts = ?
		WHERE command_id = ? AND ksuid = ? AND operation = ?
	`

	// Normalize timestamp to UTC for consistent TEXT-based sorting in SQLite
	result, err := d.conn.Exec(query, string(state), modifiedTs.UTC(), commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update state: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}

	return nil
}

// UpdateResourceUpdateProgress updates a ResourceUpdate with progress information
func (d DatastoreSQLite) UpdateResourceUpdateProgress(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time, progress plugin.TrackedProgress) error {
	_, span := sqliteTracer.Start(context.Background(), "UpdateResourceUpdateProgress")
	defer span.End()

	// First, load existing progress results to append to
	var existingProgressJSON []byte
	query := `SELECT progress_result FROM resource_updates WHERE command_id = ? AND ksuid = ? AND operation = ?`
	err := d.conn.QueryRow(query, commandID, ksuid, string(operation)).Scan(&existingProgressJSON)
	if err != nil {
		return fmt.Errorf("failed to load existing progress: %w", err)
	}

	var existingProgress []plugin.TrackedProgress
	if len(existingProgressJSON) > 0 {
		if err := json.Unmarshal(existingProgressJSON, &existingProgress); err != nil {
			return fmt.Errorf("failed to unmarshal existing progress: %w", err)
		}
	}

	// Append new progress
	existingProgress = append(existingProgress, progress)

	progressJSON, err := json.Marshal(existingProgress)
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %w", err)
	}

	mostRecentJSON, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal most recent progress: %w", err)
	}

	updateQuery := `
		UPDATE resource_updates
		SET state = ?, modified_ts = ?, progress_result = ?, most_recent_progress = ?
		WHERE command_id = ? AND ksuid = ? AND operation = ?
	`

	// Normalize timestamp to UTC for consistent TEXT-based sorting in SQLite
	result, err := d.conn.Exec(updateQuery, string(state), modifiedTs.UTC(), progressJSON, mostRecentJSON, commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update progress: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}

	return nil
}

// BatchUpdateResourceUpdateState updates multiple ResourceUpdates to the same state
// Used for bulk operations like marking dependent resources as failed
func (d DatastoreSQLite) BatchUpdateResourceUpdateState(commandID string, refs []ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	_, span := sqliteTracer.Start(context.Background(), "BatchUpdateResourceUpdateState")
	defer span.End()

	if len(refs) == 0 {
		return nil
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		UPDATE resource_updates
		SET state = ?, modified_ts = ?
		WHERE command_id = ? AND ksuid = ? AND operation = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	// Normalize timestamp to UTC for consistent TEXT-based sorting in SQLite
	modifiedTsUTC := modifiedTs.UTC()
	for _, ref := range refs {
		_, err = stmt.Exec(string(state), modifiedTsUTC, commandID, ref.KSUID, string(ref.Operation))
		if err != nil {
			return fmt.Errorf("failed to update resource update: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// UpdateFormaCommandProgress updates only the command-level metadata (state, modified_ts)
// without re-writing all ResourceUpdates. This is a performance optimization for
// progress updates where the ResourceUpdate is already updated via UpdateResourceUpdateProgress.
func (d DatastoreSQLite) UpdateFormaCommandProgress(commandID string, state forma_command.CommandState, modifiedTs time.Time) error {
	_, span := sqliteTracer.Start(context.Background(), "UpdateFormaCommandProgress")
	defer span.End()

	modifiedTsUTC := modifiedTs.UTC().Format(time.RFC3339Nano)

	result, err := d.conn.Exec(
		`UPDATE forma_commands SET state = ?, modified_ts = ? WHERE command_id = ?`,
		string(state), modifiedTsUTC, commandID,
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

func (d DatastoreSQLite) CleanUp() error {
	// No cleanup needed for SQLite, this is only used in the Postgres integration tests
	return nil
}
