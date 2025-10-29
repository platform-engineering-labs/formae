// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/demula/mksuid/v2"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

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

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		slog.Error("failed to connect to PostgreSQL database", "error", err)
		return nil, err
	}

	d := DatastorePostgres{pool: pool, agentID: agentID, cfg: cfg, ctx: ctx}

	_, err = d.pool.Exec(d.ctx, fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (
		command_id TEXT NOT NULL,
		timestamp TIMESTAMP NOT NULL,
		command TEXT NOT NULL,
		state TEXT NOT NULL,
		agent_version TEXT,
		client_id TEXT,
		agent_id TEXT,
		data JSONB,
		PRIMARY KEY (command_id)
	)`, CommandsTable))
	if err != nil {
		slog.Error(fmt.Sprintf("failed to create %s table", CommandsTable), "error", err)
		return nil, err
	}

	if err = d.createIndex(CommandsTable, "timestamp"); err != nil {
		return nil, err
	}

	if err = d.createIndex(CommandsTable, "command"); err != nil {
		return nil, err
	}

	if err = d.createIndex(CommandsTable, "state"); err != nil {
		return nil, err
	}

	if err = d.createIndex(CommandsTable, "agent_version"); err != nil {
		return nil, err
	}

	if err = d.createIndex(CommandsTable, "agent_id"); err != nil {
		return nil, err
	}

	if err = d.createIndex(CommandsTable, "client_id"); err != nil {
		return nil, err
	}

	_, err = d.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS resources (
		uri TEXT NOT NULL,
		version TEXT NOT NULL,
		valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		command_id TEXT,
		operation TEXT,
		native_id TEXT,
		stack TEXT,
		type TEXT,
		label TEXT,
		target TEXT,
		data JSONB,
		managed BOOLEAN DEFAULT TRUE,
		ksuid TEXT,
		PRIMARY KEY (uri, version)
	)`)
	if err != nil {
		slog.Error("failed to create resources table", "error", err)
		return nil, err
	}

	if err = d.createIndex("resources", "valid_from"); err != nil {
		return nil, err
	}

	if err = d.createIndex("resources", "command_id"); err != nil {
		return nil, err
	}

	if err = d.createIndex("resources", "operation"); err != nil {
		return nil, err
	}

	if err = d.createIndex("resources", "native_id"); err != nil {
		return nil, err
	}

	if err = d.createIndex("resources", "stack"); err != nil {
		return nil, err
	}

	if err = d.createIndex("resources", "type"); err != nil {
		return nil, err
	}

	if err = d.createIndex("resources", "label"); err != nil {
		return nil, err
	}

	if err := d.createCompositeIndex("resources", []string{"stack", "label", "type", "version"}); err != nil {
		return nil, err
	}

	if err := d.createIndex("resources", "ksuid"); err != nil {
		return nil, err
	}

	_, err = d.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS targets (
		label TEXT NOT NULL,
		version INTEGER NOT NULL,
		namespace TEXT NOT NULL,
		config JSONB,
		discoverable BOOLEAN DEFAULT TRUE,
		PRIMARY KEY (label, version)
	)`)
	if err != nil {
		slog.Error("failed to create targets table", "error", err)
		return nil, err
	}

	_, err = d.pool.Exec(context.Background(), `ALTER TABLE targets ADD COLUMN IF NOT EXISTS discoverable BOOLEAN DEFAULT TRUE`)
	if err != nil {
		slog.Warn("Could not add discoverable column to targets table", "error", err)
	}

	if err := d.createIndex("targets", "namespace"); err != nil {
		return nil, err
	}

	if err := d.createIndex("targets", "discoverable"); err != nil {
		return nil, err
	}

	slog.Info("Started PostgreSQL datastore", "host", cfg.Postgres.Host, "port", cfg.Postgres.Port, "database", cfg.Postgres.Database, "schema", cfg.Postgres.Schema, "user", cfg.Postgres.User, "connectionParams", cfg.Postgres.ConnectionParams)

	return d, nil
}

func (d DatastorePostgres) createIndex(tableName, columnName string) error {
	indexName := fmt.Sprintf("idx_%s_%s", tableName, columnName)
	_, err := d.pool.Exec(context.Background(), fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", indexName, tableName, columnName))
	if err != nil {
		slog.Error(fmt.Sprintf("failed to create index on %s (%s)", tableName, columnName), "error", err)
		return err
	}
	return nil
}

func (d DatastorePostgres) createCompositeIndex(tableName string, columns []string) error {
	indexName := fmt.Sprintf("idx_%s_%s", tableName, strings.Join(columns, "_"))
	columnList := strings.Join(columns, ", ")
	_, err := d.pool.Exec(context.Background(), fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", indexName, tableName, columnList))
	if err != nil {
		slog.Error(fmt.Sprintf("failed to create composite index on %s (%s)", tableName, columnList), "error", err)
		return err
	}
	return nil
}

func (d DatastorePostgres) StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	jsonData, err := json.Marshal(fa)
	if err != nil {
		return err
	}

	for _, r := range fa.ResourceUpdates {
		if r.Resource.Properties == nil {
			slog.Debug("Resource properties are empty for resource", "resourceLabel", r.Resource.Label, "commandID", commandID)
		}
	}

	query := fmt.Sprintf(`
	INSERT INTO %s (command_id, timestamp, command, state, agent_version, client_id, agent_id, data)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (command_id) DO UPDATE
	SET timestamp = EXCLUDED.timestamp,
	command = EXCLUDED.command,
	state = EXCLUDED.state,
	agent_version = EXCLUDED.agent_version,
	client_id = EXCLUDED.client_id,
	agent_id = EXCLUDED.agent_id,
	data = EXCLUDED.data
	`, CommandsTable)

	_, err = d.pool.Exec(context.Background(), query, commandID, fa.StartTs, fa.Command, fa.State, formae.Version, fa.ClientID, d.agentID, jsonData)
	if err != nil {
		slog.Error("failed to store FormaCommand", "query", query, "error", err)
		return err
	}

	return nil
}

func (d DatastorePostgres) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	query := fmt.Sprintf("SELECT data FROM %s", CommandsTable)
	rows, err := d.pool.Query(context.Background(), query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []*forma_command.FormaCommand
	for rows.Next() {
		var jsonData string
		if err := rows.Scan(&jsonData); err != nil {
			return nil, err
		}

		var app forma_command.FormaCommand
		if err := json.Unmarshal([]byte(jsonData), &app); err != nil {
			return nil, err
		}

		commands = append(commands, &app)
	}

	return commands, rows.Err()
}

func (d DatastorePostgres) DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE command_id = $1", CommandsTable)
	_, err := d.pool.Exec(context.Background(), query, commandID)
	return err
}

func (d DatastorePostgres) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	query := fmt.Sprintf("SELECT data FROM %s WHERE command_id = $1", CommandsTable)
	row := d.pool.QueryRow(context.Background(), query, commandID)

	var jsonData string
	if err := row.Scan(&jsonData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("forma command not found: %v", commandID)
		}
		return nil, err
	}

	var app forma_command.FormaCommand
	if err := json.Unmarshal([]byte(jsonData), &app); err != nil {
		return nil, err
	}

	return &app, nil
}

func (d DatastorePostgres) GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error) {
	query := fmt.Sprintf(`
	SELECT data FROM %s
	WHERE client_id = $1
	ORDER BY timestamp DESC
	LIMIT 1
	`, CommandsTable)
	row := d.pool.QueryRow(context.Background(), query, clientID)

	var jsonData string
	if err := row.Scan(&jsonData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("no forma commands found for client: %v", clientID)
		}
		return nil, err
	}

	var command forma_command.FormaCommand
	if err := json.Unmarshal([]byte(jsonData), &command); err != nil {
		return nil, err
	}

	return &command, nil
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
	queryStr := fmt.Sprintf("SELECT data FROM %s WHERE 1=1", CommandsTable)
	args := []any{}

	queryStr = extendPostgresQueryString(queryStr, query.CommandID, " AND command_id %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.ClientID, " AND client_id %s $%d", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Command, " AND LOWER(command) %s LOWER($%d)", &args)
	if query.Command == nil {
		queryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}

	queryStr = extendPostgresQueryString(queryStr, query.Stack, " AND EXISTS (SELECT 1 FROM jsonb_array_elements(data->'ResourceUpdates') AS elem WHERE elem->'Resource'->>'Stack' %s $%d)", &args)
	queryStr = extendPostgresQueryString(queryStr, query.Status, " AND LOWER(state) %s LOWER($%d)", &args)

	queryStr += " ORDER BY timestamp DESC"
	if query.N > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, query.N)
	}

	rows, err := d.pool.Query(context.Background(), queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []*forma_command.FormaCommand
	for rows.Next() {
		var jsonData string
		if err := rows.Scan(&jsonData); err != nil {
			return nil, err
		}

		var app forma_command.FormaCommand
		if err := json.Unmarshal([]byte(jsonData), &app); err != nil {
			return nil, err
		}

		commands = append(commands, &app)
	}

	return commands, rows.Err()
}

func (d DatastorePostgres) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
	query := `
	SELECT ksuid
	FROM resources
	WHERE stack = $1 AND label = $2 AND LOWER(type) = LOWER($3)
	AND operation != $4
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(context.Background(), query, stack, label, resourceType, resource_update.OperationDelete)

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
	AND NOT EXISTS (
		SELECT 1 FROM resources r2
		WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
		AND r2.version > r1.version
	)
	`, strings.Join(placeholders, ","))

	rows, err := d.pool.Query(context.Background(), query, args...)
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
		SELECT timestamp
		FROM forma_commands
		WHERE data->'Config'->>'Mode' = 'reconcile'
		ORDER BY timestamp DESC
		LIMIT 1
	)
	AND T2.stack = $1;
	`
	rows, err := d.pool.Query(context.Background(), query, stack)
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
	SELECT ksuid, stack, label, type
	FROM resources r1
	WHERE ksuid IN (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.ksuid = r2.ksuid
		AND r2.version > r1.version
	)
	`, strings.Join(placeholders, ","))

	rows, err := d.pool.Query(context.Background(), query, args...)
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
	query := `
	SELECT label
	FROM resources
	WHERE label = $1 OR label LIKE $2 || '-%'
	ORDER BY LENGTH(label) DESC, label DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(context.Background(), query, label, label)

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
	return d.storeResource(resource, []byte("{}"), commandID, string(resource_update.OperationDelete))
}

func (d DatastorePostgres) LoadAllResources() ([]*pkgmodel.Resource, error) {
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
	rows, err := d.pool.Query(context.Background(), query, resource_update.OperationDelete)
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

	rows, err := d.pool.Query(context.Background(), query, resource_update.OperationDelete)
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

	rows, err := d.pool.Query(context.Background(), query)
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
	query := fmt.Sprintf(`
	SELECT data
	FROM %s
	WHERE command != 'sync' AND state = $1
	`, CommandsTable)

	rows, err := d.pool.Query(context.Background(), query, forma_command.CommandStateInProgress)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []*forma_command.FormaCommand
	for rows.Next() {
		var jsonData string
		if err := rows.Scan(&jsonData); err != nil {
			return nil, err
		}

		var command forma_command.FormaCommand
		if err := json.Unmarshal([]byte(jsonData), &command); err != nil {
			return nil, err
		}

		commands = append(commands, &command)
	}

	return commands, rows.Err()
}

func (d DatastorePostgres) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	query := `
	SELECT data, ksuid
	FROM resources
	WHERE uri = $1
	AND operation != $2
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(context.Background(), query, uri, resource_update.OperationDelete)

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
	query := `
	SELECT data, ksuid
	FROM resources
	WHERE ksuid = $1
	AND operation != $2
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(context.Background(), query, ksuid, resource_update.OperationDelete)

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

func (d DatastorePostgres) LoadResourceByNativeID(nativeID string) (*pkgmodel.Resource, error) {
	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE native_id = $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	AND r1.operation != $2
	LIMIT 1
	`
	row := d.pool.QueryRow(context.Background(), query, nativeID, resource_update.OperationDelete)

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

	rows, err := d.pool.Query(context.Background(), query, stackLabel, resource_update.OperationDelete)
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
	query := `
	SELECT version, namespace, config, discoverable
	FROM targets
	WHERE label = $1
	ORDER BY version DESC
	LIMIT 1
	`
	row := d.pool.QueryRow(context.Background(), query, label)

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

	rows, err := d.pool.Query(context.Background(), query, args...)
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

func (d DatastorePostgres) LoadDiscoverableTargetsDistinctRegion() ([]*pkgmodel.Target, error) {
	// Get latest version per label where discoverable = true, deduplicated by region using DISTINCT ON
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
	SELECT DISTINCT ON (config->>'region') label, version, namespace, config, discoverable
	FROM latest_targets
	ORDER BY config->>'region', version DESC`

	rows, err := d.pool.Query(context.Background(), query)
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

func (d DatastorePostgres) QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error) {
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

	rows, err := d.pool.Query(context.Background(), queryStr, args...)
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
	res := stats.Stats{}

	// Count distinct clients
	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", CommandsTable)
	row := d.pool.QueryRow(context.Background(), clientsQuery)
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
	rows, err := d.pool.Query(context.Background(), commandsQuery, pkgmodel.CommandSync)
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
	rows, err = d.pool.Query(context.Background(), statusQuery, pkgmodel.CommandSync)
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
	row = d.pool.QueryRow(context.Background(), stacksQuery, resource_update.OperationDelete)
	if err := row.Scan(&res.Stacks); err != nil {
		return nil, err
	}

	// Count managed resources
	resourcesQuery := fmt.Sprintf(`
	SELECT COUNT(*)
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
	row = d.pool.QueryRow(context.Background(), resourcesQuery, resource_update.OperationDelete)
	if err := row.Scan(&res.ManagedResources); err != nil {
		return nil, err
	}

	// Count unmanaged resources
	unmanagedResourcesQuery := fmt.Sprintf(`
	SELECT COUNT(*)
	FROM resources r1
	WHERE stack = '%s'
	AND operation != $1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version > r1.version
	)
	`, constants.UnmanagedStack)
	row = d.pool.QueryRow(context.Background(), unmanagedResourcesQuery, resource_update.OperationDelete)
	if err := row.Scan(&res.UnmanagedResources); err != nil {
		return nil, err
	}

	// Count targets
	targetsQuery := `
	SELECT COUNT(*)
	FROM targets t1
	WHERE NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`
	row = d.pool.QueryRow(context.Background(), targetsQuery)
	if err := row.Scan(&res.Targets); err != nil {
		return nil, err
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
	rows, err = d.pool.Query(context.Background(), resourceTypesQuery, resource_update.OperationDelete)
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

	// Count resource errors
	res.ResourceErrors = make(map[string]int)
	errorQuery := fmt.Sprintf(`
	SELECT data
	FROM %s
	WHERE state = $1
	`, CommandsTable)
	rows, err = d.pool.Query(context.Background(), errorQuery, forma_command.CommandStateFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var jsonData string
		if err := rows.Scan(&jsonData); err != nil {
			return nil, err
		}

		var command forma_command.FormaCommand
		if err := json.Unmarshal([]byte(jsonData), &command); err != nil {
			return nil, err
		}

		for _, ru := range command.ResourceUpdates {
			if failureMessage := findFailureMessage(ru.ProgressResult); failureMessage != "" {
				res.ResourceErrors[failureMessage]++
			}
		}
	}

	return &res, nil
}

func (d DatastorePostgres) StoreResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}

	return d.storeResource(resource, jsonData, commandID, string(resource_update.OperationUpdate))
}

func (d DatastorePostgres) storeResource(resource *pkgmodel.Resource, data []byte, commandID string, operation string) (string, error) {
	if resource.Ksuid == "" {
		resource.Ksuid = metautil.NewID()
	}

	// Check if this resource already exists by native_id and type
	query := `SELECT ksuid, data, uri, version, managed FROM resources WHERE native_id = $1 AND type = $2 ORDER BY version DESC LIMIT 1`
	row := d.pool.QueryRow(context.Background(), query, resource.NativeID, resource.Type)

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
			context.Background(),
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
	// before the create operation completes. In this case, we need to delete the unmanaged resource, unless the unmanaged
	// resource is being deleted itself.
	if !managed && operation != string(resource_update.OperationDelete) {
		query = `DELETE FROM resources WHERE ksuid = $1`
		_, err = d.pool.Exec(context.Background(), query, ksuid)
		if err != nil {
			slog.Error("Failed to delete unmanaged resource", "error", err, "resourceURI", resource.URI())
			return "", err
		}
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
		row = d.pool.QueryRow(context.Background(), query, resource.URI())
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
			// Resource data is identical, return existing version ID
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
		context.Background(),
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
	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	query := `
	INSERT INTO targets (label, version, namespace, config, discoverable)
	VALUES ($1, 1, $2, $3, $4)
	`
	_, err = d.pool.Exec(context.Background(), query, target.Label, target.Namespace, cfg, target.Discoverable)
	if err != nil {
		slog.Error("failed to create target", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d DatastorePostgres) UpdateTarget(target *pkgmodel.Target) (string, error) {
	query := `SELECT MAX(version) FROM targets WHERE label = $1`
	row := d.pool.QueryRow(context.Background(), query, target.Label)

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
	_, err = d.pool.Exec(context.Background(), insertQuery, target.Label, newVersion, target.Namespace, cfg, target.Discoverable)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
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
