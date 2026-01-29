// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/demula/mksuid/v2"
	"github.com/exaring/otelpgx"
	json "github.com/goccy/go-json"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// tracer is used for creating spans within datastore methods to group SQL queries
var tracer trace.Tracer

func init() {
	tracer = otel.Tracer("formae/datastore")
}

type DatastorePostgres struct {
	pool    *pgxpool.Pool
	agentID string
	cfg     *pkgmodel.DatastoreConfig
	ctx     context.Context
}

// This can be only used in tests or in setups where we have access to admin (non-production)
func ensureDatabaseExists(ctx context.Context, cfg *pkgmodel.DatastoreConfig) error {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/postgres",
		cfg.Postgres.User, cfg.Postgres.Password, cfg.Postgres.Host, cfg.Postgres.Port)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to admin database: %w", err)
	}

	defer func() {
		if err := conn.Close(ctx); err != nil {
			slog.Error("failed to close connection", "error", err)
		}
	}()

	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)"
	err = conn.QueryRow(ctx, query, cfg.Postgres.Database).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if !exists {
		_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{cfg.Postgres.Database}.Sanitize()))
		if err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
	}

	return nil
}

// This can be only used in tests or in setups where we have access to admin (non-production)
func NewDatastorePostgresEnsureDatabase(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (Datastore, error) {
	err := ensureDatabaseExists(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return NewDatastorePostgres(ctx, cfg, agentID)
}

func NewDatastorePostgres(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (Datastore, error) {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		cfg.Postgres.User, cfg.Postgres.Password, cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.Database)

	// Append connection parameters if provided
	if cfg.Postgres.ConnectionParams != "" {
		connStr = fmt.Sprintf("%s?%s", connStr, cfg.Postgres.ConnectionParams)
	}

	// Append schema if provided
	if cfg.Postgres.Schema != "" {
		if strings.Contains(connStr, "?") {
			connStr = fmt.Sprintf("%s&search_path=%s", connStr, cfg.Postgres.Schema)
		} else {
			connStr = fmt.Sprintf("%s?search_path=%s", connStr, cfg.Postgres.Schema)
		}
	}

	migrationDB, err := sql.Open("pgx", connStr)
	if err != nil {
		slog.Error("failed to open database for migrations", "error", err)
		return nil, err
	}
	defer func() {
		if err := migrationDB.Close(); err != nil {
			slog.Warn("failed to close migration database", "error", err)
		}
	}()

	if err = runMigrations(migrationDB, "postgres"); err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		slog.Error("failed to parse postgres connection string", "error", err)
		return nil, err
	}
	poolCfg.ConnConfig.Tracer = otelpgx.NewTracer(
		// Include SQL statement in db.statement attribute and span name
		// Connection details are still disabled for security
		otelpgx.WithDisableConnectionDetailsInAttributes(),
	)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		slog.Error("failed to connect to PostgreSQL database", "error", err)
		return nil, err
	}

	// Record pool statistics as OTel metrics (connections idle/in-use/waiting, acquire time, etc.)
	if err := otelpgx.RecordStats(pool); err != nil {
		slog.Error("failed to start recording pool stats", "error", err)
		// Non-fatal - continue without pool metrics
	}

	d := DatastorePostgres{pool: pool, agentID: agentID, cfg: cfg, ctx: ctx}

	slog.Info("Started PostgreSQL datastore", "host", cfg.Postgres.Host, "port", cfg.Postgres.Port, "database", cfg.Postgres.Database, "schema", cfg.Postgres.Schema, "user", cfg.Postgres.User, "connectionParams", cfg.Postgres.ConnectionParams)

	return d, nil
}

func (d DatastorePostgres) StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	ctx, span := tracer.Start(context.Background(), "StoreFormaCommand")
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

	// We no longer store the forma JSON - Description and Config are stored as normalized columns
	query := fmt.Sprintf(`
	INSERT INTO %s (command_id, timestamp, command, state, agent_version, client_id, agent_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, modified_ts)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	ON CONFLICT (command_id) DO UPDATE
	SET timestamp = EXCLUDED.timestamp,
	command = EXCLUDED.command,
	state = EXCLUDED.state,
	agent_version = EXCLUDED.agent_version,
	client_id = EXCLUDED.client_id,
	agent_id = EXCLUDED.agent_id,
	description_text = EXCLUDED.description_text,
	description_confirm = EXCLUDED.description_confirm,
	config_mode = EXCLUDED.config_mode,
	config_force = EXCLUDED.config_force,
	config_simulate = EXCLUDED.config_simulate,
	target_updates = EXCLUDED.target_updates,
	modified_ts = EXCLUDED.modified_ts
	`, CommandsTable)

	_, err = d.pool.Exec(ctx, query, commandID, fa.StartTs.UTC(), fa.Command, fa.State, formae.Version, fa.ClientID, d.agentID,
		fa.Description.Text, fa.Description.Confirm, fa.Config.Mode, fa.Config.Force, fa.Config.Simulate,
		targetUpdatesJSON, fa.ModifiedTs.UTC())
	if err != nil {
		slog.Error("failed to store FormaCommand", "query", query, "error", err)
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

const formaCommandWithResourceUpdatesQueryBasePostgres = `
SELECT
	fc.command_id, fc.timestamp, fc.command, fc.state, fc.client_id,
	fc.description_text, fc.description_confirm, fc.config_mode, fc.config_force, fc.config_simulate,
	fc.target_updates, fc.modified_ts,
	ru.ksuid, ru.operation, ru.state, ru.start_ts, ru.modified_ts,
	ru.retries, ru.remaining, ru.version, ru.stack_label, ru.group_id, ru.source,
	ru.resource, ru.resource_target, ru.existing_resource, ru.existing_target,
	ru.progress_result, ru.most_recent_progress,
	ru.remaining_resolvables, ru.reference_labels, ru.previous_properties
FROM forma_commands fc
LEFT JOIN resource_updates ru ON fc.command_id = ru.command_id`

const resourceUpdateOrderByPostgres = " ORDER BY fc.timestamp DESC, ru.ksuid ASC"

// scanJoinedRowPostgres scans a row from the joined FormaCommand + ResourceUpdate query
// Returns the FormaCommand and optionally a ResourceUpdate (nil if LEFT JOIN has no match)
func scanJoinedRowPostgres(rows pgx.Rows) (*forma_command.FormaCommand, *resource_update.ResourceUpdate, error) {
	var cmd forma_command.FormaCommand
	var commandID, fcCommand, fcState string
	var fcTimestamp time.Time
	var fcClientID *string
	var descriptionText *string
	var descriptionConfirm *bool
	var configMode *string
	var configForce, configSimulate *bool
	var targetUpdatesJSON []byte
	var fcModifiedTs *time.Time

	// ResourceUpdate fields (all nullable due to LEFT JOIN)
	var ruKsuid, ruOperation, ruState *string
	var ruStartTs, ruModifiedTs *time.Time
	var ruRetries, ruRemaining *uint16
	var ruVersion, ruStackLabel, ruGroupID, ruSource *string
	var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
	var progressResultJSON, mostRecentProgressJSON []byte
	var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte

	err := rows.Scan(
		// FormaCommand columns
		&commandID, &fcTimestamp, &fcCommand, &fcState, &fcClientID,
		&descriptionText, &descriptionConfirm, &configMode, &configForce, &configSimulate,
		&targetUpdatesJSON, &fcModifiedTs,
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
	cmd.StartTs = fcTimestamp
	cmd.Command = pkgmodel.Command(fcCommand)
	cmd.State = forma_command.CommandState(fcState)
	if fcClientID != nil {
		cmd.ClientID = *fcClientID
	}

	// Read description from normalized columns
	if descriptionText != nil {
		cmd.Description.Text = *descriptionText
	}
	cmd.Description.Confirm = descriptionConfirm != nil && *descriptionConfirm

	// Read config from normalized columns
	if configMode != nil {
		cmd.Config.Mode = pkgmodel.FormaApplyMode(*configMode)
	}
	cmd.Config.Force = configForce != nil && *configForce
	cmd.Config.Simulate = configSimulate != nil && *configSimulate

	if fcModifiedTs != nil {
		cmd.ModifiedTs = *fcModifiedTs
	}

	if len(targetUpdatesJSON) > 0 {
		if err := json.Unmarshal(targetUpdatesJSON, &cmd.TargetUpdates); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal target updates: %w", err)
		}
	}

	// Check if there's a ResourceUpdate (LEFT JOIN may return NULL)
	if ruKsuid == nil {
		return &cmd, nil, nil
	}

	// Populate ResourceUpdate
	var ru resource_update.ResourceUpdate
	ru.Operation = types.OperationType(*ruOperation)
	ru.State = resource_update.ResourceUpdateState(*ruState)

	if ruStartTs != nil {
		ru.StartTs = *ruStartTs
	}
	if ruModifiedTs != nil {
		ru.ModifiedTs = *ruModifiedTs
	}

	if ruRetries != nil {
		ru.Retries = *ruRetries
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

	return &cmd, &ru, nil
}

// loadFormaCommandsFromJoinedRowsPostgres processes rows from a joined query
// and groups ResourceUpdates by FormaCommand ID
func loadFormaCommandsFromJoinedRowsPostgres(rows pgx.Rows) ([]*forma_command.FormaCommand, error) {
	defer rows.Close()

	// Use a map to collect ResourceUpdates for each command
	commandMap := make(map[string]*forma_command.FormaCommand)
	var commandOrder []string // Preserve order

	for rows.Next() {
		cmd, ru, err := scanJoinedRowPostgres(rows)
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

func (d DatastorePostgres) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx, span := tracer.Start(context.Background(), "LoadFormaCommands")
	defer span.End()

	rows, err := d.pool.Query(ctx, formaCommandWithResourceUpdatesQueryBasePostgres+resourceUpdateOrderByPostgres)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRowsPostgres(rows)
}

func (d DatastorePostgres) DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	ctx, span := tracer.Start(context.Background(), "DeleteFormaCommand")
	defer span.End()

	// Delete resource_updates first (no FK constraint, so we must do this manually)
	_, err := d.pool.Exec(ctx, "DELETE FROM resource_updates WHERE command_id = $1", commandID)
	if err != nil {
		return fmt.Errorf("failed to delete resource_updates: %w", err)
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE command_id = $1", CommandsTable)
	_, err = d.pool.Exec(ctx, query, commandID)
	return err
}

func (d DatastorePostgres) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	ctx, span := tracer.Start(context.Background(), "GetFormaCommandByCommandID")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBasePostgres + " WHERE fc.command_id = $1" + resourceUpdateOrderByPostgres
	rows, err := d.pool.Query(ctx, query, commandID)
	if err != nil {
		return nil, err
	}

	commands, err := loadFormaCommandsFromJoinedRowsPostgres(rows)
	if err != nil {
		return nil, err
	}

	if len(commands) == 0 {
		return nil, fmt.Errorf("forma command not found: %v", commandID)
	}

	return commands[0], nil
}

func (d DatastorePostgres) GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error) {
	ctx, span := tracer.Start(context.Background(), "GetMostRecentFormaCommandByClientID")
	defer span.End()

	// Use subquery to find the most recent command_id first, then fetch all its resource_updates
	// LIMIT 1 on the joined query would only return 1 row, not 1 command
	query := formaCommandWithResourceUpdatesQueryBasePostgres +
		" WHERE fc.command_id = (SELECT command_id FROM forma_commands WHERE client_id = $1 ORDER BY timestamp DESC LIMIT 1)" +
		resourceUpdateOrderByPostgres
	rows, err := d.pool.Query(ctx, query, clientID)
	if err != nil {
		return nil, err
	}

	commands, err := loadFormaCommandsFromJoinedRowsPostgres(rows)
	if err != nil {
		return nil, err
	}

	if len(commands) == 0 {
		return nil, fmt.Errorf("no forma commands found for client: %v", clientID)
	}

	return commands[0], nil
}

func extendPostgresQueryString[T any](queryStr string, queryItem *QueryItem[T], sqlPart string, args *[]any) string {
	if queryItem != nil {
		var operator string

		if queryItem.Constraint == Excluded {
			operator = "!="
		} else if queryItem.Constraint == Required || queryItem.Constraint == Optional {
			operator = "="
		}

		queryStr += fmt.Sprintf(sqlPart, operator, len(*args)+1)
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

func (d DatastorePostgres) QueryFormaCommands(query *StatusQuery) ([]*forma_command.FormaCommand, error) {
	ctx, span := tracer.Start(context.Background(), "QueryFormaCommands")
	defer span.End()

	// Build subquery to find matching command IDs with filtering and LIMIT
	subqueryStr := "SELECT command_id FROM forma_commands WHERE 1=1"
	args := []any{}

	subqueryStr = extendPostgresQueryString(subqueryStr, query.CommandID, " AND command_id %s $%d", &args)
	subqueryStr = extendPostgresQueryString(subqueryStr, query.ClientID, " AND client_id %s $%d", &args)
	subqueryStr = extendPostgresQueryString(subqueryStr, query.Command, " AND LOWER(command) %s LOWER($%d)", &args)
	if query.Command == nil {
		subqueryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}

	// Stack filter uses the normalized resource_updates table
	subqueryStr = extendPostgresQueryString(subqueryStr, query.Stack, " AND EXISTS (SELECT 1 FROM resource_updates ru WHERE ru.command_id = forma_commands.command_id AND ru.stack_label %s $%d)", &args)
	subqueryStr = extendPostgresQueryString(subqueryStr, query.Status, " AND LOWER(state) %s LOWER($%d)", &args)

	subqueryStr += " ORDER BY timestamp DESC"
	if query.N > 0 {
		subqueryStr += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, min(DefaultFormaCommandsQueryLimit, query.N))
	} else {
		subqueryStr += fmt.Sprintf(" LIMIT %d", DefaultFormaCommandsQueryLimit)
	}

	// Main query joins with resource_updates for commands matching the subquery
	queryStr := fmt.Sprintf(`
		SELECT
			fc.command_id, fc.timestamp, fc.command, fc.state, fc.client_id,
			fc.description_text, fc.description_confirm, fc.config_mode, fc.config_force, fc.config_simulate,
			fc.target_updates, fc.modified_ts,
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

	rows, err := d.pool.Query(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	return loadFormaCommandsFromJoinedRowsPostgres(rows)
}

func (d DatastorePostgres) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "GetKSUIDByTriplet")
	defer span.End()

	query := `
	SELECT ksuid
	FROM resources
	WHERE stack = $1 AND label = $2 AND LOWER(type) = LOWER($3)
	AND operation != $4
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, stack, label, resourceType, resource_update.OperationDelete)

	var ksuid string
	if err := row.Scan(&ksuid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}

	return ksuid, nil
}

func (d DatastorePostgres) BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	ctx, span := tracer.Start(context.Background(), "BatchGetKSUIDsByTriplets")
	defer span.End()

	if len(triplets) == 0 {
		return make(map[pkgmodel.TripletKey]string), nil
	}

	placeholders := make([]string, len(triplets))
	args := make([]any, len(triplets)*3)

	for i, triplet := range triplets {
		placeholders[i] = fmt.Sprintf("($%d, $%d, $%d)", i*3+1, i*3+2, i*3+3)
		args[i*3] = triplet.Stack
		args[i*3+1] = triplet.Label
		args[i*3+2] = triplet.Type
	}

	query := fmt.Sprintf(`
	SELECT stack, label, type, ksuid
	FROM resources r1
	WHERE (stack, label, type) IN (%s)
	AND r1.operation != $%d
	AND NOT EXISTS (
		SELECT 1 FROM resources r2
		WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
		AND r2.version > r1.version
	)
	`, strings.Join(placeholders, ","), len(triplets)*3+1)

	args = append(args, resource_update.OperationDelete)
	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("query error: %w", rows.Err())
	}

	defer rows.Close()

	result := make(map[pkgmodel.TripletKey]string)
	for rows.Next() {
		var stack, label, resourceType, ksuid string
		if err := rows.Scan(&stack, &label, &resourceType, &ksuid); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		key := pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}
		result[key] = ksuid
	}

	return result, nil
}

func (d DatastorePostgres) GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error) {
	ctx, span := tracer.Start(context.Background(), "GetResourceModificationsSinceLastReconcile")
	defer span.End()

	query := `
	SELECT DISTINCT
	T2.type,
	T2.label,
	T2.operation
	FROM forma_commands AS T1
	JOIN resources AS T2
	ON T1.command_id = T2.command_id
	WHERE
	EXISTS (
		SELECT 1
		FROM resources AS r1
		WHERE r1.stack = $1
		AND NOT EXISTS (
			SELECT 1
			FROM resources AS r2
			WHERE r1.ksuid = r2.ksuid
			AND r2.version > r1.version
		)
		AND r1.operation != 'delete'
	)
	AND T1.timestamp > (
		SELECT fc.timestamp
		FROM forma_commands fc
		WHERE fc.config_mode = 'reconcile'
		AND EXISTS (
			SELECT 1
			FROM resources r
			WHERE r.command_id = fc.command_id
			AND r.stack = $1
		)
		ORDER BY fc.timestamp DESC
		LIMIT 1
	)
	AND T2.stack = $1;
	`
	rows, err := d.pool.Query(ctx, query, stack)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	modifications := make(map[ResourceModification]struct{})
	for rows.Next() {
		var resourceType, label, operation string
		if err := rows.Scan(&resourceType, &label, &operation); err != nil {
			return nil, err
		}
		modifications[ResourceModification{Stack: stack, Type: resourceType, Label: label, Operation: operation}] = struct{}{}
	}

	result := make([]ResourceModification, 0, len(modifications))
	for mod := range modifications {
		result = append(result, mod)
	}

	return result, nil
}

func (d DatastorePostgres) BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error) {
	ctx, span := tracer.Start(context.Background(), "BatchGetTripletsByKSUIDs")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string]pkgmodel.TripletKey), nil
	}

	// Build the placeholders for the query
	placeholders := make([]string, len(ksuids))
	args := make([]any, len(ksuids))
	for i, ksuid := range ksuids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = ksuid
	}

	query := fmt.Sprintf(`
	WITH latest_resources AS (
		SELECT ksuid, stack, label, type, managed, version,
		       ROW_NUMBER() OVER (PARTITION BY ksuid ORDER BY managed DESC, version DESC) as rn
		FROM resources
		WHERE ksuid IN (%s)
		AND operation != $%d
	)
	SELECT ksuid, stack, label, type
	FROM latest_resources
	WHERE rn = 1
	`, strings.Join(placeholders, ","), len(ksuids)+1)

	// Add operation type to args for the delete check
	args = append(args, resource_update.OperationDelete)

	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (d DatastorePostgres) LatestLabelForResource(label string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "LatestLabelForResource")
	defer span.End()

	query := `
	SELECT label
	FROM resources
	WHERE label = $1 OR label LIKE $2 || '-%'
	ORDER BY LENGTH(label) DESC, label DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, label, label)

	var latestLabel string
	if err := row.Scan(&latestLabel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil // Resource not found, return empty string
		}
		return "", err
	}

	return latestLabel, nil
}

func (d DatastorePostgres) DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "DeleteResource")
	defer span.End()

	return d.storeResource(ctx, resource, []byte("{}"), commandID, string(resource_update.OperationDelete))
}

func (d DatastorePostgres) LoadAllResources() ([]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadAllResources")
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
	AND operation != $1
	`
	rows, err := d.pool.Query(ctx, query, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (d DatastorePostgres) LoadAllStacks() ([]*pkgmodel.Forma, error) {
	ctx, span := tracer.Start(context.Background(), "LoadAllStacks")
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
	AND operation != $1
	`

	rows, err := d.pool.Query(ctx, query, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	// Create Forma objects for each stack
	var stacks []*pkgmodel.Forma
	for _, stackResources := range stackResourcesMap {
		if len(stackResources) > 0 {
			forma := pkgmodel.FormaFromResources(stackResources)
			stacks = append(stacks, forma)
		}
	}

	return stacks, nil
}

func (d DatastorePostgres) LoadAllTargets() ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "LoadAllTargets")
	defer span.End()

	query := `
	SELECT label, version, namespace, config, discoverable
	FROM targets t1
	WHERE NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`

	rows, err := d.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var discoverable bool
		if err := rows.Scan(&label, &version, &namespace, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx, span := tracer.Start(context.Background(), "LoadIncompleteFormaCommands")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBasePostgres +
		" WHERE fc.command != 'sync' AND fc.state = $1" +
		resourceUpdateOrderByPostgres

	rows, err := d.pool.Query(ctx, query, forma_command.CommandStateInProgress)
	if err != nil {
		return nil, err
	}

	return loadFormaCommandsFromJoinedRowsPostgres(rows)
}

func (d DatastorePostgres) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadResource")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources
	WHERE uri = $1
	AND operation != $2
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, uri, resource_update.OperationDelete)

	var jsonData string
	var ksuid string
	if err := row.Scan(&jsonData, &ksuid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Resource not found, return nil without error
		}
		return nil, err
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}

	resource.Ksuid = ksuid
	return &resource, nil
}

func (d DatastorePostgres) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadResourceById")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources
	WHERE ksuid = $1
	AND operation != $2
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, ksuid, resource_update.OperationDelete)

	var jsonData string
	var ksuidResult string
	if err := row.Scan(&jsonData, &ksuidResult); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Resource not found
		}
		return nil, err
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}

	// Set the KSUID directly since we already have it
	resource.Ksuid = ksuidResult

	return &resource, nil
}

func (d DatastorePostgres) LoadResourceByNativeID(nativeID string, resourceType string) (*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadResourceByNativeID")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE native_id = $1 AND type = $2
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	AND r1.operation != $3
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, nativeID, resourceType, resource_update.OperationDelete)

	var jsonData string
	var ksuid string
	if err := row.Scan(&jsonData, &ksuid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Resource not found
		}
		return nil, err
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}

	resource.Ksuid = ksuid
	return &resource, nil
}

func (d DatastorePostgres) LoadStack(stackLabel string) (*pkgmodel.Forma, error) {
	ctx, span := tracer.Start(context.Background(), "LoadStack")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE stack = $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	AND operation != $2
	`

	rows, err := d.pool.Query(ctx, query, stackLabel, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	// Filter resources that belong to this stack
	var stackResources []*pkgmodel.Resource
	for _, resource := range allResources {
		if resource.Stack == stackLabel {
			stackResources = append(stackResources, resource)
		}
	}

	// If no resources found for this stack, return nil
	if len(stackResources) == 0 {
		return nil, nil
	}

	// Create a Forma object from the filtered resources
	forma := pkgmodel.FormaFromResources(stackResources)

	return forma, nil
}

func (d DatastorePostgres) LoadTarget(label string) (*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "LoadTarget")
	defer span.End()

	query := `
	SELECT version, namespace, config, discoverable
	FROM targets
	WHERE label = $1
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, label)

	var version int
	var namespace string
	var config json.RawMessage
	var discoverable bool
	if err := row.Scan(&version, &namespace, &config, &discoverable); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Target not found, return nil without error
		}
		return nil, err
	}

	return &pkgmodel.Target{
		Label:        label,
		Namespace:    namespace,
		Config:       config,
		Discoverable: discoverable,
		Version:      version,
	}, nil
}

func (d DatastorePostgres) LoadTargetsByLabels(targetNames []string) ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "LoadTargetsByLabels")
	defer span.End()

	if len(targetNames) == 0 {
		return []*pkgmodel.Target{}, nil
	}

	// Build placeholders for the query
	placeholders := make([]string, len(targetNames))
	args := make([]any, len(targetNames))
	for i, name := range targetNames {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
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
	)
	`, strings.Join(placeholders, ","))

	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var discoverable bool
		if err := rows.Scan(&label, &version, &namespace, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "LoadDiscoverableTargets")
	defer span.End()

	// Get latest version per label where discoverable = true, deduplicated by config using DISTINCT ON
	query := `
	WITH latest_targets AS (
		SELECT label, version, namespace, config, discoverable
		FROM targets t1
		WHERE discoverable = TRUE
		AND NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)
	)
	SELECT DISTINCT ON (config) label, version, namespace, config, discoverable
	FROM latest_targets
	ORDER BY config, version DESC`

	rows, err := d.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, ns string
		var version int
		var config json.RawMessage
		var discoverable bool
		if err := rows.Scan(&label, &version, &ns, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    ns,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) QueryTargets(query *TargetQuery) ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "QueryTargets")
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

	queryStr = extendPostgresQueryString(queryStr, query.Label, " AND label %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Namespace, " AND namespace %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Discoverable, " AND discoverable %s $%d", &args)
	queryStr += " ORDER BY label"

	rows, err := d.pool.Query(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []*pkgmodel.Target
	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var discoverable bool
		if err := rows.Scan(&label, &version, &namespace, &config, &discoverable); err != nil {
			return nil, err
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "QueryResources")
	defer span.End()

	queryStr := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	AND r1.operation != $1
	`
	args := []any{resource_update.OperationDelete}

	queryStr = extendPostgresQueryString(queryStr, query.NativeID, " AND native_id %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Stack, " AND stack %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Type, " AND LOWER(type) %s LOWER($%d)", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Label, " AND label %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Target, " AND target %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Managed, " AND managed %s $%d", &args)

	queryStr += " ORDER BY type, label"

	rows, err := d.pool.Query(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	return resources, rows.Err()
}

func (d DatastorePostgres) Stats() (*stats.Stats, error) {
	ctx, span := tracer.Start(context.Background(), "Stats")
	defer span.End()

	res := stats.Stats{}

	// Count distinct clients
	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", CommandsTable)
	row := d.pool.QueryRow(ctx, clientsQuery)
	if err := row.Scan(&res.Clients); err != nil {
		return nil, err
	}

	// Count commands by type
	commandsQuery := fmt.Sprintf(`
	SELECT command, COUNT(*)
	FROM %s
	WHERE command != $1
	GROUP BY command
	`, CommandsTable)
	rows, err := d.pool.Query(ctx, commandsQuery, pkgmodel.CommandSync)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res.Commands = make(map[string]int)
	for rows.Next() {
		var command string
		var count int
		if err := rows.Scan(&command, &count); err != nil {
			return nil, err
		}
		res.Commands[command] = count
	}

	// Count states by type
	statusQuery := fmt.Sprintf(`
	SELECT state, COUNT(*)
	FROM %s
	WHERE command != $1
	GROUP BY state
	`, CommandsTable)
	rows, err = d.pool.Query(ctx, statusQuery, pkgmodel.CommandSync)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res.States = make(map[string]int)
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		res.States[state] = count
	}

	// Count distinct stacks
	stacksQuery := fmt.Sprintf(`
	SELECT COUNT(DISTINCT stack)
	FROM resources r1
	WHERE stack IS NOT NULL
	AND stack != '%s'
	AND operation != $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	`, constants.UnmanagedStack)
	row = d.pool.QueryRow(ctx, stacksQuery, resource_update.OperationDelete)
	if err := row.Scan(&res.Stacks); err != nil {
		return nil, err
	}

	// Count managed resources by namespace
	res.ManagedResources = make(map[string]int)
	managedResourcesQuery := fmt.Sprintf(`
	SELECT SPLIT_PART(type, '::', 1) as namespace, COUNT(*)
	FROM resources r1
	WHERE stack IS NOT NULL
	AND stack != '%s'
	AND operation != $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	GROUP BY namespace
	`, constants.UnmanagedStack)
	rows, err = d.pool.Query(ctx, managedResourcesQuery, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var namespace string
		var count int
		if err := rows.Scan(&namespace, &count); err != nil {
			return nil, err
		}
		res.ManagedResources[namespace] = count
	}

	// Count unmanaged resources by namespace
	res.UnmanagedResources = make(map[string]int)
	unmanagedResourcesQuery := fmt.Sprintf(`
	SELECT SPLIT_PART(type, '::', 1) as namespace, COUNT(*)
	FROM resources r1
	WHERE stack = '%s'
	AND operation != $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	GROUP BY namespace
	`, constants.UnmanagedStack)
	rows, err = d.pool.Query(ctx, unmanagedResourcesQuery, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var namespace string
		var count int
		if err := rows.Scan(&namespace, &count); err != nil {
			return nil, err
		}
		res.UnmanagedResources[namespace] = count
	}

	// Count targets by namespace
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
	rows, err = d.pool.Query(ctx, targetsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var namespace string
		var count int
		if err := rows.Scan(&namespace, &count); err != nil {
			return nil, err
		}
		res.Targets[namespace] = count
	}

	// Count resource types
	res.ResourceTypes = make(map[string]int)
	resourceTypesQuery := `
	SELECT type, COUNT(*)
	FROM resources r1
	WHERE operation != $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	GROUP BY type
	`
	rows, err = d.pool.Query(ctx, resourceTypesQuery, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var resourceType string
		var count int
		if err := rows.Scan(&resourceType, &count); err != nil {
			return nil, err
		}
		res.ResourceTypes[resourceType] = count
	}

	// Count resource errors by resource type
	res.ResourceErrors = make(map[string]int)
	errorQuery := `
	SELECT resource::jsonb->>'Type' as resource_type, COUNT(*)
	FROM resource_updates
	WHERE state = $1
	AND resource IS NOT NULL
	GROUP BY resource_type
	`
	rows, err = d.pool.Query(ctx, errorQuery, types.ResourceUpdateStateFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var resourceType string
		var count int
		if err := rows.Scan(&resourceType, &count); err != nil {
			return nil, err
		}
		if resourceType != "" {
			res.ResourceErrors[resourceType] = count
		}
	}

	return &res, nil
}

func (d DatastorePostgres) StoreResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "StoreResource")
	defer span.End()

	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}

	return d.storeResource(ctx, resource, jsonData, commandID, string(resource_update.OperationUpdate))
}

func (d DatastorePostgres) storeResource(ctx context.Context, resource *pkgmodel.Resource, data []byte, commandID string, operation string) (string, error) {
	// Track if KSUID was provided (from GenerateResourceUpdates) vs generated here.
	// If a KSUID was already assigned, other resources in the same command may reference it,
	// so we must not overwrite it with a discovered KSUID.
	originalKsuidProvided := resource.Ksuid != ""
	if resource.Ksuid == "" {
		resource.Ksuid = metautil.NewID()
	}

	// Check if this resource already exists by native_id and type
	query := `SELECT ksuid, data, uri, version, managed FROM resources WHERE native_id = $1 AND type = $2 ORDER BY version DESC LIMIT 1`
	row := d.pool.QueryRow(ctx, query, resource.NativeID, resource.Type)

	var ksuid string
	var existingData string
	var uri string
	var version string
	var managed bool
	err := row.Scan(&ksuid, &existingData, &uri, &version, &managed)

	if errors.Is(err, pgx.ErrNoRows) {
		newVersion := mksuid.New().String()
		query = `
		INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`
		_, err = d.pool.Exec(
			ctx,
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
			resource.Managed,
			resource.Ksuid,
		)
		if err != nil {
			slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
			return "", fmt.Errorf("failed to store resource: %w", err)
		}

		return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
	} else if err != nil {
		return "", fmt.Errorf("failed to retrieve resources: %w", err)
	}

	// It can happen in rare cases that the resource existed on the unmanaged stack. This happens when a resource is discovered
	// before the create operation completes. In this case, we preserve the discovered KSUID to maintain referential integrity
	// for any resources that may have already created references to it.
	//
	// However, we only adopt the discovered KSUID if the incoming resource didn't already have one assigned.
	// If GenerateResourceUpdates already assigned a KSUID, other resources in the same command reference it,
	// so overwriting it would break those references.
	if !managed && operation != string(resource_update.OperationDelete) && !originalKsuidProvided {
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

	var existingResource pkgmodel.Resource
	if err = json.Unmarshal([]byte(existingData), &existingResource); err != nil {
		return "", fmt.Errorf("failed to unmarshal existing resource: %w", err)
	}

	readWriteEqual, readOnlyEqual := resourcesAreEqual(resource, &existingResource)

	if operation == string(resource_update.OperationDelete) {
		// For delete operations, check if the latest version is already a delete
		var latestOperation string
		query = `SELECT operation FROM resources WHERE uri = $1 ORDER BY version DESC LIMIT 1`
		row = d.pool.QueryRow(ctx, query, resource.URI())
		err = row.Scan(&latestOperation)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", nil // No rows found, return nil without error
			}
			return "", fmt.Errorf("failed to retrieve latest operation: %w", err)
		}

		if latestOperation == string(resource_update.OperationDelete) {
			// Already deleted, return existing version ID
			return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
		}
	} else {
		// For non-delete operations, compare resources
		if readWriteEqual && readOnlyEqual {
			// Resource data is identical. Only return early if KSUIDs match.
			// If a new KSUID was assigned (e.g., after destroy with deterministic native_id like Azure),
			// we need to insert a row with the new KSUID for references to resolve.
			if resource.Ksuid == ksuid {
				return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
			}
			// KSUIDs differ - fall through to insert with the new KSUID
		}
	}

	var newVersion string
	if readWriteEqual && !readOnlyEqual {
		newVersion = version
	} else {
		newVersion = mksuid.New().String()
	}

	query = `
	INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	ON CONFLICT (uri, version) DO UPDATE SET
	command_id = EXCLUDED.command_id,
	operation = EXCLUDED.operation,
	native_id = EXCLUDED.native_id,
	stack = EXCLUDED.stack,
	type = EXCLUDED.type,
	label = EXCLUDED.label,
	target = EXCLUDED.target,
	data = EXCLUDED.data,
	managed = EXCLUDED.managed,
	ksuid = EXCLUDED.ksuid
	`
	_, err = d.pool.Exec(
		ctx,
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
		resource.Managed,
		resource.Ksuid,
	)
	if err != nil {
		slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
		return "", fmt.Errorf("failed to store resource: %w", err)
	}

	return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
}

func (d DatastorePostgres) StoreStack(stack *pkgmodel.Forma, commandID string) (string, error) {
	_, span := tracer.Start(context.Background(), "StoreStack")
	defer span.End()

	var lastVersionID string
	for _, resource := range stack.Resources {
		versionID, err := d.StoreResource(&resource, commandID)
		if err != nil {
			slog.Error("failed to store resource in stack", "error", err, "resourceURI", resource.URI())
			return "", err
		}
		lastVersionID = versionID
	}
	return lastVersionID, nil
}

func (d DatastorePostgres) CreateTarget(target *pkgmodel.Target) (string, error) {
	ctx, span := tracer.Start(context.Background(), "CreateTarget")
	defer span.End()

	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	query := `
	INSERT INTO targets (label, version, namespace, config, discoverable)
	VALUES ($1, 1, $2, $3, $4)
	`
	_, err = d.pool.Exec(ctx, query, target.Label, target.Namespace, cfg, target.Discoverable)
	if err != nil {
		slog.Error("failed to create target", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d DatastorePostgres) UpdateTarget(target *pkgmodel.Target) (string, error) {
	ctx, span := tracer.Start(context.Background(), "UpdateTarget")
	defer span.End()

	query := `SELECT MAX(version) FROM targets WHERE label = $1`
	row := d.pool.QueryRow(ctx, query, target.Label)

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

	insertQuery := `INSERT INTO targets (label, version, namespace, config, discoverable) VALUES ($1, $2, $3, $4, $5)`
	_, err = d.pool.Exec(ctx, insertQuery, target.Label, newVersion, target.Namespace, cfg, target.Discoverable)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

// BulkStoreResourceUpdates stores multiple ResourceUpdates in a single transaction
// This is the key performance optimization: insert all updates in one transaction
func (d DatastorePostgres) BulkStoreResourceUpdates(commandID string, updates []resource_update.ResourceUpdate) error {
	ctx, span := tracer.Start(context.Background(), "BulkStoreResourceUpdates")
	defer span.End()

	if len(updates) == 0 {
		return nil
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

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

		_, err = tx.Exec(ctx, `
			INSERT INTO resource_updates (
				command_id, ksuid, operation, state, start_ts, modified_ts,
				retries, remaining, version, stack_label, group_id, source,
				resource, resource_target, existing_resource, existing_target,
				progress_result, most_recent_progress,
				remaining_resolvables, reference_labels, previous_properties
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
			ON CONFLICT (command_id, ksuid, operation) DO UPDATE SET
				state = EXCLUDED.state,
				start_ts = EXCLUDED.start_ts,
				modified_ts = EXCLUDED.modified_ts,
				retries = EXCLUDED.retries,
				remaining = EXCLUDED.remaining,
				version = EXCLUDED.version,
				stack_label = EXCLUDED.stack_label,
				group_id = EXCLUDED.group_id,
				source = EXCLUDED.source,
				resource = EXCLUDED.resource,
				resource_target = EXCLUDED.resource_target,
				existing_resource = EXCLUDED.existing_resource,
				existing_target = EXCLUDED.existing_target,
				progress_result = EXCLUDED.progress_result,
				most_recent_progress = EXCLUDED.most_recent_progress,
				remaining_resolvables = EXCLUDED.remaining_resolvables,
				reference_labels = EXCLUDED.reference_labels,
				previous_properties = EXCLUDED.previous_properties
		`,
			commandID,
			ru.DesiredState.Ksuid,
			string(ru.Operation),
			string(ru.State),
			ru.StartTs.UTC(),
			ru.ModifiedTs.UTC(),
			ru.Retries,
			ru.Remaining,
			ru.Version,
			ru.StackLabel,
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

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// LoadResourceUpdates loads all ResourceUpdates for a given command
// ORDER BY ksuid ensures deterministic ordering (KSUIDs are time-sortable)
func (d DatastorePostgres) LoadResourceUpdates(commandID string) ([]resource_update.ResourceUpdate, error) {
	ctx, span := tracer.Start(context.Background(), "LoadResourceUpdates")
	defer span.End()

	query := `
		SELECT ksuid, operation, state, start_ts, modified_ts,
			retries, remaining, version, stack_label, group_id, source,
			resource, resource_target, existing_resource, existing_target,
			progress_result, most_recent_progress,
			remaining_resolvables, reference_labels, previous_properties
		FROM resource_updates
		WHERE command_id = $1
		ORDER BY ksuid ASC
	`

	rows, err := d.pool.Query(ctx, query, commandID)
	if err != nil {
		return nil, fmt.Errorf("failed to query resource updates: %w", err)
	}
	defer rows.Close()

	var updates []resource_update.ResourceUpdate
	for rows.Next() {
		var ru resource_update.ResourceUpdate
		var ksuid, operation, state string
		var startTs, modifiedTs *time.Time
		var stackLabel, groupID, source, version *string
		var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
		var progressResultJSON, mostRecentProgressJSON []byte
		var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte

		err := rows.Scan(
			&ksuid,
			&operation,
			&state,
			&startTs,
			&modifiedTs,
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
		if startTs != nil {
			ru.StartTs = *startTs
		}
		if modifiedTs != nil {
			ru.ModifiedTs = *modifiedTs
		}
		if version != nil {
			ru.Version = *version
		}
		if stackLabel != nil {
			ru.StackLabel = *stackLabel
		}
		if groupID != nil {
			ru.GroupID = *groupID
		}
		if source != nil {
			ru.Source = resource_update.FormaCommandSource(*source)
		}

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
func (d DatastorePostgres) UpdateResourceUpdateState(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	ctx, span := tracer.Start(context.Background(), "UpdateResourceUpdateState")
	defer span.End()

	query := `
		UPDATE resource_updates
		SET state = $1, modified_ts = $2
		WHERE command_id = $3 AND ksuid = $4 AND operation = $5
	`

	result, err := d.pool.Exec(ctx, query, string(state), modifiedTs.UTC(), commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update state: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}

	return nil
}

// UpdateResourceUpdateProgress updates a ResourceUpdate with progress information
func (d DatastorePostgres) UpdateResourceUpdateProgress(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time, progress plugin.TrackedProgress) error {
	ctx, span := tracer.Start(context.Background(), "UpdateResourceUpdateProgress")
	defer span.End()

	// First, load existing progress results to append to
	var existingProgressJSON []byte
	query := `SELECT progress_result FROM resource_updates WHERE command_id = $1 AND ksuid = $2 AND operation = $3`
	err := d.pool.QueryRow(ctx, query, commandID, ksuid, string(operation)).Scan(&existingProgressJSON)
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
		SET state = $1, modified_ts = $2, progress_result = $3, most_recent_progress = $4
		WHERE command_id = $5 AND ksuid = $6 AND operation = $7
	`

	result, err := d.pool.Exec(ctx, updateQuery, string(state), modifiedTs.UTC(), progressJSON, mostRecentJSON, commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update progress: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}

	return nil
}

// BatchUpdateResourceUpdateState updates multiple ResourceUpdates to the same state
// Used for bulk operations like marking dependent resources as failed
func (d DatastorePostgres) BatchUpdateResourceUpdateState(commandID string, refs []ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	ctx, span := tracer.Start(context.Background(), "BatchUpdateResourceUpdateState")
	defer span.End()

	if len(refs) == 0 {
		return nil
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	for _, ref := range refs {
		_, err = tx.Exec(ctx, `
			UPDATE resource_updates
			SET state = $1, modified_ts = $2
			WHERE command_id = $3 AND ksuid = $4 AND operation = $5
		`, string(state), modifiedTs.UTC(), commandID, ref.KSUID, string(ref.Operation))
		if err != nil {
			return fmt.Errorf("failed to update resource update: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// UpdateFormaCommandProgress updates only the command-level metadata (state, modified_ts)
// without re-writing all ResourceUpdates. This is a performance optimization for
// progress updates where the ResourceUpdate is already updated via UpdateResourceUpdateProgress.
func (d DatastorePostgres) UpdateFormaCommandProgress(commandID string, state forma_command.CommandState, modifiedTs time.Time) error {
	ctx, span := tracer.Start(context.Background(), "UpdateFormaCommandProgress")
	defer span.End()

	query := `UPDATE forma_commands SET state = $1, modified_ts = $2 WHERE command_id = $3`
	result, err := d.pool.Exec(ctx, query, string(state), modifiedTs.UTC(), commandID)
	if err != nil {
		return fmt.Errorf("failed to update forma command meta: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("forma command not found: %s", commandID)
	}

	return nil
}

func (d DatastorePostgres) Close() {
	d.pool.Close()
}

// This can be only used in tests or in setups where we have access to admin (non-production)
func (d DatastorePostgres) CleanUp() error {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		d.cfg.Postgres.User, d.cfg.Postgres.Password, d.cfg.Postgres.Host, d.cfg.Postgres.Port, d.cfg.Postgres.Database)

	conn, err := pgx.Connect(d.ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to admin database: %w", err)
	}

	defer func() {
		if err := conn.Close(d.ctx); err != nil {
			slog.Error("failed to close connection", "error", err)
		}
	}()

	_, err = conn.Exec(d.ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgx.Identifier{d.cfg.Postgres.Database}.Sanitize()))
	if err != nil {
		return fmt.Errorf("failed to delete database: %w", err)
	}

	return nil
}
