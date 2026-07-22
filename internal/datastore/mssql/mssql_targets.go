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
	"time"

	"github.com/demula/mksuid/v2"
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
	var incarnationID, healthState string
	var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt sql.NullTime
	var unreachableAccumSeconds int64
	var lastErrorCode sql.NullString
	var reapKind string
	var reapMaxUnreachableSeconds int64
	if err := scan(&label, &version, &namespace, &config, &configSchemaRaw, &discoverable,
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

	health := &pkgmodel.TargetHealth{
		IncarnationID:           incarnationID,
		State:                   healthState,
		UnreachableAccumSeconds: unreachableAccumSeconds,
	}
	if lastSeenAt.Valid {
		t := lastSeenAt.Time
		health.LastSeenAt = &t
	}
	if observedAt.Valid {
		t := observedAt.Time
		health.ObservedAt = &t
	}
	if firstUnreachableAt.Valid {
		t := firstUnreachableAt.Time
		health.FirstUnreachableAt = &t
	}
	if lastSampleAt.Valid {
		t := lastSampleAt.Time
		health.LastSampleAt = &t
	}
	if lastErrorCode.Valid {
		health.LastErrorCode = lastErrorCode.String
	}

	return &pkgmodel.Target{
		Label:        label,
		Namespace:    namespace,
		Config:       json.RawMessage(config),
		ConfigSchema: configSchema,
		Discoverable: discoverable,
		Version:      version,
		Reaping:      pkgmodel.ReapingRawFromColumns(reapKind, reapMaxUnreachableSeconds),
		Health:       health,
	}, nil
}

func marshalConfigSchema(target *pkgmodel.Target) ([]byte, error) {
	if len(target.ConfigSchema.Hints) == 0 {
		return nil, nil
	}
	return json.Marshal(target.ConfigSchema)
}

func (d *DatastoreMSSQL) CreateTarget(target *pkgmodel.Target) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "CreateTarget")
	defer span.End()

	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	configSchemaJSON, err := marshalConfigSchema(target)
	if err != nil {
		return "", err
	}

	incarnationID := mksuid.New().String()

	reapKind, reapMaxUnreachableSeconds, err := pkgmodel.ReapingToColumns(target.Reaping)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO targets (label, version, namespace, config, config_schema, discoverable, target_incarnation_id, health_state, unreachable_accum_seconds, reap_kind, reap_max_unreachable_seconds) VALUES (@p1, 1, @p2, @p3, @p4, @p5, @p6, 'unknown', 0, @p7, @p8)`
	_, err = d.conn.ExecContext(ctx, query, target.Label, target.Namespace, string(cfg), nullableJSON(configSchemaJSON), target.Discoverable, incarnationID, reapKind, reapMaxUnreachableSeconds)
	if err != nil {
		slog.Debug("failed to create target (may be retried as update)", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d *DatastoreMSSQL) UpdateTarget(target *pkgmodel.Target) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateTarget")
	defer span.End()

	// Load the latest row to carry health state forward onto the new version.
	healthQuery := `
		SELECT TOP (1) version, target_incarnation_id, health_state, last_seen_at, observed_at,
		       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code
		FROM targets WHERE label = @p1 ORDER BY version DESC`
	healthRow := d.conn.QueryRowContext(ctx, healthQuery, target.Label)

	var currentVersion int64
	var incarnationID, healthState sql.NullString
	var lastSeenAt, observedAt, firstUnreachableAt, lastSampleAt sql.NullTime
	var unreachableAccumSeconds sql.NullInt64
	var lastErrorCode sql.NullString
	if err := healthRow.Scan(&currentVersion, &incarnationID, &healthState, &lastSeenAt, &observedAt,
		&firstUnreachableAt, &lastSampleAt, &unreachableAccumSeconds, &lastErrorCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("target %s does not exist, cannot update", target.Label)
		}
		return "", err
	}

	newVersion := int(currentVersion) + 1
	cfg, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	configSchemaJSON, err := marshalConfigSchema(target)
	if err != nil {
		return "", err
	}

	nullTime := func(nt sql.NullTime) any {
		if nt.Valid {
			return nt.Time
		}
		return nil
	}
	nullStr := func(ns sql.NullString) any {
		if ns.Valid {
			return ns.String
		}
		return nil
	}

	reapKind, reapMaxUnreachableSeconds, err := pkgmodel.ReapingToColumns(target.Reaping)
	if err != nil {
		return "", err
	}

	// Recovery: a reaped current row is brought back to life on re-declare —
	// fresh incarnation, health reset to 'unknown', accrual and timestamps cleared.
	newIncarnationID := incarnationID.String
	newHealthState := healthState.String
	newLastSeenAt := nullTime(lastSeenAt)
	newObservedAt := nullTime(observedAt)
	newFirstUnreachableAt := nullTime(firstUnreachableAt)
	newLastSampleAt := nullTime(lastSampleAt)
	newUnreachableAccumSeconds := unreachableAccumSeconds.Int64
	newLastErrorCode := nullStr(lastErrorCode)
	if healthState.String == pkgmodel.TargetHealthStateReaped {
		newIncarnationID = mksuid.New().String()
		newHealthState = pkgmodel.TargetHealthStateUnknown
		newLastSeenAt = nil
		newObservedAt = nil
		newFirstUnreachableAt = nil
		newLastSampleAt = nil
		newUnreachableAccumSeconds = 0
		newLastErrorCode = nil
	}

	query := `
		INSERT INTO targets (label, version, namespace, config, config_schema, discoverable,
		                     target_incarnation_id, health_state, last_seen_at, observed_at,
		                     first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
		                     reap_kind, reap_max_unreachable_seconds)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13, @p14, @p15, @p16)`
	_, err = d.conn.ExecContext(ctx, query,
		target.Label, newVersion, target.Namespace, string(cfg), nullableJSON(configSchemaJSON), target.Discoverable,
		newIncarnationID, newHealthState,
		newLastSeenAt, newObservedAt, newFirstUnreachableAt, newLastSampleAt,
		newUnreachableAccumSeconds, newLastErrorCode, reapKind, reapMaxUnreachableSeconds)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

func (d *DatastoreMSSQL) UpdateTargetHealth(obs pkgmodel.TargetHealthObservation) (bool, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateTargetHealth")
	defer span.End()

	observedAt := obs.ObservedAt.UTC()

	var lastSeenAt any
	if obs.LastSeenAt != nil {
		lastSeenAt = obs.LastSeenAt.UTC()
	}

	var lastErrorCode any
	if obs.LastErrorCode != "" {
		lastErrorCode = obs.LastErrorCode
	}

	// MSSQL uses named parameters (@p1, @p2, …).
	// The monotonic guard uses a string comparison for observed_at since MSSQL stores it
	// as datetime2. observed_at < @p_observed_at handles the NULL case via COALESCE in WHERE.
	// A reachable ("success") observation clears any accrued unreachability.
	accrualReset := ""
	if obs.State == pkgmodel.TargetHealthStateReachable {
		accrualReset = `,
				first_unreachable_at = NULL,
				unreachable_accum_seconds = 0`
	}

	var result sql.Result
	var err error
	if obs.IncarnationID != "" {
		query := fmt.Sprintf(`
			UPDATE targets SET
				health_state = @p1,
				observed_at = @p2,
				last_seen_at = COALESCE(@p3, last_seen_at),
				last_error_code = @p4%s
			WHERE label = @p5
			  AND version = (SELECT MAX(version) FROM targets WHERE label = @p5)
			  AND health_state <> 'reaped'
			  AND (observed_at IS NULL OR observed_at < @p2)
			  AND target_incarnation_id = @p6`, accrualReset)
		result, err = d.conn.ExecContext(ctx, query, obs.State, observedAt, lastSeenAt, lastErrorCode, obs.TargetLabel, obs.IncarnationID)
	} else {
		query := fmt.Sprintf(`
			UPDATE targets SET
				health_state = @p1,
				observed_at = @p2,
				last_seen_at = COALESCE(@p3, last_seen_at),
				last_error_code = @p4%s
			WHERE label = @p5
			  AND version = (SELECT MAX(version) FROM targets WHERE label = @p5)
			  AND health_state <> 'reaped'
			  AND (observed_at IS NULL OR observed_at < @p2)`, accrualReset)
		result, err = d.conn.ExecContext(ctx, query, obs.State, observedAt, lastSeenAt, lastErrorCode, obs.TargetLabel)
	}
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (d *DatastoreMSSQL) AdvanceTargetAccrual(targetLabel, incarnationID string, lastSampleAt time.Time, deltaSeconds int64) (bool, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "AdvanceTargetAccrual")
	defer span.End()

	query := `
		UPDATE targets SET
			unreachable_accum_seconds = unreachable_accum_seconds + @p1,
			last_sample_at = @p2
		WHERE label = @p3
		  AND version = (SELECT MAX(version) FROM targets WHERE label = @p3)
		  AND health_state = 'unreachable'
		  AND target_incarnation_id = @p4`

	result, err := d.conn.ExecContext(ctx, query, deltaSeconds, lastSampleAt.UTC(), targetLabel, incarnationID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (d *DatastoreMSSQL) GetUnreachableTargets() ([]*pkgmodel.Target, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetUnreachableTargets")
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
		AND t1.health_state = 'unreachable'`
	rows, err := d.conn.QueryContext(ctx, query)
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

func (d *DatastoreMSSQL) LoadTarget(label string) (*pkgmodel.Target, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadTarget")
	defer span.End()

	query := `SELECT TOP (1) label, version, namespace, config, config_schema, discoverable, target_incarnation_id, health_state, last_seen_at, observed_at, first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code, reap_kind, reap_max_unreachable_seconds FROM targets WHERE label = @p1 ORDER BY version DESC`
	row := d.conn.QueryRowContext(ctx, query, label)

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
	ctx, span := mssqlTracer.Start(context.Background(), "LoadAllTargets")
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
		)`
	rows, err := d.conn.QueryContext(ctx, query)
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
	ctx, span := mssqlTracer.Start(context.Background(), "LoadTargetsByLabels")
	defer span.End()

	if len(targetNames) == 0 {
		return []*pkgmodel.Target{}, nil
	}

	args := make([]any, len(targetNames))
	for i, name := range targetNames {
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
		)`, placeholders(1, len(targetNames)))

	rows, err := d.conn.QueryContext(ctx, query, args...)
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
	ctx, span := mssqlTracer.Start(context.Background(), "LoadDiscoverableTargets")
	defer span.End()

	// Latest version per label, then dedupe by config via ROW_NUMBER()
	// (MSSQL lacks postgres's DISTINCT ON).
	query := `
		WITH latest_targets AS (
			SELECT label, version, namespace, config, config_schema, discoverable,
			       target_incarnation_id, health_state, last_seen_at, observed_at,
			       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
			       reap_kind, reap_max_unreachable_seconds
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
			       target_incarnation_id, health_state, last_seen_at, observed_at,
			       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
			       reap_kind, reap_max_unreachable_seconds,
				ROW_NUMBER() OVER (PARTITION BY config ORDER BY version DESC) AS rn
			FROM latest_targets
		)
		SELECT label, version, namespace, config, config_schema, discoverable,
		       target_incarnation_id, health_state, last_seen_at, observed_at,
		       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
		       reap_kind, reap_max_unreachable_seconds
		FROM ranked
		WHERE rn = 1`

	rows, err := d.conn.QueryContext(ctx, query)
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
	ctx, span := mssqlTracer.Start(context.Background(), "QueryTargets")
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

	queryStr = extendMSSQLQueryString(queryStr, query.Label, " AND label %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Namespace, " AND namespace %s @p%d{esc}", &args)
	queryStr = extendMSSQLQueryString(queryStr, query.Discoverable, " AND discoverable %s @p%d", &args)
	queryStr += " ORDER BY label"

	rows, err := d.conn.QueryContext(ctx, queryStr, args...)
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
	ctx, span := mssqlTracer.Start(context.Background(), "DeleteTarget")
	defer span.End()

	result, err := d.conn.ExecContext(ctx, "DELETE FROM targets WHERE label = @p1", targetLabel)
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
	ctx, span := mssqlTracer.Start(context.Background(), "CountResourcesInTarget")
	defer span.End()

	query := fmt.Sprintf(`
		SELECT COUNT(*) FROM resources r1
		WHERE target = @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version %[1]s > r1.version %[1]s
		)
		AND operation != @p2`, binColl)
	row := d.conn.QueryRowContext(ctx, query, targetLabel, resource_update.OperationDelete)

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
