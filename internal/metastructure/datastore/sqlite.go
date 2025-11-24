// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/demula/mksuid/v2"
	_ "github.com/mattn/go-sqlite3"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

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

	conn, err := sql.Open("sqlite3", cfg.Sqlite.FilePath)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		return nil, err
	}

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
	jsonData, err := json.Marshal(fa)
	if err != nil {
		return err
	}

	for _, r := range fa.ResourceUpdates {
		if r.Resource.Properties == nil {
			slog.Debug("Resource properties are empty for resource", "resourceLabel", r.Resource.Label, "commandID", commandID)
		}
	}

	query := fmt.Sprintf("INSERT OR REPLACE INTO %s (command_id, timestamp, command, state, agent_version, client_id, agent_id, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", CommandsTable)
	_, err = d.conn.Exec(query, commandID, fa.StartTs, fa.Command, fa.State, formae.Version, fa.ClientID, d.agentID, jsonData)
	if err != nil {

		slog.Error("Query", "query", query, "error", err)

		return err
	}

	return nil
}

func (d DatastoreSQLite) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	rows, err := d.conn.Query(fmt.Sprintf("SELECT data FROM %s", CommandsTable))
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

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

func (d DatastoreSQLite) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	query := fmt.Sprintf("SELECT data FROM %s WHERE command != 'sync' AND state = ?", CommandsTable)
	rows, err := d.conn.Query(query, forma_command.CommandStateInProgress)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

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

func (d DatastoreSQLite) DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE command_id = ?", CommandsTable)
	_, err := d.conn.Exec(query, commandID)
	return err
}

func (d DatastoreSQLite) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	query := fmt.Sprintf("SELECT data FROM %s WHERE command_id = ?", CommandsTable)
	rows, err := d.conn.Query(query, commandID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var jsonData string
	if !rows.Next() {
		return nil, fmt.Errorf("forma command not found: %v", commandID)
	}

	if err := rows.Scan(&jsonData); err != nil {
		return nil, err
	}

	var app forma_command.FormaCommand
	if err := json.Unmarshal([]byte(jsonData), &app); err != nil {
		return nil, err
	}

	return &app, nil
}

func (d DatastoreSQLite) GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error) {
	query := fmt.Sprintf("SELECT data FROM %s WHERE client_id = ? ORDER BY timestamp DESC LIMIT 1", CommandsTable)
	rows, err := d.conn.Query(query, clientID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	if !rows.Next() {
		return nil, fmt.Errorf("no forma commands found for client: %v", clientID)
	}

	var jsonData string
	if err := rows.Scan(&jsonData); err != nil {
		return nil, err
	}

	var command forma_command.FormaCommand
	if err := json.Unmarshal([]byte(jsonData), &command); err != nil {
		return nil, err
	}

	return &command, nil
}

func (d DatastoreSQLite) GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error) {
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
      json_extract(fc.data, '$.Config.Mode') = 'reconcile'
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
	queryStr := fmt.Sprintf("SELECT data FROM %s WHERE 1=1", CommandsTable)
	args := []any{}

	queryStr = extendSQLiteQueryString(queryStr, query.CommandID, " AND command_id %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.ClientID, " AND client_id %s ?", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Command, " AND LOWER(command) %s LOWER(?)", &args)
	if query.Command == nil {
		queryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}

	queryStr = extendSQLiteQueryString(queryStr, query.Stack, " AND EXISTS (SELECT 1 FROM json_each(json_extract(data, '$.ResourceUpdates')) WHERE json_extract(value, '$.Resource.Stack') %s ?)", &args)
	queryStr = extendSQLiteQueryString(queryStr, query.Status, " AND LOWER(state) %s LOWER(?)", &args)

	queryStr += " ORDER BY timestamp DESC"
	if query.N > 0 {
		queryStr += " LIMIT ?"
		args = append(args, min(DefaultFormaCommandsQueryLimit, query.N))
	} else {
		queryStr += fmt.Sprintf(" LIMIT %d", DefaultFormaCommandsQueryLimit)
	}

	rows, err := d.conn.Query(queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

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

func (d DatastoreSQLite) QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error) {
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
	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}

	return d.storeResource(resource, jsonData, commandID, string(resource_update.OperationUpdate))
}

func (d DatastoreSQLite) DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	return d.storeResource(resource, []byte("{}"), commandID, string(resource_update.OperationDelete))
}

func (d DatastoreSQLite) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
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

func (d DatastoreSQLite) StoreStack(stack *pkgmodel.Forma, commandID string) (string, error) {
	var ret string
	var err error
	for _, resource := range stack.Resources {
		if ret, err = d.StoreResource(&resource, commandID); err != nil {
			slog.Error("Failed to store resource in stack", "error", err, "resourceURI", resource.URI())
			return "", err
		}
	}
	return ret, nil
}

func (d DatastoreSQLite) LoadAllStacks() ([]*pkgmodel.Forma, error) {
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

func (d DatastoreSQLite) LoadStack(stackLabel string) (*pkgmodel.Forma, error) {
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

func (d DatastoreSQLite) CreateTarget(target *pkgmodel.Target) (string, error) {
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

func (d DatastoreSQLite) LoadTarget(label string) (*pkgmodel.Target, error) {
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

	resourcesQuery := fmt.Sprintf(`
		SELECT COUNT(*)
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
	row = d.conn.QueryRow(resourcesQuery, resource_update.OperationDelete)

	var resourceCount int
	if err = row.Scan(&resourceCount); err != nil {
		return nil, err
	}

	res.ManagedResources = resourceCount

	unmanagedResourcesQuery := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM resources r1
		WHERE stack = '%s'
		AND operation != ?
		AND NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)`, constants.UnmanagedStack)
	row = d.conn.QueryRow(unmanagedResourcesQuery, resource_update.OperationDelete)

	var unmanagedResourceCount int
	if err = row.Scan(&unmanagedResourceCount); err != nil {
		return nil, err
	}

	res.UnmanagedResources = unmanagedResourceCount

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
	row = d.conn.QueryRow(targetsQuery)

	var targetCount int
	if err := row.Scan(&targetCount); err != nil {
		return nil, err
	}

	res.Targets = targetCount

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
	rows, err = d.conn.Query(fmt.Sprintf("SELECT data FROM %s where state == ?", CommandsTable), forma_command.CommandStateFailed)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

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
				if _, exists := res.ResourceErrors[failureMessage]; !exists {
					res.ResourceErrors[failureMessage] = 0
				}

				res.ResourceErrors[failureMessage]++
			}
		}
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func (d DatastoreSQLite) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
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

func (d DatastoreSQLite) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
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
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
			AND r2.version > r1.version
		)`, strings.Join(placeholders, ","))

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

func (d DatastoreSQLite) CleanUp() error {
	// No cleanup needed for SQLite, this is only used in the Postgres integration tests
	return nil
}
