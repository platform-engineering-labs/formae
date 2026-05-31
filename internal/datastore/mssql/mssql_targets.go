// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	json "github.com/goccy/go-json"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Targets are versioned by an integer `version`: create writes v1, update
// writes MAX(version)+1, reads pick the latest per label via NOT EXISTS.

func scanTargetColumns(scan func(dest ...any) error) (*pkgmodel.Target, error) {
	var label, namespace string
	var version int
	// nvarchar(max) → scan as []byte; the driver can't target json.RawMessage.
	var config, configSchemaRaw []byte
	var discoverable bool
	if err := scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable); err != nil {
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
		Config:       json.RawMessage(config),
		ConfigSchema: configSchema,
		Discoverable: discoverable,
		Version:      version,
	}, nil
}

func marshalConfigSchema(target *pkgmodel.Target) ([]byte, error) {
	if len(target.ConfigSchema.Hints) == 0 {
		return nil, nil
	}
	return json.Marshal(target.ConfigSchema)
}

func (d *DatastoreMSSQL) CreateTarget(target *pkgmodel.Target) (string, error) {
	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	configSchemaJSON, err := marshalConfigSchema(target)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO targets (label, version, namespace, config, config_schema, discoverable) VALUES (@p1, 1, @p2, @p3, @p4, @p5)`
	_, err = d.conn.ExecContext(d.ctx, query, target.Label, target.Namespace, string(cfg), nullableJSON(configSchemaJSON), target.Discoverable)
	if err != nil {
		slog.Debug("failed to create target (may be retried as update)", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d *DatastoreMSSQL) UpdateTarget(target *pkgmodel.Target) (string, error) {
	row := d.conn.QueryRowContext(d.ctx, "SELECT MAX(version) FROM targets WHERE label = @p1", target.Label)

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

	configSchemaJSON, err := marshalConfigSchema(target)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO targets (label, version, namespace, config, config_schema, discoverable) VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`
	_, err = d.conn.ExecContext(d.ctx, query, target.Label, newVersion, target.Namespace, string(cfg), nullableJSON(configSchemaJSON), target.Discoverable)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

func (d *DatastoreMSSQL) LoadTarget(label string) (*pkgmodel.Target, error) {
	query := `SELECT TOP (1) label, version, namespace, config, config_schema, discoverable FROM targets WHERE label = @p1 ORDER BY version DESC`
	row := d.conn.QueryRowContext(d.ctx, query, label)

	target, err := scanTargetColumns(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return target, nil
}

func (d *DatastoreMSSQL) LoadAllTargets() ([]*pkgmodel.Target, error) {
	query := `
		SELECT label, version, namespace, config, config_schema, discoverable
		FROM targets t1
		WHERE NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)`
	rows, err := d.conn.QueryContext(d.ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var targets []*pkgmodel.Target
	for rows.Next() {
		target, err := scanTargetColumns(rows.Scan)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (d *DatastoreMSSQL) LoadTargetsByLabels(targetNames []string) ([]*pkgmodel.Target, error) {
	if len(targetNames) == 0 {
		return []*pkgmodel.Target{}, nil
	}

	args := make([]any, len(targetNames))
	for i, name := range targetNames {
		args[i] = name
	}

	query := fmt.Sprintf(`
		SELECT t1.label, t1.version, t1.namespace, t1.config, t1.config_schema, t1.discoverable
		FROM targets t1
		WHERE t1.label IN (%s)
		AND NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)`, placeholders(1, len(targetNames)))

	rows, err := d.conn.QueryContext(d.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var targets []*pkgmodel.Target
	for rows.Next() {
		target, err := scanTargetColumns(rows.Scan)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (d *DatastoreMSSQL) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	// Latest version per label, then dedupe by config via ROW_NUMBER()
	// (MSSQL lacks postgres's DISTINCT ON).
	query := `
		WITH latest_targets AS (
			SELECT label, version, namespace, config, config_schema, discoverable
			FROM targets t1
			WHERE discoverable = 1
			AND NOT EXISTS (
				SELECT 1
				FROM targets t2
				WHERE t1.label = t2.label
				AND t2.version > t1.version
			)
		),
		ranked AS (
			SELECT label, version, namespace, config, config_schema, discoverable,
				ROW_NUMBER() OVER (PARTITION BY config ORDER BY version DESC) AS rn
			FROM latest_targets
		)
		SELECT label, version, namespace, config, config_schema, discoverable
		FROM ranked
		WHERE rn = 1`

	rows, err := d.conn.QueryContext(d.ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var targets []*pkgmodel.Target
	for rows.Next() {
		target, err := scanTargetColumns(rows.Scan)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (d *DatastoreMSSQL) QueryTargets(query *datastore.TargetQuery) ([]*pkgmodel.Target, error) {
	queryStr := `
		SELECT label, version, namespace, config, config_schema, discoverable
		FROM targets t1
		WHERE NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)`
	args := []any{}

	queryStr = extendMSSQLQueryString(queryStr, query.Label, " AND label %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Namespace, " AND namespace %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Discoverable, " AND discoverable %s @p%d", &args)
	queryStr += " ORDER BY label"

	rows, err := d.conn.QueryContext(d.ctx, queryStr, args...)
	if err != nil {
		slog.Error("QueryTargets failed", "error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var targets []*pkgmodel.Target
	for rows.Next() {
		target, err := scanTargetColumns(rows.Scan)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (d *DatastoreMSSQL) DeleteTarget(targetLabel string) (string, error) {
	result, err := d.conn.ExecContext(d.ctx, "DELETE FROM targets WHERE label = @p1", targetLabel)
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

func (d *DatastoreMSSQL) CountResourcesInTarget(targetLabel string) (int, error) {
	query := fmt.Sprintf(`
		SELECT COUNT(*) FROM resources r1
		WHERE target = @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version %[1]s > r1.version %[1]s
		)
		AND operation != @p2`, binColl)
	row := d.conn.QueryRowContext(d.ctx, query, targetLabel, resource_update.OperationDelete)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// nullableJSON returns nil for an empty blob (write SQL NULL) and the JSON as
// a string otherwise, so we avoid storing a literal "null".
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
