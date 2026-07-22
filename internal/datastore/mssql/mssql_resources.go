// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/demula/mksuid/v2"
	json "github.com/goccy/go-json"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// binColl forces byte-order comparison of base62 KSUID versions. MSSQL's
// default collation is case-insensitive — without this, a later lowercase
// delete would sort before an earlier uppercase update and leak deleted rows.
// Equivalent to postgres' COLLATE "C".
const binColl = "COLLATE Latin1_General_BIN2"

var _ datastore.Datastore = (*DatastoreMSSQL)(nil)

func (d *DatastoreMSSQL) QueryResources(query *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "QueryResources")
	defer span.End()

	queryStr := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %[1]s > r1.version %[1]s
	)
	AND r1.operation != @p1 AND r1.operation != 'reaped'
	`, binColl)
	args := []any{string(resource_update.OperationDelete)}

	queryStr = extendMSSQLQueryString(queryStr, query.NativeID, " AND native_id %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Stack, " AND stack %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Type, " AND LOWER(type) %s LOWER(@p%d){esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Label, " AND label %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Target, " AND target %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Managed, " AND managed %s @p%d", &args)
	queryStr += " ORDER BY type, label"

	rows, err := d.conn.QueryContext(ctx, queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

func (d *DatastoreMSSQL) StoreResource(resource *pkgmodel.Resource, commandID string, expectedIncarnation ...string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "StoreResource")
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

func (d *DatastoreMSSQL) DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "DeleteResource")
	defer span.End()

	return d.storeResource(ctx, resource, []byte("{}"), commandID, string(resource_update.OperationDelete), "")
}

// storeResource is the shared upsert path for create/update/delete.
func (d *DatastoreMSSQL) storeResource(ctx context.Context, resource *pkgmodel.Resource, data []byte, commandID, operation, expectedIncarnation string) (string, error) {
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
		guardQuery := fmt.Sprintf(
			"SELECT TOP (1) operation, COALESCE(target_incarnation_id, '') FROM resources WHERE uri = @p1 ORDER BY version %s DESC",
			binColl,
		)
		var curOp, curInc string
		guardErr := d.conn.QueryRowContext(ctx, guardQuery, string(resource.URI())).Scan(&curOp, &curInc)
		if guardErr != nil && !errors.Is(guardErr, sql.ErrNoRows) {
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

	lookup := fmt.Sprintf(
		"SELECT TOP (1) ksuid, data, uri, version, managed FROM resources WHERE native_id = @p1 AND type = @p2 ORDER BY version %s DESC",
		binColl,
	)
	row := d.conn.QueryRowContext(ctx, lookup, resource.NativeID, resource.Type)

	var (
		ksuid        string
		existingData string
		uri          string
		version      string
		managed      bool
	)
	err := row.Scan(&ksuid, &existingData, &uri, &version, &managed)

	if errors.Is(err, sql.ErrNoRows) {
		newVersion := mksuid.New().String()
		if err := d.insertResource(ctx, resource, newVersion, commandID, operation, data, expectedIncarnation); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
	} else if err != nil {
		return "", fmt.Errorf("failed to retrieve resources: %w", err)
	}

	// Adopt the discovered KSUID so references made during discovery stay valid.
	if !managed && operation != string(resource_update.OperationDelete) {
		slog.Debug("Resource discovered before apply completed, adopting discovered KSUID",
			"native_id", resource.NativeID,
			"type", resource.Type,
			"discovered_ksuid", ksuid,
			"original_ksuid", resource.Ksuid)
		resource.Ksuid = ksuid
	}

	var existingResource pkgmodel.Resource
	if err := json.Unmarshal([]byte(existingData), &existingResource); err != nil {
		return "", fmt.Errorf("failed to unmarshal existing resource: %w", err)
	}

	readWriteEqual, readOnlyEqual := datastore.ResourcesAreEqual(resource, &existingResource)

	if operation == string(resource_update.OperationDelete) {
		// Don't write a second tombstone if the latest version is already a delete.
		var latestOperation string
		latestQuery := fmt.Sprintf(
			"SELECT TOP (1) operation FROM resources WHERE uri = @p1 ORDER BY version %s DESC",
			binColl,
		)
		err = d.conn.QueryRowContext(ctx, latestQuery, string(resource.URI())).Scan(&latestOperation)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil
			}
			return "", fmt.Errorf("failed to retrieve latest operation: %w", err)
		}
		if latestOperation == string(resource_update.OperationDelete) {
			return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
		}
	} else if readWriteEqual && readOnlyEqual && resource.Ksuid == ksuid {
		return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
	}

	// Read-only-only changes update in place; read-write changes mint a new version.
	var newVersion string
	if readWriteEqual && !readOnlyEqual {
		newVersion = version
	} else {
		newVersion = mksuid.New().String()
	}

	if err := d.upsertResource(ctx, resource, newVersion, commandID, operation, data, expectedIncarnation); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
}

func (d *DatastoreMSSQL) insertResource(ctx context.Context, resource *pkgmodel.Resource, version, commandID, operation string, data []byte, expectedIncarnation string) error {
	query := `
	INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid, target_incarnation_id)
	VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13)
	`
	_, err := d.conn.ExecContext(ctx, query,
		string(resource.URI()), version, commandID, operation,
		resource.NativeID, resource.Stack, resource.Type, resource.Label,
		resource.Target, string(data), resource.Managed, resource.Ksuid,
		expectedIncarnation,
	)
	if err != nil {
		slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
		return fmt.Errorf("failed to store resource: %w", err)
	}
	return nil
}

// upsertResource is MSSQL's atomic ON CONFLICT (uri, version) DO UPDATE:
// UPDATE WITH (UPDLOCK, SERIALIZABLE); IF @@ROWCOUNT=0 INSERT in a transaction.
func (d *DatastoreMSSQL) upsertResource(ctx context.Context, resource *pkgmodel.Resource, version, commandID, operation string, data []byte, expectedIncarnation string) error {
	args := []any{
		string(resource.URI()), version, commandID, operation,
		resource.NativeID, resource.Stack, resource.Type, resource.Label,
		resource.Target, string(data), resource.Managed, resource.Ksuid,
		expectedIncarnation,
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin upsertResource tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	const updateQuery = `
	UPDATE resources WITH (UPDLOCK, SERIALIZABLE) SET
		command_id = @p3, operation = @p4, native_id = @p5, stack = @p6,
		type = @p7, label = @p8, target = @p9, data = @p10, managed = @p11, ksuid = @p12,
		target_incarnation_id = @p13
	WHERE uri = @p1 AND version = @p2`
	res, err := tx.ExecContext(ctx, updateQuery, args...)
	if err != nil {
		slog.Error("failed to upsert resource (update)", "error", err, "resourceURI", resource.URI())
		return fmt.Errorf("failed to store resource: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		const insertQuery = `
		INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid, target_incarnation_id)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13)`
		if _, err := tx.ExecContext(ctx, insertQuery, args...); err != nil {
			slog.Error("failed to upsert resource (insert)", "error", err, "resourceURI", resource.URI())
			return fmt.Errorf("failed to store resource: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsertResource tx: %w", err)
	}
	committed = true
	return nil
}

func (d *DatastoreMSSQL) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadResource")
	defer span.End()

	query := fmt.Sprintf(`
	SELECT TOP (1) data, ksuid
	FROM resources
	WHERE uri = @p1
	AND operation != @p2 AND operation != 'reaped'
	ORDER BY version %s DESC
	`, binColl)
	row := d.conn.QueryRowContext(ctx, query, string(uri), string(resource_update.OperationDelete))

	var jsonData, ksuid string
	if err := row.Scan(&jsonData, &ksuid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
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

func (d *DatastoreMSSQL) LoadResourceByNativeID(nativeID, resourceType string) (*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadResourceByNativeID")
	defer span.End()

	query := fmt.Sprintf(`
	SELECT TOP (1) data, ksuid
	FROM resources r1
	WHERE native_id = @p1 AND type = @p2
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %[1]s > r1.version %[1]s
	)
	AND r1.operation != @p3 AND r1.operation != 'reaped'
	`, binColl)
	row := d.conn.QueryRowContext(ctx, query, nativeID, resourceType, string(resource_update.OperationDelete))

	var jsonData, ksuid string
	if err := row.Scan(&jsonData, &ksuid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
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

func (d *DatastoreMSSQL) LoadAllResources() ([]*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadAllResources")
	defer span.End()

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %[1]s > r1.version %[1]s
	)
	AND operation != @p1 AND operation != 'reaped'
	`, binColl)
	rows, err := d.conn.QueryContext(ctx, query, string(resource_update.OperationDelete))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

func (d *DatastoreMSSQL) LatestLabelForResource(label string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LatestLabelForResource")
	defer span.End()

	query := `
	SELECT TOP (1) label
	FROM resources
	WHERE label = @p1 OR label LIKE @p2 + '-%'
	ORDER BY LEN(label) DESC, label DESC
	`
	row := d.conn.QueryRowContext(ctx, query, label, label)

	var latestLabel string
	if err := row.Scan(&latestLabel); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return latestLabel, nil
}

func (d *DatastoreMSSQL) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadResourceById")
	defer span.End()

	query := fmt.Sprintf(`
	SELECT TOP (1) data, ksuid
	FROM resources
	WHERE ksuid = @p1
	AND operation != @p2 AND operation != 'reaped'
	ORDER BY version %s DESC
	`, binColl)
	row := d.conn.QueryRowContext(ctx, query, ksuid, string(resource_update.OperationDelete))

	var jsonData, ksuidResult string
	if err := row.Scan(&jsonData, &ksuidResult); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}
	resource.Ksuid = ksuidResult
	return &resource, nil
}

// FindResourcesDependingOn matches `"$ref":"formae://<ksuid>#` over the
// nvarchar(max) data column with LIKE. Full scan (same TODO as postgres/sqlite).
func (d *DatastoreMSSQL) FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "FindResourcesDependingOn")
	defer span.End()

	pattern := fmt.Sprintf("%%\"$ref\":\"formae://%s#%%", ksuid)

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE data LIKE @p1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %[1]s > r1.version %[1]s
	)
	AND operation != @p2 AND operation != 'reaped'
	`, binColl)

	rows, err := d.conn.QueryContext(ctx, query, pattern, string(resource_update.OperationDelete))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

	return resources, rows.Err()
}

func (d *DatastoreMSSQL) FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "FindResourcesDependingOnMany")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string][]*pkgmodel.Resource), nil
	}

	var conditions []string
	var args []any
	for i, ksuid := range ksuids {
		pattern := fmt.Sprintf("%%\"$ref\":\"formae://%s#%%", ksuid)
		conditions = append(conditions, fmt.Sprintf("data LIKE @p%d", i+1))
		args = append(args, pattern)
	}
	deleteArgNum := len(ksuids) + 1
	args = append(args, string(resource_update.OperationDelete))

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %s > r1.version %s
	)
	AND operation != @p%d AND operation != 'reaped'
	`, strings.Join(conditions, " OR "), binColl, binColl, deleteArgNum)

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

		for _, ksuid := range ksuids {
			pattern := fmt.Sprintf("\"$ref\":\"formae://%s#", ksuid)
			if strings.Contains(jsonData, pattern) {
				result[ksuid] = append(result[ksuid], &resource)
			}
		}
	}

	return result, rows.Err()
}

func (d *DatastoreMSSQL) FindTargetsDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Target, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "FindTargetsDependingOnMany")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string][]*pkgmodel.Target), nil
	}

	var conditions []string
	var args []any
	for i, ksuid := range ksuids {
		pattern := fmt.Sprintf("%%\"$ref\":\"formae://%s#%%", ksuid)
		conditions = append(conditions, fmt.Sprintf("config LIKE @p%d", i+1))
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

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]*pkgmodel.Target)
	for rows.Next() {
		target, err := scanTargetColumns(rows.Scan)
		if err != nil {
			return nil, err
		}

		configStr := string(target.Config)
		for _, ksuid := range ksuids {
			pattern := fmt.Sprintf("\"$ref\":\"formae://%s#", ksuid)
			if strings.Contains(configStr, pattern) {
				result[ksuid] = append(result[ksuid], target)
			}
		}
	}

	return result, rows.Err()
}

func (d *DatastoreMSSQL) BulkStoreResources(resources []pkgmodel.Resource, commandID string) (string, error) {
	_, span := mssqlTracer.Start(context.Background(), "BulkStoreResources")
	defer span.End()

	var lastVersionID string
	for i := range resources {
		versionID, err := d.StoreResource(&resources[i], commandID)
		if err != nil {
			slog.Error("failed to store resource", "error", err, "resourceURI", resources[i].URI())
			return "", err
		}
		lastVersionID = versionID
	}
	return lastVersionID, nil
}

func (d *DatastoreMSSQL) LoadResourcesByStack(stackLabel string) ([]*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadResourcesByStack")
	defer span.End()

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE stack = @p1
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %[1]s > r1.version %[1]s
	)
	AND operation != @p2 AND operation != 'reaped'
	`, binColl)

	rows, err := d.conn.QueryContext(ctx, query, stackLabel, string(resource_update.OperationDelete))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

func (d *DatastoreMSSQL) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadAllResourcesByStack")
	defer span.End()

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version %[1]s > r1.version %[1]s
	)
	AND operation != @p1 AND operation != 'reaped'
	`, binColl)

	rows, err := d.conn.QueryContext(ctx, query, string(resource_update.OperationDelete))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	stackResourcesMap := make(map[string][]*pkgmodel.Resource)
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
		if resource.Stack != "" {
			stackResourcesMap[resource.Stack] = append(stackResourcesMap[resource.Stack], &resource)
		}
	}

	return stackResourcesMap, rows.Err()
}
