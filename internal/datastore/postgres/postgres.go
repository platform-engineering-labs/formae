// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	"github.com/platform-engineering-labs/formae/internal/datastore"
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

	// Register Postgres factory with the datastore extension registry
	datastore.DefaultRegistry.Register("postgres", func(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
		return NewDatastorePostgres(ctx, cfg, agentID)
	})
}

type DatastorePostgres struct {
	pool    *pgxpool.Pool
	agentID string
	cfg     *pkgmodel.DatastoreConfig
	ctx     context.Context
}

// BuildConnStr constructs a PostgreSQL connection string from config fields.
// Unix socket hosts (starting with /) use DSN key-value format to avoid
// URI parsing issues with colons in paths like /cloudsql/project:region:instance.
//
// User and password are percent-encoded per RFC 3986 userinfo rules so that
// passwords containing reserved characters (e.g. RDS-managed master passwords
// with `!`, `<`, `(`, `:`, `#`, `*`) round-trip cleanly through pgx's URL parser.
func BuildConnStr(host string, port int, user, password, database string) string {
	if strings.HasPrefix(host, "/") {
		return fmt.Sprintf("host=%s user=%s password=%s dbname=%s", host, user, password, database)
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/" + database,
	}
	return u.String()
}

// This can be only used in tests or in setups where we have access to admin (non-production)
func ensureDatabaseExists(ctx context.Context, cfg *pkgmodel.DatastoreConfig) error {
	connStr := BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, "postgres")

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
func NewDatastorePostgresEnsureDatabase(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
	err := ensureDatabaseExists(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return NewDatastorePostgres(ctx, cfg, agentID)
}

func NewDatastorePostgres(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
	connStr := BuildConnStr(cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.User, cfg.Postgres.Password, cfg.Postgres.Database)

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

	if err = datastore.RunMigrations(migrationDB, "postgres"); err != nil {
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

	stackUpdatesJSON, err := json.Marshal(fa.StackUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal stack updates: %w", err)
	}

	policyUpdatesJSON, err := json.Marshal(fa.PolicyUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal policy updates: %w", err)
	}

	// We no longer store the forma JSON - Description and Config are stored as normalized columns
	query := fmt.Sprintf(`
	INSERT INTO %s (command_id, timestamp, command, state, agent_version, client_id, agent_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, policy_updates, modified_ts)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
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
	stack_updates = EXCLUDED.stack_updates,
	policy_updates = EXCLUDED.policy_updates,
	modified_ts = EXCLUDED.modified_ts
	`, datastore.CommandsTable)

	_, err = d.pool.Exec(ctx, query, commandID, fa.StartTs.UTC(), fa.Command, fa.State, formae.Version, fa.ClientID, d.agentID,
		fa.Description.Text, fa.Description.Confirm, fa.Config.Mode, fa.Config.Force, fa.Config.Simulate,
		targetUpdatesJSON, stackUpdatesJSON, policyUpdatesJSON, fa.ModifiedTs.UTC())
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
	fc.target_updates, fc.stack_updates, fc.policy_updates, fc.modified_ts,
	ru.ksuid, ru.operation, ru.state, ru.start_ts, ru.modified_ts,
	ru.retries, ru.remaining, ru.version, ru.stack_label, ru.group_id, ru.source,
	ru.resource, ru.resource_target, ru.existing_resource, ru.existing_target,
	ru.progress_result, ru.most_recent_progress,
	ru.remaining_resolvables, ru.reference_labels, ru.previous_properties,
	ru.is_cascade, ru.cascade_source
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
	var stackUpdatesJSON []byte
	var policyUpdatesJSON []byte
	var fcModifiedTs *time.Time

	// ResourceUpdate fields (all nullable due to LEFT JOIN)
	var ruKsuid, ruOperation, ruState *string
	var ruStartTs, ruModifiedTs *time.Time
	var ruRetries, ruRemaining *uint16
	var ruVersion, ruStackLabel, ruGroupID, ruSource *string
	var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
	var progressResultJSON, mostRecentProgressJSON []byte
	var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte
	var ruIsCascade *bool
	var ruCascadeSource *string

	err := rows.Scan(
		// FormaCommand columns
		&commandID, &fcTimestamp, &fcCommand, &fcState, &fcClientID,
		&descriptionText, &descriptionConfirm, &configMode, &configForce, &configSimulate,
		&targetUpdatesJSON, &stackUpdatesJSON, &policyUpdatesJSON, &fcModifiedTs,
		// ResourceUpdate columns
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

	if ruIsCascade != nil {
		ru.IsCascade = *ruIsCascade
	}
	if ruCascadeSource != nil {
		ru.CascadeSource = *ruCascadeSource
	}

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

	query := fmt.Sprintf("DELETE FROM %s WHERE command_id = $1", datastore.CommandsTable)
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

// extendPostgresQueryString appends a WHERE clause to queryStr for the given
// query item.
//
// sqlPart is a template that takes two %-verbs — the comparison operator and
// the positional parameter index — e.g. " AND command_id %s $%d".
//
// For multi-valued items the inner clause is replicated once per value and
// joined with OR (or AND when the constraint is Excluded). String values may
// carry leading or trailing `*` for wildcard matching, which becomes a SQL
// LIKE pattern.
func extendPostgresQueryString[T any](queryStr string, queryItem *datastore.QueryItem[T], sqlPart string, args *[]any) string {
	if queryItem == nil {
		return queryStr
	}

	values := allQueryItemValues(queryItem)
	if len(values) == 0 {
		return queryStr
	}

	isExcluded := queryItem.Constraint == datastore.Excluded

	if len(values) == 1 {
		op, operand, _ := pgOpAndOperand(values[0], isExcluded)
		queryStr += fmt.Sprintf(sqlPart, op, len(*args)+1)
		*args = append(*args, operand)
		return queryStr
	}

	innerTemplate := strings.TrimPrefix(sqlPart, " AND ")
	clauses := make([]string, 0, len(values))
	for _, v := range values {
		op, operand, _ := pgOpAndOperand(v, isExcluded)
		clauses = append(clauses, fmt.Sprintf(innerTemplate, op, len(*args)+1))
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

// pgOpAndOperand resolves the operator and bound value for one term,
// accounting for exclusion and `*` wildcards on strings. Any `*` in the
// value (anywhere) flips the operator to LIKE and translates to `%`.
func pgOpAndOperand(v any, isExcluded bool) (op string, operand any, isLike bool) {
	s, isString := v.(string)
	if !isString {
		if b, ok := v.(bool); ok {
			if b {
				return pgEqOp(isExcluded), "1", false
			}
			return pgEqOp(isExcluded), "0", false
		}
		return pgEqOp(isExcluded), fmt.Sprintf("%v", v), false
	}

	if !strings.Contains(s, "*") {
		return pgEqOp(isExcluded), s, false
	}

	likeOp := "LIKE"
	if isExcluded {
		likeOp = "NOT LIKE"
	}
	return likeOp, pgLikePattern(s), true
}

func pgEqOp(isExcluded bool) string {
	if isExcluded {
		return "!="
	}
	return "="
}

// pgLikePattern translates every `*` in s into a SQL LIKE `%`. Literal `%`,
// `_`, and `\` are escaped so they match as themselves.
func pgLikePattern(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "%", "\\%")
	escaped = strings.ReplaceAll(escaped, "_", "\\_")
	return strings.ReplaceAll(escaped, "*", "%")
}

func (d DatastorePostgres) QueryFormaCommands(query *datastore.StatusQuery) ([]*forma_command.FormaCommand, error) {
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
		args = append(args, min(datastore.DefaultFormaCommandsQueryLimit, query.N))
	} else {
		subqueryStr += fmt.Sprintf(" LIMIT %d", datastore.DefaultFormaCommandsQueryLimit)
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
			ru.remaining_resolvables, ru.reference_labels, ru.previous_properties,
	ru.is_cascade, ru.cascade_source
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
	AND operation != $4 AND operation != 'reaped'
	ORDER BY version COLLATE "C" DESC
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
	AND r1.operation != $%d AND r1.operation != 'reaped'
	AND NOT EXISTS (
		SELECT 1 FROM resources r2
		WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
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

func (d DatastorePostgres) GetResourceModificationsSinceLastReconcile(stack string) ([]datastore.ResourceModification, error) {
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
			AND r2.version COLLATE "C" > r1.version COLLATE "C"
		)
		AND r1.operation != 'delete'
		AND r1.operation != 'reaped'
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

	modifications := make(map[datastore.ResourceModification]struct{})
	for rows.Next() {
		var resourceType, label, operation string
		if err := rows.Scan(&resourceType, &label, &operation); err != nil {
			return nil, err
		}
		modifications[datastore.ResourceModification{Stack: stack, Type: resourceType, Label: label, Operation: operation}] = struct{}{}
	}

	result := make([]datastore.ResourceModification, 0, len(modifications))
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
		       ROW_NUMBER() OVER (PARTITION BY ksuid ORDER BY managed DESC, version COLLATE "C" DESC) as rn
		FROM resources
		WHERE ksuid IN (%s)
		AND operation != $%d AND operation != 'reaped'
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

	return d.storeResource(ctx, resource, []byte("{}"), commandID, string(resource_update.OperationDelete), "")
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
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $1 AND operation != 'reaped'
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

// LoadReapedResources returns the current-version rows tombstoned with the
// 'reaped' marker, across all targets. See the Datastore interface for the
// contract.
func (d DatastorePostgres) LoadReapedResources() ([]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadReapedResources")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation = 'reaped'
	`
	rows, err := d.pool.Query(ctx, query)
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

func (d DatastorePostgres) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadAllResourcesByStack")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $1 AND operation != 'reaped'
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

	return stackResourcesMap, nil
}

func (d DatastorePostgres) LoadAllTargets() ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "LoadAllTargets")
	defer span.End()

	query := `
	SELECT label, version, namespace, config, config_schema, discoverable,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
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
		var configSchemaRaw []byte
		var discoverable bool
		var incarnationID, healthState string
		var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
		var unreachableAccumSeconds int64
		var lastErrorCode *string
		var reapKind string
		var reapMaxUnreachableSeconds int64
		if err := rows.Scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable,
			&incarnationID, &healthState, &lastSeenAt, &observedAt,
			&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
			&reapKind, &reapMaxUnreachableSeconds); err != nil {
			return nil, err
		}

		var configSchema pkgmodel.ConfigSchema
		if len(configSchemaRaw) > 0 {
			if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
			}
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
			Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx, span := tracer.Start(context.Background(), "LoadIncompleteFormaCommands")
	defer span.End()

	query := formaCommandWithResourceUpdatesQueryBasePostgres +
		" WHERE fc.command != 'sync' AND fc.state IN ($1, $2)" +
		resourceUpdateOrderByPostgres

	rows, err := d.pool.Query(ctx, query, forma_command.CommandStateNotStarted, forma_command.CommandStateInProgress)
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
	AND operation != $2 AND operation != 'reaped'
	ORDER BY version COLLATE "C" DESC
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
	AND operation != $2 AND operation != 'reaped'
	ORDER BY version COLLATE "C" DESC
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

// FindResourcesDependingOn finds resources that reference the given KSUID via $ref in their properties.
// This is essential for referential integrity — without it we risk leaving orphaned resources in an
// inconsistent state. Currently this requires a full table scan (LIKE on the data column) which will
// be slow for users with large resource counts.
// TODO: make the dependency graph discoverable from the schema so we can query edges directly.
func (d DatastorePostgres) FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "FindResourcesDependingOn")
	defer span.End()

	// Search for resources that contain a $ref to this KSUID in their properties
	// Use regex to handle Postgres JSONB text formatting which adds spaces after colons
	pattern := fmt.Sprintf(`"\$ref"\s*:\s*"formae://%s#`, ksuid)

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE data::text ~ $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $2 AND operation != 'reaped'
	`

	rows, err := d.pool.Query(ctx, query, pattern, resource_update.OperationDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (d DatastorePostgres) FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "FindResourcesDependingOnMany")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string][]*pkgmodel.Resource), nil
	}

	// Build OR conditions for each KSUID pattern with numbered placeholders
	// Use regex to handle Postgres JSONB text formatting which adds spaces after colons
	var conditions []string
	var args []any
	for i, ksuid := range ksuids {
		pattern := fmt.Sprintf(`"\$ref"\s*:\s*"formae://%s#`, ksuid)
		conditions = append(conditions, fmt.Sprintf("data::text ~ $%d", i+1))
		args = append(args, pattern)
	}
	args = append(args, resource_update.OperationDelete)
	deleteArgNum := len(ksuids) + 1

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $%d AND operation != 'reaped'
	`, strings.Join(conditions, " OR "), deleteArgNum)

	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Build a map of KSUID -> resources that depend on it
	result := make(map[string][]*pkgmodel.Resource)
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

		// Find which of the input KSUIDs this resource depends on
		for _, ksuid := range ksuids {
			pattern := fmt.Sprintf("\"$ref\":\"formae://%s#", ksuid)
			if strings.Contains(jsonData, pattern) {
				result[ksuid] = append(result[ksuid], &resource)
			}
		}
	}

	return result, nil
}

func (d DatastorePostgres) FindTargetsDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "FindTargetsDependingOnMany")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string][]*pkgmodel.Target), nil
	}

	// Build OR conditions for each KSUID pattern with numbered placeholders
	// Use regex to handle Postgres JSONB text formatting which adds spaces after colons
	var conditions []string
	var args []any
	for i, ksuid := range ksuids {
		pattern := fmt.Sprintf(`"\$ref"\s*:\s*"formae://%s#`, ksuid)
		conditions = append(conditions, fmt.Sprintf("config::text ~ $%d", i+1))
		args = append(args, pattern)
	}

	query := fmt.Sprintf(`
	SELECT label, version, namespace, config, config_schema, discoverable,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
	FROM targets t1
	WHERE (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`, strings.Join(conditions, " OR "))

	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Build a map of KSUID -> targets that depend on it
	result := make(map[string][]*pkgmodel.Target)
	for rows.Next() {
		var label, namespace string
		var version int
		var config json.RawMessage
		var configSchemaRaw []byte
		var discoverable bool
		var incarnationID, healthState string
		var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
		var unreachableAccumSeconds int64
		var lastErrorCode *string
		var reapKind string
		var reapMaxUnreachableSeconds int64
		if err := rows.Scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable,
			&incarnationID, &healthState, &lastSeenAt, &observedAt,
			&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
			&reapKind, &reapMaxUnreachableSeconds); err != nil {
			return nil, err
		}

		var configSchema pkgmodel.ConfigSchema
		if len(configSchemaRaw) > 0 {
			if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
			}
		}

		target := &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
			Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
		}

		// Find which of the input KSUIDs this target depends on
		// jsonb::text output has spaces after colons, so check both forms.
		configStr := string(config)
		for _, ksuid := range ksuids {
			withSpace := fmt.Sprintf("\"$ref\": \"formae://%s#", ksuid)
			withoutSpace := fmt.Sprintf("\"$ref\":\"formae://%s#", ksuid)
			if strings.Contains(configStr, withSpace) || strings.Contains(configStr, withoutSpace) {
				result[ksuid] = append(result[ksuid], target)
			}
		}
	}

	return result, nil
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
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND r1.operation != $3 AND r1.operation != 'reaped'
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

func (d DatastorePostgres) LoadResourcesByStack(stackLabel string) ([]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "LoadResourcesByStack")
	defer span.End()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE stack = $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != $2 AND operation != 'reaped'
	`

	rows, err := d.pool.Query(ctx, query, stackLabel, resource_update.OperationDelete)
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

	return resources, nil
}

// Stack metadata operations

func (d DatastorePostgres) CreateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "CreateStack")
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

	query := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = d.pool.Exec(ctx, query, id, version, commandID, "create", stack.Label, stack.Description)
	if err != nil {
		slog.Error("Failed to create stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d DatastorePostgres) UpdateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "UpdateStack")
	defer span.End()

	// Get the existing stack to find its id (most recent version of any operation)
	query := `
		SELECT id, operation FROM stacks
		WHERE label = $1
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, stack.Label)

	var id, operation string
	if err := row.Scan(&id, &operation); err != nil {
		if err == pgx.ErrNoRows {
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
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := d.pool.Exec(ctx, insertQuery, id, version, commandID, "update", stack.Label, stack.Description)
	if err != nil {
		slog.Error("Failed to update stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d DatastorePostgres) DeleteStack(label string, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "DeleteStack")
	defer span.End()

	// Get the existing stack to find its id (most recent version of any operation)
	query := `
		SELECT id, operation FROM stacks
		WHERE label = $1
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, label)

	var id, operation string
	if err := row.Scan(&id, &operation); err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("stack not found: %s", label)
		}
		return "", err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", label)
	}

	// Cascade delete: delete all inline policies associated with this stack
	if err := d.DeletePoliciesForStack(id, commandID); err != nil {
		slog.Warn("Failed to delete policies for stack", "error", err, "stackID", id, "label", label)
		// Continue with stack deletion even if policy deletion fails
	}

	// Insert tombstone version
	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES ($1, $2, $3, $4, $5, $6)`
	_, execErr := d.pool.Exec(ctx, insertQuery, id, version, commandID, "delete", label, "")
	if execErr != nil {
		slog.Error("Failed to delete stack", "error", execErr, "label", label)
		return "", execErr
	}

	return version, nil
}

func (d DatastorePostgres) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	ctx, span := tracer.Start(context.Background(), "GetStackByLabel")
	defer span.End()

	// Get the latest version of the stack, return nil if deleted
	// We need to check if the MOST RECENT version is a delete operation
	query := `
		SELECT id, description, operation FROM stacks
		WHERE label = $1
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, label)

	var id, description, operation string
	if err := row.Scan(&id, &description, &operation); err != nil {
		if err == pgx.ErrNoRows {
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
	policies, err := d.loadPoliciesForStackAsJSON(ctx, id)
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
func (d DatastorePostgres) loadPoliciesForStackAsJSON(ctx context.Context, stackID string) ([]json.RawMessage, error) {
	var policies []json.RawMessage

	// 1. Load inline policies (stack_id set directly on the policy)
	inlineQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE stack_id = $1
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	rows, err := d.pool.Query(ctx, inlineQuery, stackID)
	if err != nil {
		return nil, fmt.Errorf("failed to query inline policies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var label, policyType, policyData string
		if err := rows.Scan(&label, &policyType, &policyData); err != nil {
			continue
		}
		// Reconstruct full policy JSON by merging stored data with type and label
		fullPolicyJSON, err := reconstructPolicyJSONPg(label, policyType, policyData)
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
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT p.label FROM stack_policies sp
		JOIN latest_policies p ON p.id = sp.policy_id
		WHERE sp.stack_id = $1
		AND p.rn = 1 AND p.operation != 'delete'
	`
	rows2, err := d.pool.Query(ctx, standaloneQuery, stackID)
	if err != nil {
		return policies, fmt.Errorf("failed to query standalone policies: %w", err)
	}
	defer rows2.Close()

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

// reconstructPolicyJSONPg merges policy_data with type and label to create full policy JSON
func reconstructPolicyJSONPg(label, policyType, policyData string) (json.RawMessage, error) {
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

func (d DatastorePostgres) CountResourcesInStack(label string) (int, error) {
	ctx, span := tracer.Start(context.Background(), "CountResourcesInStack")
	defer span.End()

	// Count only latest version of resources that haven't been deleted
	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE stack = $1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE "C" > r1.version COLLATE "C"
		)
		AND operation != $2 AND operation != 'reaped'
	`
	row := d.pool.QueryRow(ctx, query, label, resource_update.OperationDelete)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func (d DatastorePostgres) ListAllStacks() ([]*pkgmodel.Stack, error) {
	ctx, span := tracer.Start(context.Background(), "ListAllStackMetadata")
	defer span.End()

	// Get all stacks at their latest version that aren't deleted
	// Uses window function to reliably get the most recent version per stack id
	query := `
		SELECT id, label, description, valid_from FROM (
			SELECT id, label, description, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		) sub
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`
	rows, err := d.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (d DatastorePostgres) CreatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "CreatePolicy")
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
	case *pkgmodel.AutoReconcilePolicy:
		policyData, err = json.Marshal(map[string]any{
			"IntervalSeconds": p.IntervalSeconds,
		})
	default:
		return "", fmt.Errorf("unsupported policy type: %T", policy)
	}
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy data: %w", err)
	}

	query := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = d.pool.Exec(ctx, query, id, version, commandID, "create", policy.GetLabel(), policy.GetType(), policy.GetStackID(), string(policyData))
	if err != nil {
		slog.Error("Failed to create policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d DatastorePostgres) UpdatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "UpdatePolicy")
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
			WHERE label = $1 AND (stack_id IS NULL OR stack_id = '')
			ORDER BY version COLLATE "C" DESC
			LIMIT 1
		`
		err = d.pool.QueryRow(ctx, query, policy.GetLabel()).Scan(&id)
	} else {
		// Inline policy - exact match on stack_id
		query = `
			SELECT id FROM policies
			WHERE label = $1 AND stack_id = $2
			ORDER BY version COLLATE "C" DESC
			LIMIT 1
		`
		err = d.pool.QueryRow(ctx, query, policy.GetLabel(), policy.GetStackID()).Scan(&id)
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
	case *pkgmodel.AutoReconcilePolicy:
		policyData, err = json.Marshal(map[string]any{
			"IntervalSeconds": p.IntervalSeconds,
		})
	default:
		return "", fmt.Errorf("unsupported policy type: %T", policy)
	}
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy data: %w", err)
	}

	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	                VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = d.pool.Exec(ctx, insertQuery, id, version, commandID, "update", policy.GetLabel(), policy.GetType(), policy.GetStackID(), string(policyData))
	if err != nil {
		slog.Error("Failed to update policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d DatastorePostgres) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	ctx, span := tracer.Start(context.Background(), "GetPoliciesForStack")
	defer span.End()

	// Get the latest non-deleted version of each policy for the given stack
	// This includes both:
	// 1. Inline policies (stack_id set directly on the policy)
	// 2. Standalone policies (attached via stack_policies junction table)
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, stack_id, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
		),
		-- Inline policies: stack_id is set directly on the policy
		inline_policies AS (
			SELECT label, policy_type, policy_data, stack_id
			FROM latest_policies
			WHERE stack_id = $1 AND rn = 1 AND operation != 'delete'
		),
		-- Standalone policies: attached via stack_policies junction table
		standalone_policies AS (
			SELECT lp.label, lp.policy_type, lp.policy_data, lp.stack_id
			FROM latest_policies lp
			JOIN stack_policies sp ON sp.policy_id = lp.id
			WHERE sp.stack_id = $2 AND lp.rn = 1 AND lp.operation != 'delete'
			AND (lp.stack_id IS NULL OR lp.stack_id = '')
		)
		SELECT label, policy_type, policy_data, stack_id FROM inline_policies
		UNION
		SELECT label, policy_type, policy_data, stack_id FROM standalone_policies
	`

	rows, err := d.pool.Query(ctx, query, stackID, stackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []pkgmodel.Policy
	for rows.Next() {
		var label, policyType, policyDataStr string
		var policyStackID *string
		if err := rows.Scan(&label, &policyType, &policyDataStr, &policyStackID); err != nil {
			return nil, err
		}

		// For standalone policies, use the queried stackID for display purposes
		effectiveStackID := stackID
		if policyStackID != nil && *policyStackID != "" {
			effectiveStackID = *policyStackID
		}
		policy, err := deserializePolicyPostgres(label, policyType, policyDataStr, effectiveStackID)
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

func (d DatastorePostgres) GetStandalonePolicy(label string) (pkgmodel.Policy, error) {
	ctx, span := tracer.Start(context.Background(), "GetStandalonePolicy")
	defer span.End()

	// Get the latest non-deleted version of the standalone policy with this label
	// Check for both NULL and empty string for compatibility
	query := `
		WITH latest_policy AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = $1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'
	`

	var policyLabel, policyType, policyDataStr string
	err := d.pool.QueryRow(ctx, query, label).Scan(&policyLabel, &policyType, &policyDataStr)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return deserializePolicyPostgres(policyLabel, policyType, policyDataStr, "")
}

func (d DatastorePostgres) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) {
	ctx, span := tracer.Start(context.Background(), "ListAllStandalonePolicies")
	defer span.End()

	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`
	rows, err := d.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list standalone policies: %w", err)
	}
	defer rows.Close()

	var policies []pkgmodel.Policy
	for rows.Next() {
		var label, policyType, policyDataStr string
		if err := rows.Scan(&label, &policyType, &policyDataStr); err != nil {
			return nil, fmt.Errorf("failed to scan policy: %w", err)
		}
		policy, err := deserializePolicyPostgres(label, policyType, policyDataStr, "")
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

func (d DatastorePostgres) AttachPolicyToStack(stackID, policyLabel string) error {
	ctx, span := tracer.Start(context.Background(), "AttachPolicyToStack")
	defer span.End()

	// First, get the policy ID from the label
	// Check for both NULL and empty string for compatibility
	policyQuery := `
		WITH latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = $1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'
	`
	var policyID string
	err := d.pool.QueryRow(ctx, policyQuery, policyLabel).Scan(&policyID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("standalone policy not found: %s", policyLabel)
		}
		return fmt.Errorf("failed to get policy ID: %w", err)
	}

	// Insert into stack_policies (ignore if already exists)
	insertQuery := `INSERT INTO stack_policies (stack_id, policy_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	_, err = d.pool.Exec(ctx, insertQuery, stackID, policyID)
	if err != nil {
		return fmt.Errorf("failed to attach policy to stack: %w", err)
	}

	slog.Debug("Attached standalone policy to stack",
		"stackID", stackID,
		"policyLabel", policyLabel,
		"policyID", policyID)

	return nil
}

func (d DatastorePostgres) IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error) {
	ctx, span := tracer.Start(context.Background(), "IsPolicyAttachedToStack")
	defer span.End()

	// Check if the attachment exists in stack_policies by looking up stack and policy by label
	query := `
		WITH latest_stack AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
			WHERE label = $1
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = $2 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT 1 FROM stack_policies sp
		JOIN latest_stack s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		LIMIT 1
	`
	var exists int
	err := d.pool.QueryRow(ctx, query, stackLabel, policyLabel).Scan(&exists)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to check policy attachment: %w", err)
	}
	return true, nil
}

func (d DatastorePostgres) GetStacksReferencingPolicy(policyLabel string) ([]string, error) {
	ctx, span := tracer.Start(context.Background(), "GetStacksReferencingPolicy")
	defer span.End()

	// Get all stack labels that reference this standalone policy via the junction table
	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = $1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT s.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY s.label
	`
	rows, err := d.pool.Query(ctx, query, policyLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to get stacks referencing policy: %w", err)
	}
	defer rows.Close()

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

func (d DatastorePostgres) GetAttachedPolicyLabelsForStack(stackLabel string) ([]string, error) {
	ctx, span := tracer.Start(context.Background(), "GetAttachedPolicyLabelsForStack")
	defer span.End()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
			WHERE label = $1
		),
		latest_policies AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
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
	rows, err := d.pool.Query(ctx, query, stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to get attached policies for stack: %w", err)
	}
	defer rows.Close()

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

func (d DatastorePostgres) DetachPolicyFromStack(stackLabel, policyLabel string) error {
	ctx, span := tracer.Start(context.Background(), "DetachPolicyFromStack")
	defer span.End()

	query := `
		DELETE FROM stack_policies
		WHERE stack_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
				FROM stacks
				WHERE label = $1
			) sub WHERE rn = 1 AND operation != 'delete'
		)
		AND policy_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
				FROM policies
				WHERE label = $2 AND (stack_id IS NULL OR stack_id = '')
			) sub WHERE rn = 1 AND operation != 'delete'
		)
	`
	_, err := d.pool.Exec(ctx, query, stackLabel, policyLabel)
	if err != nil {
		return fmt.Errorf("failed to detach policy from stack: %w", err)
	}

	slog.Debug("Detached standalone policy from stack",
		"stackLabel", stackLabel,
		"policyLabel", policyLabel)

	return nil
}

func (d DatastorePostgres) DeletePolicy(policyLabel string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "DeletePolicy")
	defer span.End()

	// Get the current policy to get its ID and type
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = $1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	var id, policyType string
	err := d.pool.QueryRow(ctx, query, policyLabel).Scan(&id, &policyType)
	if err != nil {
		return "", fmt.Errorf("failed to get policy for deletion: %w", err)
	}

	// Insert tombstone version
	version := mksuid.New().String()
	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = d.pool.Exec(ctx, insertQuery, id, version, "", "delete", policyLabel, policyType, nil, "{}")
	if err != nil {
		return "", fmt.Errorf("failed to delete policy: %w", err)
	}

	slog.Debug("Deleted standalone policy", "label", policyLabel, "id", id)

	return version, nil
}

func (d DatastorePostgres) DeletePoliciesForStack(stackID string, commandID string) error {
	ctx, span := tracer.Start(context.Background(), "DeletePoliciesForStack")
	defer span.End()

	// First, remove any standalone policy attachments from the junction table
	// This doesn't delete the standalone policies themselves, just the association
	deleteAttachmentsQuery := `DELETE FROM stack_policies WHERE stack_id = $1`
	_, err := d.pool.Exec(ctx, deleteAttachmentsQuery, stackID)
	if err != nil {
		slog.Warn("Failed to delete policy attachments for stack", "error", err, "stackID", stackID)
	}

	// Now get and delete only inline policies (policies with stack_id set to this stack)
	// We query directly for inline policies rather than using GetPoliciesForStack
	// since that also returns standalone policies which shouldn't be deleted
	inlinePoliciesQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE stack_id = $1
		)
		SELECT id, label, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	rows, err := d.pool.Query(ctx, inlinePoliciesQuery, stackID)
	if err != nil {
		return fmt.Errorf("failed to get inline policies for stack: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, label, policyType string
		if err := rows.Scan(&id, &label, &policyType); err != nil {
			slog.Warn("Failed to scan policy for deletion", "error", err)
			continue
		}

		// Insert tombstone version for inline policy
		version := mksuid.New().String()
		insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
		_, err = d.pool.Exec(ctx, insertQuery, id, version, commandID, "delete", label, policyType, stackID, "{}")
		if err != nil {
			slog.Error("Failed to delete inline policy", "error", err, "label", label)
			continue
		}
		slog.Debug("Deleted inline policy as part of stack deletion", "label", label, "stackID", stackID)
	}

	return nil
}

// deserializePolicyPostgres creates a Policy from stored data (Postgres version)
func deserializePolicyPostgres(label, policyType, policyDataStr, stackID string) (pkgmodel.Policy, error) {
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
	case "auto-reconcile":
		var data struct {
			IntervalSeconds int64 `json:"IntervalSeconds"`
		}
		if err := json.Unmarshal([]byte(policyDataStr), &data); err != nil {
			return nil, fmt.Errorf("failed to unmarshal auto-reconcile policy data: %w", err)
		}
		return &pkgmodel.AutoReconcilePolicy{
			Type:            "auto-reconcile",
			Label:           label,
			IntervalSeconds: data.IntervalSeconds,
			StackID:         stackID,
		}, nil
	default:
		return nil, fmt.Errorf("unknown policy type: %s", policyType)
	}
}

func (d DatastorePostgres) GetExpiredStacks() ([]datastore.ExpiredStackInfo, error) {
	ctx, span := tracer.Start(context.Background(), "GetExpiredStacks")
	defer span.End()

	// Get stacks with TTL policies that have expired:
	// - Handles both inline policies (stack_id set) and standalone policies (via stack_policies junction)
	// - Calculate expiration as stack.valid_from + policy.ttl_seconds
	// - Exclude stacks with active forma commands
	// - Only consider latest non-deleted versions of both stacks and policies
	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
		),
		-- Inline policies: stack_id is set directly on the policy
		inline_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       p.policy_data->>'OnDependents' as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND s.valid_from + ((p.policy_data->>'TTLSeconds')::int * interval '1 second') < now()
		),
		-- Standalone policies: attached via stack_policies junction table
		standalone_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       p.policy_data->>'OnDependents' as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN stack_policies sp ON sp.stack_id = s.id
			JOIN latest_policies p ON p.id = sp.policy_id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND (p.stack_id IS NULL OR p.stack_id = '')  -- standalone policies have NULL or empty stack_id
			AND s.valid_from + ((p.policy_data->>'TTLSeconds')::int * interval '1 second') < now()
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

	rows, err := d.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []datastore.ExpiredStackInfo
	for rows.Next() {
		var info datastore.ExpiredStackInfo
		var onDependents *string
		if err := rows.Scan(&info.StackLabel, &info.StackID, &onDependents); err != nil {
			return nil, err
		}
		if onDependents != nil {
			info.OnDependents = *onDependents
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

func (d DatastorePostgres) GetStacksWithAutoReconcilePolicy() ([]datastore.StackReconcileInfo, error) {
	ctx, span := tracer.Start(context.Background(), "GetStacksWithAutoReconcilePolicy")
	defer span.End()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
		),
		inline_auto_reconcile AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       (p.policy_data->>'IntervalSeconds')::bigint as interval_seconds
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'auto-reconcile'
		),
		standalone_auto_reconcile AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       (p.policy_data->>'IntervalSeconds')::bigint as interval_seconds
			FROM latest_stacks s
			JOIN stack_policies sp ON sp.stack_id = s.id
			JOIN latest_policies p ON p.id = sp.policy_id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'auto-reconcile'
			AND (p.stack_id IS NULL OR p.stack_id = '')
		),
		all_auto_reconcile AS (
			SELECT * FROM inline_auto_reconcile
			UNION
			SELECT * FROM standalone_auto_reconcile
		),
		last_reconcile AS (
			SELECT ru.stack_label, MAX(fc.timestamp) as last_reconcile_at
			FROM resource_updates ru
			JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE fc.config_mode = 'reconcile'
			AND fc.state = 'Success'
			GROUP BY ru.stack_label
		)
		SELECT ar.stack_label, ar.stack_id, ar.interval_seconds,
		       COALESCE(lr.last_reconcile_at, '1970-01-01 00:00:00'::timestamp) as last_reconcile_at
		FROM all_auto_reconcile ar
		LEFT JOIN last_reconcile lr ON ar.stack_label = lr.stack_label
	`

	rows, err := d.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []datastore.StackReconcileInfo
	for rows.Next() {
		var info datastore.StackReconcileInfo
		if err := rows.Scan(&info.StackLabel, &info.StackID, &info.IntervalSeconds, &info.LastReconcileAt); err != nil {
			return nil, err
		}
		result = append(result, info)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (d DatastorePostgres) GetResourcesAtLastReconcile(stackLabel string) ([]datastore.ResourceSnapshot, error) {
	ctx, span := tracer.Start(context.Background(), "GetResourcesAtLastReconcile")
	defer span.End()

	// Declared state for auto-reconcile: per-resource DesiredState from the
	// most recent user-source reconcile that touched each resource. Failed
	// reconciles count (so failed updates are retried until they converge);
	// Canceled and InProgress reconciles do not (they aren't accepted user
	// intent).
	//
	// Reading per-resource rather than per-command is the key invariant.
	// The generator only emits resource_updates rows for resources whose
	// state actually changes — unchanged resources produce no row. If we
	// scoped the snapshot to a single reconcile command, a partial reconcile
	// that changed only some resources would yield a desired-state Forma
	// that omits the unchanged ones, and auto-reconcile would implicitly
	// delete them as drift. Taking the most recent user-source reconcile
	// row per ksuid keeps unchanged resources represented by the earlier
	// reconcile that last declared them.
	//
	// Destroy commands also contribute to the baseline. A destroy is the
	// user's latest declaration that the named resources should not exist —
	// its resource_updates rows have operation='delete' and become the
	// latest-per-ksuid touch for any destroyed resource. The outer filter
	// (operation != 'delete') then drops them from the snapshot, yielding
	// the correct empty desired baseline for fully-destroyed stacks (or
	// the correctly trimmed baseline for partial destroys). Destroys land
	// in forma_commands with command='destroy' and config_mode='patch', so
	// the OR branch admits them without further filtering on config_mode.
	//
	// The resource column is stored as TEXT; cast to json once in the CTE
	// so the downstream extractions can use the JSON operators.
	//
	// Delete operations are excluded from the outer SELECT: a deletion the
	// user requested is not part of the desired state going forward.
	query := `
		WITH user_reconcile_updates AS (
			SELECT ru.ksuid, ru.resource::json AS resource_json, ru.operation, fc.timestamp
			FROM resource_updates ru
			INNER JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE (
				(fc.command = 'apply' AND fc.config_mode = 'reconcile')
				OR fc.command = 'destroy'
			)
			AND fc.state IN ('Success', 'Failed')
			AND ru.source = 'user'
			AND ru.stack_label = $1
		),
		latest_per_ksuid AS (
			SELECT ksuid, resource_json, operation,
			       ROW_NUMBER() OVER (
			           PARTITION BY ksuid
			           ORDER BY timestamp DESC,
			                    CASE WHEN operation = 'delete' THEN 1 ELSE 0 END
			       ) as rn
			FROM user_reconcile_updates
		)
		SELECT ksuid,
		       resource_json->>'Type'      as type,
		       resource_json->>'Label'     as label,
		       resource_json->>'Target'    as target,
		       resource_json->'Properties' as properties,
		       resource_json->'Schema'     as schema,
		       resource_json->>'NativeID'  as native_id
		FROM latest_per_ksuid
		WHERE rn = 1 AND operation != 'delete'
	`

	rows, err := d.pool.Query(ctx, query, stackLabel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []datastore.ResourceSnapshot
	for rows.Next() {
		var snapshot datastore.ResourceSnapshot
		var propsData, schemaData []byte
		var nativeID *string
		if err := rows.Scan(&snapshot.KSUID, &snapshot.Type, &snapshot.Label, &snapshot.Target, &propsData, &schemaData, &nativeID); err != nil {
			return nil, err
		}
		if propsData != nil {
			snapshot.Properties = json.RawMessage(propsData)
		}
		if schemaData != nil {
			if err := json.Unmarshal(schemaData, &snapshot.Schema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal schema for resource %s: %w", snapshot.Label, err)
			}
		}
		if nativeID != nil {
			snapshot.NativeID = *nativeID
		}
		result = append(result, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (d DatastorePostgres) StackHasActiveCommands(stackLabel string) (bool, error) {
	ctx, span := tracer.Start(context.Background(), "StackHasActiveCommands")
	defer span.End()

	query := `
		SELECT EXISTS (
			SELECT 1 FROM resource_updates ru
			JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE ru.stack_label = $1
			AND fc.state NOT IN ('Success', 'Failed', 'Canceled')
		)
	`

	var exists bool
	err := d.pool.QueryRow(ctx, query, stackLabel).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func (d DatastorePostgres) LoadTarget(label string) (*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "LoadTarget")
	defer span.End()

	query := `
	SELECT version, namespace, config, config_schema, discoverable,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
	FROM targets
	WHERE label = $1
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(ctx, query, label)

	var version int
	var namespace string
	var config json.RawMessage
	var configSchemaRaw []byte
	var discoverable bool
	var incarnationID, healthState string
	var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
	var unreachableAccumSeconds int64
	var lastErrorCode *string
	var reapKind string
	var reapMaxUnreachableSeconds int64
	if err := row.Scan(&version, &namespace, &config, &configSchemaRaw, &discoverable,
		&incarnationID, &healthState, &lastSeenAt, &observedAt,
		&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
		&reapKind, &reapMaxUnreachableSeconds); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Target not found, return nil without error
		}
		return nil, err
	}

	var configSchema pkgmodel.ConfigSchema
	if len(configSchemaRaw) > 0 {
		if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
		}
	}

	return &pkgmodel.Target{
		Label:        label,
		Namespace:    namespace,
		Config:       config,
		ConfigSchema: configSchema,
		Discoverable: discoverable,
		Version:      version,
		Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
		Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
	}, nil
}

// buildPostgresTargetHealth constructs a TargetHealth from postgres scan results.
func buildPostgresTargetHealth(
	incarnationID string,
	healthState string,
	lastSeenAt *time.Time,
	observedAt *time.Time,
	firstUnreachableAt *time.Time,
	lastSampleAt *time.Time,
	unreachableAccumSeconds int64,
	lastErrorCode *string,
) *pkgmodel.TargetHealth {
	h := &pkgmodel.TargetHealth{
		IncarnationID:           incarnationID,
		State:                   healthState,
		LastSeenAt:              lastSeenAt,
		ObservedAt:              observedAt,
		FirstUnreachableAt:      firstUnreachableAt,
		LastSampleAt:            lastSampleAt,
		UnreachableAccumSeconds: unreachableAccumSeconds,
	}
	if lastErrorCode != nil {
		h.LastErrorCode = *lastErrorCode
	}
	return h
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
	SELECT t1.label, t1.version, t1.namespace, t1.config, t1.config_schema, t1.discoverable,
	       t1.target_incarnation_id, t1.health_state, t1.last_seen_at, t1.observed_at,
	       t1.first_unreachable_at, t1.last_sample_at, t1.unreachable_accum_seconds, t1.last_error_code,
	       t1.reap_kind, t1.reap_max_unreachable_seconds
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
		var configSchemaRaw []byte
		var discoverable bool
		var incarnationID, healthState string
		var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
		var unreachableAccumSeconds int64
		var lastErrorCode *string
		var reapKind string
		var reapMaxUnreachableSeconds int64
		if err := rows.Scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable,
			&incarnationID, &healthState, &lastSeenAt, &observedAt,
			&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
			&reapKind, &reapMaxUnreachableSeconds); err != nil {
			return nil, err
		}

		var configSchema pkgmodel.ConfigSchema
		if len(configSchemaRaw) > 0 {
			if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
			}
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
			Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
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
		SELECT label, version, namespace, config, config_schema, discoverable,
		       target_incarnation_id, health_state, last_seen_at, observed_at,
		       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
		       reap_kind, reap_max_unreachable_seconds
		FROM targets t1
		WHERE discoverable = TRUE
		AND NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)
	)
	SELECT DISTINCT ON (config) label, version, namespace, config, config_schema, discoverable,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
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
		var configSchemaRaw []byte
		var discoverable bool
		var incarnationID, healthState string
		var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
		var unreachableAccumSeconds int64
		var lastErrorCode *string
		var reapKind string
		var reapMaxUnreachableSeconds int64
		if err := rows.Scan(&label, &version, &ns, &config, &configSchemaRaw, &discoverable,
			&incarnationID, &healthState, &lastSeenAt, &observedAt,
			&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
			&reapKind, &reapMaxUnreachableSeconds); err != nil {
			return nil, err
		}

		var configSchema pkgmodel.ConfigSchema
		if len(configSchemaRaw) > 0 {
			if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
			}
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    ns,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
			Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) QueryTargets(query *datastore.TargetQuery) ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "QueryTargets")
	defer span.End()

	queryStr := `
		SELECT label, version, namespace, config, config_schema, discoverable,
		       target_incarnation_id, health_state, last_seen_at, observed_at,
		       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
		       reap_kind, reap_max_unreachable_seconds
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
		var configSchemaRaw []byte
		var discoverable bool
		var incarnationID, healthState string
		var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
		var unreachableAccumSeconds int64
		var lastErrorCode *string
		var reapKind string
		var reapMaxUnreachableSeconds int64
		if err := rows.Scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable,
			&incarnationID, &healthState, &lastSeenAt, &observedAt,
			&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
			&reapKind, &reapMaxUnreachableSeconds); err != nil {
			return nil, err
		}

		var configSchema pkgmodel.ConfigSchema
		if len(configSchemaRaw) > 0 {
			if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
			}
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
			Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
		})
	}

	return targets, rows.Err()
}

func (d DatastorePostgres) QueryResources(query *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
	ctx, span := tracer.Start(context.Background(), "QueryResources")
	defer span.End()

	queryStr := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND r1.operation != $1 AND r1.operation != 'reaped'
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
	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", datastore.CommandsTable)
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
	`, datastore.CommandsTable)
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
	`, datastore.CommandsTable)
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
	AND operation != $1 AND operation != 'reaped'
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
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
	AND operation != $1 AND operation != 'reaped'
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
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
	AND operation != $1 AND operation != 'reaped'
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
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
	WHERE operation != $1 AND operation != 'reaped'
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
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

func (d DatastorePostgres) StoreResource(resource *pkgmodel.Resource, commandID string, expectedIncarnation ...string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "StoreResource")
	defer span.End()

	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}

	inc := ""
	if len(expectedIncarnation) > 0 {
		inc = expectedIncarnation[0]
	}
	return d.storeResource(ctx, resource, jsonData, commandID, string(resource_update.OperationUpdate), inc)
}

func (d DatastorePostgres) storeResource(ctx context.Context, resource *pkgmodel.Resource, data []byte, commandID string, operation string, expectedIncarnation string) (string, error) {
	if resource.Ksuid == "" {
		resource.Ksuid = metautil.NewID()
	}

	// Reaped/incarnation guard. Deletes are exempt: a delete tombstone must
	// always be recordable. For every other write, inspect the resource's
	// current (max-version) row and reject the write when that row is a reaped
	// tombstone, or when an expected incarnation was supplied and does not match
	// the incarnation stamped on the current row. An empty stored incarnation
	// skips the incarnation check.
	if operation != string(resource_update.OperationDelete) {
		var curOp, curInc string
		guardErr := d.pool.QueryRow(ctx,
			`SELECT operation, COALESCE(target_incarnation_id, '') FROM resources WHERE uri = $1 ORDER BY version COLLATE "C" DESC LIMIT 1`,
			resource.URI(),
		).Scan(&curOp, &curInc)
		if guardErr != nil && !errors.Is(guardErr, pgx.ErrNoRows) {
			return "", fmt.Errorf("failed to evaluate resource write guard: %w", guardErr)
		}
		if guardErr == nil {
			if curOp == string(resource_update.OperationReaped) {
				return "", fmt.Errorf("%w: resource %s current row is reaped", datastore.ErrResourceWriteRejected, resource.URI())
			}
			if expectedIncarnation != "" && curInc != "" && curInc != expectedIncarnation {
				return "", fmt.Errorf("%w: resource %s incarnation %q does not match expected %q",
					datastore.ErrResourceWriteRejected, resource.URI(), curInc, expectedIncarnation)
			}
		}
	}

	// Check if this resource already exists by native_id and type
	query := `SELECT ksuid, data, uri, version, managed FROM resources WHERE native_id = $1 AND type = $2 ORDER BY version COLLATE "C" DESC LIMIT 1`
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
		INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid, target_incarnation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
			expectedIncarnation,
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
	if !managed && operation != string(resource_update.OperationDelete) {
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

	readWriteEqual, readOnlyEqual := datastore.ResourcesAreEqual(resource, &existingResource)

	if operation == string(resource_update.OperationDelete) {
		// For delete operations, check if the latest version is already a delete
		var latestOperation string
		query = `SELECT operation FROM resources WHERE uri = $1 ORDER BY version COLLATE "C" DESC LIMIT 1`
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
		if readWriteEqual && readOnlyEqual && resource.Ksuid == ksuid {
			// Resource data is identical and KSUID matches, return existing version ID
			return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
		}
	}

	var newVersion string
	if readWriteEqual && !readOnlyEqual {
		newVersion = version
	} else {
		newVersion = mksuid.New().String()
	}

	query = `
	INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid, target_incarnation_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
	ksuid = EXCLUDED.ksuid,
	target_incarnation_id = EXCLUDED.target_incarnation_id
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
		expectedIncarnation,
	)
	if err != nil {
		slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
		return "", fmt.Errorf("failed to store resource: %w", err)
	}

	return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
}

func (d DatastorePostgres) BulkStoreResources(resources []pkgmodel.Resource, commandID string) (string, error) {
	_, span := tracer.Start(context.Background(), "BulkStoreResources")
	defer span.End()

	var lastVersionID string
	for _, resource := range resources {
		versionID, err := d.StoreResource(&resource, commandID)
		if err != nil {
			slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
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

	var configSchemaJSON []byte
	if len(target.ConfigSchema.Hints) > 0 {
		configSchemaJSON, err = json.Marshal(target.ConfigSchema)
		if err != nil {
			return "", err
		}
	}

	incarnationID := mksuid.New().String()

	reapKind, reapMaxUnreachableSeconds, err := pkgmodel.ReapingToColumns(target.Reaping)
	if err != nil {
		return "", err
	}

	query := `
	INSERT INTO targets (label, version, namespace, config, config_schema, discoverable,
	                     target_incarnation_id, health_state, unreachable_accum_seconds,
	                     reap_kind, reap_max_unreachable_seconds)
	VALUES ($1, 1, $2, $3, $4, $5, $6, 'unknown', 0, $7, $8)
	`
	_, err = d.pool.Exec(ctx, query, target.Label, target.Namespace, cfg, configSchemaJSON, target.Discoverable, incarnationID, reapKind, reapMaxUnreachableSeconds)
	if err != nil {
		slog.Debug("failed to create target (may be retried as update)", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d DatastorePostgres) UpdateTarget(target *pkgmodel.Target) (string, error) {
	ctx, span := tracer.Start(context.Background(), "UpdateTarget")
	defer span.End()

	// Load the latest row to carry health state forward onto the new version.
	healthQuery := `
		SELECT version, target_incarnation_id, health_state, last_seen_at, observed_at,
		       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code
		FROM targets WHERE label = $1 ORDER BY version DESC LIMIT 1`
	healthRow := d.pool.QueryRow(ctx, healthQuery, target.Label)

	var currentVersion int64
	var incarnationID, healthState *string
	var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
	var unreachableAccumSeconds *int64
	var lastErrorCode *string
	if err := healthRow.Scan(&currentVersion, &incarnationID, &healthState, &lastSeenAt, &observedAt,
		&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("target %s does not exist, cannot update", target.Label)
		}
		return "", err
	}

	safeStr := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	safeInt64 := func(i *int64) int64 {
		if i == nil {
			return 0
		}
		return *i
	}

	newVersion := int(currentVersion) + 1
	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	var configSchemaJSON []byte
	if len(target.ConfigSchema.Hints) > 0 {
		configSchemaJSON, err = json.Marshal(target.ConfigSchema)
		if err != nil {
			return "", err
		}
	}

	reapKind, reapMaxUnreachableSeconds, err := pkgmodel.ReapingToColumns(target.Reaping)
	if err != nil {
		return "", err
	}

	// Recovery: when the current row has been reaped, re-declaring the target
	// mints a fresh incarnation id and resets health to 'unknown' (accrual 0,
	// timestamps cleared) rather than carrying the reaped state forward.
	newIncarnationID := safeStr(incarnationID)
	newHealthState := safeStr(healthState)
	newLastSeenAt := lastSeenAt
	newObservedAt := observedAt
	newFirstUnreachableAt := firstUnreachableAt
	newLastSampleAt := lastSampleAt
	newUnreachableAccumSeconds := safeInt64(unreachableAccumSeconds)
	newLastErrorCode := lastErrorCode
	recovered := safeStr(healthState) == pkgmodel.TargetHealthStateReaped
	if recovered {
		newIncarnationID = mksuid.New().String()
		newHealthState = pkgmodel.TargetHealthStateUnknown
		newLastSeenAt = nil
		newObservedAt = nil
		newFirstUnreachableAt = nil
		newLastSampleAt = nil
		newUnreachableAccumSeconds = 0
		newLastErrorCode = nil
	}

	insertQuery := `
		INSERT INTO targets (label, version, namespace, config, config_schema, discoverable,
		                     target_incarnation_id, health_state, last_seen_at, observed_at,
		                     first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
		                     reap_kind, reap_max_unreachable_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

	// The version INSERT and the recovery un-reap must be atomic. A crash strictly
	// between them would leave the target recovered (fresh incarnation, health
	// 'unknown') while its resources stayed marked 'reaped'; a resumed UpdateTarget
	// would not re-trigger the un-reap (the target is no longer reaped), stranding
	// those resources as invisible tombstones the write-guard permanently rejects.
	// One transaction makes it both-or-neither.
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to begin target update transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx, insertQuery, target.Label, newVersion, target.Namespace, cfg, configSchemaJSON, target.Discoverable,
		newIncarnationID, newHealthState, newLastSeenAt, newObservedAt, newFirstUnreachableAt, newLastSampleAt,
		newUnreachableAccumSeconds, newLastErrorCode, reapKind, reapMaxUnreachableSeconds)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	// Recovery: un-reap the target's tombstoned resource rows and stamp them with
	// the fresh incarnation, so a subsequent re-adopt write is accepted rather than
	// rejected as a reaped tombstone. See the SQLite UpdateTarget for the rationale.
	if recovered {
		if _, err = tx.Exec(ctx, `
			UPDATE resources SET operation = $1, target_incarnation_id = $2
			WHERE target = $3
			  AND operation = 'reaped'
			  AND NOT EXISTS (
			    SELECT 1 FROM resources r2
			    WHERE r2.uri = resources.uri AND r2.version > resources.version
			  )`,
			string(resource_update.OperationUpdate), newIncarnationID, target.Label); err != nil {
			slog.Error("failed to un-reap resources on target recovery", "error", err, "label", target.Label)
			return "", err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit target update transaction: %w", err)
	}
	committed = true

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

func (d DatastorePostgres) UpdateTargetHealth(obs pkgmodel.TargetHealthObservation) (bool, error) {
	ctx, span := tracer.Start(context.Background(), "UpdateTargetHealth")
	defer span.End()

	var lastSeenAt *time.Time
	if obs.LastSeenAt != nil {
		t := obs.LastSeenAt.UTC()
		lastSeenAt = &t
	}
	observedAt := obs.ObservedAt.UTC()

	var lastErrorCode *string
	if obs.LastErrorCode != "" {
		lastErrorCode = &obs.LastErrorCode
	}

	// A reachable ("success") observation clears any accrued unreachability:
	// the target is healthy again, so first_unreachable_at and the accumulated
	// unreachable seconds reset to their pristine (never-unreachable) values.
	accrualReset := ""
	if obs.State == pkgmodel.TargetHealthStateReachable {
		accrualReset = `,
				first_unreachable_at = NULL,
				unreachable_accum_seconds = 0`
	}

	var rowsAffected int64
	var err error
	if obs.IncarnationID != "" {
		query := fmt.Sprintf(`
			UPDATE targets SET
				health_state = $1,
				observed_at = $2,
				last_seen_at = COALESCE($3, last_seen_at),
				last_error_code = $4%s
			WHERE label = $5
			  AND version = (SELECT MAX(version) FROM targets WHERE label = $5)
			  AND health_state <> 'reaped'
			  AND (observed_at IS NULL OR observed_at < $2)
			  AND target_incarnation_id = $6`, accrualReset)
		tag, execErr := d.pool.Exec(ctx, query, obs.State, observedAt, lastSeenAt, lastErrorCode, obs.TargetLabel, obs.IncarnationID)
		err = execErr
		if execErr == nil {
			rowsAffected = tag.RowsAffected()
		}
	} else {
		query := fmt.Sprintf(`
			UPDATE targets SET
				health_state = $1,
				observed_at = $2,
				last_seen_at = COALESCE($3, last_seen_at),
				last_error_code = $4%s
			WHERE label = $5
			  AND version = (SELECT MAX(version) FROM targets WHERE label = $5)
			  AND health_state <> 'reaped'
			  AND (observed_at IS NULL OR observed_at < $2)`, accrualReset)
		tag, execErr := d.pool.Exec(ctx, query, obs.State, observedAt, lastSeenAt, lastErrorCode, obs.TargetLabel)
		err = execErr
		if execErr == nil {
			rowsAffected = tag.RowsAffected()
		}
	}
	if err != nil {
		return false, err
	}
	return rowsAffected == 1, nil
}

func (d DatastorePostgres) AdvanceTargetAccrual(targetLabel, incarnationID string, lastSampleAt time.Time, deltaSeconds int64) (bool, error) {
	ctx, span := tracer.Start(context.Background(), "AdvanceTargetAccrual")
	defer span.End()

	query := `
		UPDATE targets SET
			unreachable_accum_seconds = unreachable_accum_seconds + $1,
			last_sample_at = $2
		WHERE label = $3
		  AND version = (SELECT MAX(version) FROM targets WHERE label = $3)
		  AND health_state = 'unreachable'
		  AND target_incarnation_id = $4`

	tag, err := d.pool.Exec(ctx, query, deltaSeconds, lastSampleAt.UTC(), targetLabel, incarnationID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (d DatastorePostgres) CheckTargetsReaped(labels []string) ([]string, error) {
	ctx, span := tracer.Start(context.Background(), "CheckTargetsReaped")
	defer span.End()

	if len(labels) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(labels))
	args := make([]any, len(labels))
	for i, label := range labels {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = label
	}

	query := fmt.Sprintf(`
	SELECT t1.label
	FROM targets t1
	WHERE t1.label IN (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	AND t1.health_state = 'reaped'
	`, strings.Join(placeholders, ","))

	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reaped []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, err
		}
		reaped = append(reaped, label)
	}

	return reaped, rows.Err()
}

func (d DatastorePostgres) GetUnreachableTargets() ([]*pkgmodel.Target, error) {
	ctx, span := tracer.Start(context.Background(), "GetUnreachableTargets")
	defer span.End()

	query := `
	SELECT label, version, namespace, config, config_schema, discoverable,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
	FROM targets t1
	WHERE NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	AND t1.health_state = 'unreachable'
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
		var configSchemaRaw []byte
		var discoverable bool
		var incarnationID, healthState string
		var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt *time.Time
		var unreachableAccumSeconds int64
		var lastErrorCode *string
		var reapKind string
		var reapMaxUnreachableSeconds int64
		if err := rows.Scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable,
			&incarnationID, &healthState, &lastSeenAt, &observedAt,
			&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode,
			&reapKind, &reapMaxUnreachableSeconds); err != nil {
			return nil, err
		}

		var configSchema pkgmodel.ConfigSchema
		if len(configSchemaRaw) > 0 {
			if err := json.Unmarshal(configSchemaRaw, &configSchema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config_schema for target %s: %w", label, err)
			}
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
			Health:       buildPostgresTargetHealth(incarnationID, healthState, lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt, unreachableAccumSeconds, lastErrorCode),
		})
	}

	return targets, rows.Err()
}

// PersistTargetReap performs the whole target reap in one transaction. See the
// Datastore interface for the contract. Postgres compares the native
// timestamptz grace columns directly and extracts JSON references with jsonb
// operators (the resource/target_updates columns are TEXT, so they are cast).
func (d DatastorePostgres) PersistTargetReap(req datastore.PersistTargetReapRequest) (bool, error) {
	ctx, span := tracer.Start(context.Background(), "PersistTargetReap")
	defer span.End()

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	// 1. Conditional transition FIRST — the atomic CAS (no locks). Thresholds are
	//    re-read from the row's OWN persisted columns, never from the request.
	casTag, err := tx.Exec(ctx, `
		UPDATE targets SET health_state = 'reaped'
		WHERE label = $1
		  AND version = (SELECT MAX(version) FROM targets WHERE label = $1)
		  AND target_incarnation_id = $2
		  AND health_state = 'unreachable'
		  AND reap_kind = 'after'
		  AND unreachable_accum_seconds >= reap_max_unreachable_seconds
		  AND last_seen_at <= $3
		  AND last_sample_at <= $4`,
		req.Label, req.IncarnationID, req.LastSeenBefore.UTC(), req.LastSampleBefore.UTC())
	if err != nil {
		return false, err
	}
	if casTag.RowsAffected() != 1 {
		return false, nil
	}

	var accumSeconds int64
	if err = tx.QueryRow(ctx,
		`SELECT unreachable_accum_seconds FROM targets
		 WHERE label = $1 AND version = (SELECT MAX(version) FROM targets WHERE label = $1)`,
		req.Label,
	).Scan(&accumSeconds); err != nil {
		return false, err
	}

	// 2. Active-command assertion.
	var active bool
	if err = tx.QueryRow(ctx, `
		SELECT
		  EXISTS (
		    SELECT 1 FROM resource_updates ru
		    JOIN forma_commands fc ON ru.command_id = fc.command_id
		    WHERE fc.command <> 'sync'
		      AND fc.state NOT IN ('Success', 'Failed', 'Canceled')
		      AND ru.resource IS NOT NULL
		      AND (ru.resource::jsonb ->> 'Target') = $1
		  )
		  OR EXISTS (
		    SELECT 1 FROM forma_commands fc
		    WHERE fc.command <> 'sync'
		      AND fc.state NOT IN ('Success', 'Failed', 'Canceled')
		      AND fc.target_updates IS NOT NULL
		      AND jsonb_typeof(fc.target_updates::jsonb) = 'array'
		      AND EXISTS (
		        SELECT 1 FROM jsonb_array_elements(fc.target_updates::jsonb) e
		        WHERE (e -> 'Target' ->> 'Label') = $1
		      )
		  )`, req.Label).Scan(&active); err != nil {
		return false, err
	}
	if active {
		return false, nil
	}

	// 3. Tombstone every current-row resource on this target.
	tombTag, err := tx.Exec(ctx, `
		UPDATE resources SET operation = 'reaped'
		WHERE target = $1
		  AND operation <> 'delete' AND operation <> 'reaped'
		  AND NOT EXISTS (
		    SELECT 1 FROM resources r2
		    WHERE r2.uri = resources.uri AND r2.version > resources.version
		  )`, req.Label)
	if err != nil {
		return false, err
	}
	resourceCount := tombTag.RowsAffected()

	// 4. Insert the UNIQUE audit row.
	if _, err = tx.Exec(ctx,
		`INSERT INTO target_reap_audit (incarnation_id, label, reaped_at, accum_seconds, resource_count)
		 VALUES ($1, $2, $3, $4, $5)`,
		req.IncarnationID, req.Label, req.ReapedAt.UTC(), accumSeconds, resourceCount,
	); err != nil {
		return false, err
	}

	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

func (d DatastorePostgres) DeleteTarget(targetLabel string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "DeleteTarget")
	defer span.End()

	// Hard delete all versions of the target
	query := `DELETE FROM targets WHERE label = $1`
	result, err := d.pool.Exec(ctx, query, targetLabel)
	if err != nil {
		slog.Error("Failed to delete target", "error", err, "label", targetLabel)
		return "", err
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		return "", fmt.Errorf("target %s does not exist, cannot delete", targetLabel)
	}

	return fmt.Sprintf("%s_deleted", targetLabel), nil
}

func (d DatastorePostgres) CountResourcesInTarget(targetLabel string) (int, error) {
	ctx, span := tracer.Start(context.Background(), "CountResourcesInTarget")
	defer span.End()

	// Count only latest version of resources that haven't been deleted
	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE target = $1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE "C" > r1.version COLLATE "C"
		)
		AND operation != $2 AND operation != 'reaped'
	`
	row := d.pool.QueryRow(ctx, query, targetLabel, resource_update.OperationDelete)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
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
				remaining_resolvables, reference_labels, previous_properties,
				is_cascade, cascade_source
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
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
				previous_properties = EXCLUDED.previous_properties,
				is_cascade = EXCLUDED.is_cascade,
				cascade_source = EXCLUDED.cascade_source
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
			ru.IsCascade,
			ru.CascadeSource,
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
			remaining_resolvables, reference_labels, previous_properties,
			is_cascade, cascade_source
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
		var ruIsCascade *bool
		var ruCascadeSource *string

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
			&ruIsCascade,
			&ruCascadeSource,
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

		if ruIsCascade != nil {
			ru.IsCascade = *ruIsCascade
		}
		if ruCascadeSource != nil {
			ru.CascadeSource = *ruCascadeSource
		}

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
		  AND state NOT IN ('Success','Failed','Rejected','Canceled')
	`

	result, err := d.pool.Exec(ctx, query, string(state), modifiedTs.UTC(), commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update state: %w", err)
	}

	if result.RowsAffected() == 0 {
		slog.Debug("UpdateResourceUpdateState: row already in terminal state or not found, no-op", "commandID", commandID, "ksuid", ksuid)
		return nil
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
func (d DatastorePostgres) BatchUpdateResourceUpdateState(commandID string, refs []datastore.ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
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
			  AND state NOT IN ('Success','Failed','Rejected','Canceled')
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

func (d DatastorePostgres) UpdateFormaCommandTargetUpdates(commandID string, targetUpdatesJSON json.RawMessage, state forma_command.CommandState, modifiedTs time.Time) error {
	ctx, span := tracer.Start(context.Background(), "UpdateFormaCommandTargetUpdates")
	defer span.End()

	query := `UPDATE forma_commands SET target_updates = $1, state = $2, modified_ts = $3 WHERE command_id = $4`
	result, err := d.pool.Exec(ctx, query, string(targetUpdatesJSON), string(state), modifiedTs.UTC(), commandID)
	if err != nil {
		return fmt.Errorf("failed to update forma command target updates: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("forma command not found: %s", commandID)
	}

	return nil
}

func (d DatastorePostgres) Close() {
	d.pool.Close()
}

// Pool returns the underlying connection pool. Used by test helpers that need
// direct SQL access (e.g. forcing health_state for guard assertions).
func (d DatastorePostgres) Pool() *pgxpool.Pool { return d.pool }

// This can be only used in tests or in setups where we have access to admin (non-production)
func (d DatastorePostgres) CleanUp() error {
	connStr := BuildConnStr(d.cfg.Postgres.Host, d.cfg.Postgres.Port, d.cfg.Postgres.User, d.cfg.Postgres.Password, d.cfg.Postgres.Database)

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

// ForceCancelResourceUpdates CAS-terminalizes in-flight resource updates to Canceled in one
// transaction. For InProgress rows it also writes force-cancel progress. Returns the rows
// transitioned (split by prior state) and those already terminal (Skipped). Idempotent.
func (d DatastorePostgres) ForceCancelResourceUpdates(commandID string, inProgress []datastore.ForceCancelRow, notStarted []datastore.ResourceUpdateRef, modifiedTs time.Time) (datastore.ForceCancelResult, error) {
	ctx, span := tracer.Start(context.Background(), "ForceCancelResourceUpdates")
	defer span.End()

	var result datastore.ForceCancelResult

	if len(inProgress) == 0 && len(notStarted) == 0 {
		return result, nil
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	modifiedTsUTC := modifiedTs.UTC()

	for _, row := range inProgress {
		ref := datastore.ResourceUpdateRef{KSUID: row.KSUID, Operation: row.Operation}
		res, execErr := tx.Exec(ctx, `
			UPDATE resource_updates
			SET state = 'Canceled', modified_ts = $1, progress_result = $2, most_recent_progress = $3
			WHERE command_id = $4 AND ksuid = $5 AND operation = $6 AND state = 'InProgress'
		`, modifiedTsUTC, []byte(row.ProgressJSON), []byte(row.MostRecentProgressJSON), commandID, row.KSUID, string(row.Operation))
		if execErr != nil {
			err = execErr
			return result, fmt.Errorf("failed to force-cancel InProgress row %s: %w", row.KSUID, err)
		}
		if res.RowsAffected() > 0 {
			result.CanceledInProgress = append(result.CanceledInProgress, ref)
		} else {
			result.Skipped = append(result.Skipped, ref)
		}
	}

	for _, ref := range notStarted {
		res, execErr := tx.Exec(ctx, `
			UPDATE resource_updates
			SET state = 'Canceled', modified_ts = $1
			WHERE command_id = $2 AND ksuid = $3 AND operation = $4 AND state = 'NotStarted'
		`, modifiedTsUTC, commandID, ref.KSUID, string(ref.Operation))
		if execErr != nil {
			err = execErr
			return result, fmt.Errorf("failed to force-cancel NotStarted row %s: %w", ref.KSUID, err)
		}
		if res.RowsAffected() > 0 {
			result.CanceledNotStarted = append(result.CanceledNotStarted, ref)
		} else {
			result.Skipped = append(result.Skipped, ref)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return result, nil
}
