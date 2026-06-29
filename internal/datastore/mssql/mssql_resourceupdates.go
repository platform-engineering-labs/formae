// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	json "github.com/goccy/go-json"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func (d *DatastoreMSSQL) Stats() (*stats.Stats, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "Stats")
	defer span.End()

	res := stats.Stats{}

	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", datastore.CommandsTable)
	if err := d.conn.QueryRowContext(ctx, clientsQuery).Scan(&res.Clients); err != nil {
		return nil, err
	}

	res.Commands = make(map[string]int)
	if err := d.scanCountMap(ctx,
		fmt.Sprintf("SELECT command, COUNT(*) FROM %s WHERE command != @p1 GROUP BY command", datastore.CommandsTable),
		res.Commands, string(pkgmodel.CommandSync),
	); err != nil {
		return nil, err
	}

	res.States = make(map[string]int)
	if err := d.scanCountMap(ctx,
		fmt.Sprintf("SELECT state, COUNT(*) FROM %s WHERE command != @p1 GROUP BY state", datastore.CommandsTable),
		res.States, string(pkgmodel.CommandSync),
	); err != nil {
		return nil, err
	}

	stacksQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT stack)
		FROM resources r1
		WHERE stack IS NOT NULL
		AND stack != '%s'
		AND operation != @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE Latin1_General_BIN2 > r1.version COLLATE Latin1_General_BIN2
		)`, constants.UnmanagedStack)
	if err := d.conn.QueryRowContext(ctx, stacksQuery, string(types.OperationDelete)).Scan(&res.Stacks); err != nil {
		return nil, err
	}

	res.ManagedResources = make(map[string]int)
	if err := d.scanCountMap(ctx, fmt.Sprintf(`
		SELECT SUBSTRING(type, 1, CHARINDEX('::', type) - 1) AS namespace, COUNT(*)
		FROM resources r1
		WHERE stack IS NOT NULL
		AND stack != '%s'
		AND operation != @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE Latin1_General_BIN2 > r1.version COLLATE Latin1_General_BIN2
		)
		GROUP BY SUBSTRING(type, 1, CHARINDEX('::', type) - 1)`, constants.UnmanagedStack),
		res.ManagedResources, string(types.OperationDelete),
	); err != nil {
		return nil, err
	}

	res.UnmanagedResources = make(map[string]int)
	if err := d.scanCountMap(ctx, fmt.Sprintf(`
		SELECT SUBSTRING(type, 1, CHARINDEX('::', type) - 1) AS namespace, COUNT(*)
		FROM resources r1
		WHERE stack = '%s'
		AND operation != @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE Latin1_General_BIN2 > r1.version COLLATE Latin1_General_BIN2
		)
		GROUP BY SUBSTRING(type, 1, CHARINDEX('::', type) - 1)`, constants.UnmanagedStack),
		res.UnmanagedResources, string(types.OperationDelete),
	); err != nil {
		return nil, err
	}

	res.Targets = make(map[string]int)
	if err := d.scanCountMap(ctx, `
		SELECT namespace, COUNT(*)
		FROM targets t1
		WHERE NOT EXISTS (
			SELECT 1 FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)
		GROUP BY namespace`, res.Targets,
	); err != nil {
		return nil, err
	}

	res.ResourceTypes = make(map[string]int)
	if err := d.scanCountMap(ctx, `
		SELECT type, COUNT(*)
		FROM resources r1
		WHERE operation != @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE Latin1_General_BIN2 > r1.version COLLATE Latin1_General_BIN2
		)
		GROUP BY type`, res.ResourceTypes, string(types.OperationDelete),
	); err != nil {
		return nil, err
	}

	res.ResourceErrors = make(map[string]int)
	if err := d.scanCountMap(ctx, `
		SELECT JSON_VALUE(resource, '$.Type') AS resource_type, COUNT(*)
		FROM resource_updates
		WHERE state = @p1
		AND resource IS NOT NULL
		GROUP BY JSON_VALUE(resource, '$.Type')`, res.ResourceErrors, string(types.ResourceUpdateStateFailed),
	); err != nil {
		return nil, err
	}
	delete(res.ResourceErrors, "")

	return &res, nil
}

// scanCountMap runs a "SELECT k, COUNT(*) ... GROUP BY k" and fills dst.
// NULL keys scan into "" (callers strip them when needed).
func (d *DatastoreMSSQL) scanCountMap(ctx context.Context, query string, dst map[string]int, args ...any) error {
	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key sql.NullString
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		dst[key.String] = count
	}
	return rows.Err()
}

// GetKSUIDByTriplet returns the ksuid of the latest non-deleted resource
// matching (stack, label, type).
func (d *DatastoreMSSQL) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetKSUIDByTriplet")
	defer span.End()

	query := `
		SELECT TOP (1) ksuid
		FROM resources
		WHERE stack = @p1 AND label = @p2 AND LOWER(type) = LOWER(@p3)
		AND operation != @p4
		ORDER BY version COLLATE Latin1_General_BIN2 DESC`
	var ksuid string
	err := d.conn.QueryRowContext(ctx, query, stack, label, resourceType, string(types.OperationDelete)).Scan(&ksuid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ksuid, nil
}

// BatchGetKSUIDsByTriplets maps each (stack,label,type) triplet to the ksuid
// of its latest non-deleted resource. MSSQL has no row-value IN form, so the
// WHERE is built as OR-ed (stack=? AND label=? AND type=?) groups.
func (d *DatastoreMSSQL) BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "BatchGetKSUIDsByTriplets")
	defer span.End()

	if len(triplets) == 0 {
		return make(map[pkgmodel.TripletKey]string), nil
	}

	args := make([]any, 0, len(triplets)*3+1)
	conds := ""
	for i, t := range triplets {
		if i > 0 {
			conds += " OR "
		}
		base := i*3 + 1
		conds += fmt.Sprintf("(r1.stack = @p%d AND r1.label = @p%d AND r1.type = @p%d)", base, base+1, base+2)
		args = append(args, t.Stack, t.Label, t.Type)
	}
	opOrdinal := len(args) + 1
	args = append(args, string(types.OperationDelete))

	query := fmt.Sprintf(`
		SELECT stack, label, type, ksuid
		FROM resources r1
		WHERE (%s)
		AND r1.operation != @p%d
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
			AND r2.version COLLATE Latin1_General_BIN2 > r1.version COLLATE Latin1_General_BIN2
		)`, conds, opOrdinal)

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[pkgmodel.TripletKey]string)
	for rows.Next() {
		var stack, label, resourceType, ksuid string
		if err := rows.Scan(&stack, &label, &resourceType, &ksuid); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result[pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}] = ksuid
	}
	return result, rows.Err()
}

// BatchGetTripletsByKSUIDs maps each ksuid to the (stack,label,type) of its
// latest non-deleted row (managed wins ties).
func (d *DatastoreMSSQL) BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "BatchGetTripletsByKSUIDs")
	defer span.End()

	if len(ksuids) == 0 {
		return make(map[string]pkgmodel.TripletKey), nil
	}

	args := make([]any, 0, len(ksuids)+1)
	for _, k := range ksuids {
		args = append(args, k)
	}
	opOrdinal := len(args) + 1
	args = append(args, string(types.OperationDelete))

	query := fmt.Sprintf(`
		WITH latest_resources AS (
			SELECT ksuid, stack, label, type, managed, version,
				ROW_NUMBER() OVER (PARTITION BY ksuid ORDER BY managed DESC, version COLLATE Latin1_General_BIN2 DESC) AS rn
			FROM resources
			WHERE ksuid IN (%s)
			AND operation != @p%d
		)
		SELECT ksuid, stack, label, type
		FROM latest_resources
		WHERE rn = 1`, placeholders(1, len(ksuids)), opOrdinal)

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

// LoadResourceUpdates reads the resource_updates rows for a command.
// Column order mirrors BulkStoreResourceUpdates.
func (d *DatastoreMSSQL) LoadResourceUpdates(commandID string) ([]resource_update.ResourceUpdate, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "LoadResourceUpdates")
	defer span.End()

	query := `
		SELECT ksuid, operation, state, start_ts, modified_ts,
			retries, remaining, version, stack_label, group_id, source,
			resource, resource_target, existing_resource, existing_target,
			progress_result, most_recent_progress,
			remaining_resolvables, reference_labels, previous_properties,
			is_cascade, cascade_source
		FROM resource_updates
		WHERE command_id = @p1
		ORDER BY ksuid ASC`

	rows, err := d.conn.QueryContext(ctx, query, commandID)
	if err != nil {
		return nil, fmt.Errorf("failed to query resource updates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var updates []resource_update.ResourceUpdate
	for rows.Next() {
		var ru resource_update.ResourceUpdate
		var ksuid, operation, state string
		var startTs, modifiedTs *time.Time
		var retries, remaining *int64
		var stackLabel, groupID, source, version *string
		var resourceJSON, resourceTargetJSON, existingResourceJSON, existingTargetJSON []byte
		var progressResultJSON, mostRecentProgressJSON []byte
		var remainingResolvablesJSON, referenceLabelsJSON, previousPropertiesJSON []byte
		var ruIsCascade *bool
		var ruCascadeSource *string

		err := rows.Scan(
			&ksuid, &operation, &state, &startTs, &modifiedTs,
			&retries, &remaining, &version, &stackLabel, &groupID, &source,
			&resourceJSON, &resourceTargetJSON, &existingResourceJSON, &existingTargetJSON,
			&progressResultJSON, &mostRecentProgressJSON,
			&remainingResolvablesJSON, &referenceLabelsJSON, &previousPropertiesJSON,
			&ruIsCascade, &ruCascadeSource,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan resource update: %w", err)
		}

		ru.Operation = types.OperationType(operation)
		ru.State = resource_update.ResourceUpdateState(state)
		if startTs != nil {
			ru.StartTs = startTs.UTC()
		}
		if modifiedTs != nil {
			ru.ModifiedTs = modifiedTs.UTC()
		}
		if retries != nil {
			ru.Retries = uint16(*retries)
		}
		if remaining != nil {
			ru.Remaining = int16(*remaining)
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

		if len(resourceJSON) > 0 {
			if err := json.Unmarshal(resourceJSON, &ru.DesiredState); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %w", err)
			}
		}
		ru.DesiredState.Ksuid = ksuid
		if len(resourceTargetJSON) > 0 {
			if err := json.Unmarshal(resourceTargetJSON, &ru.ResourceTarget); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource target: %w", err)
			}
		}
		if len(existingResourceJSON) > 0 {
			if err := json.Unmarshal(existingResourceJSON, &ru.PriorState); err != nil {
				return nil, fmt.Errorf("failed to unmarshal existing resource: %w", err)
			}
		}
		if len(existingTargetJSON) > 0 {
			if err := json.Unmarshal(existingTargetJSON, &ru.ExistingTarget); err != nil {
				return nil, fmt.Errorf("failed to unmarshal existing target: %w", err)
			}
		}
		if len(progressResultJSON) > 0 {
			if err := json.Unmarshal(progressResultJSON, &ru.ProgressResult); err != nil {
				return nil, fmt.Errorf("failed to unmarshal progress result: %w", err)
			}
		}
		if len(mostRecentProgressJSON) > 0 {
			if err := json.Unmarshal(mostRecentProgressJSON, &ru.MostRecentProgressResult); err != nil {
				return nil, fmt.Errorf("failed to unmarshal most recent progress: %w", err)
			}
		}
		if len(remainingResolvablesJSON) > 0 {
			if err := json.Unmarshal(remainingResolvablesJSON, &ru.RemainingResolvables); err != nil {
				return nil, fmt.Errorf("failed to unmarshal remaining resolvables: %w", err)
			}
		}
		if len(referenceLabelsJSON) > 0 {
			if err := json.Unmarshal(referenceLabelsJSON, &ru.ReferenceLabels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal reference labels: %w", err)
			}
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

func (d *DatastoreMSSQL) UpdateResourceUpdateState(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateResourceUpdateState")
	defer span.End()

	query := `
		UPDATE resource_updates
		SET state = @p1, modified_ts = @p2
		WHERE command_id = @p3 AND ksuid = @p4 AND operation = @p5`

	result, err := d.conn.ExecContext(ctx, query, string(state), modifiedTs.UTC(), commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}
	return nil
}

// UpdateResourceUpdateProgress appends an entry to progress_result, updates
// most_recent_progress, and bumps state.
func (d *DatastoreMSSQL) UpdateResourceUpdateProgress(commandID string, ksuid string, operation types.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time, progress plugin.TrackedProgress) error {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateResourceUpdateProgress")
	defer span.End()

	var existingProgressJSON []byte
	selectQuery := `SELECT progress_result FROM resource_updates WHERE command_id = @p1 AND ksuid = @p2 AND operation = @p3`
	if err := d.conn.QueryRowContext(ctx, selectQuery, commandID, ksuid, string(operation)).Scan(&existingProgressJSON); err != nil {
		return fmt.Errorf("failed to load existing progress: %w", err)
	}

	var existingProgress []plugin.TrackedProgress
	if len(existingProgressJSON) > 0 {
		if err := json.Unmarshal(existingProgressJSON, &existingProgress); err != nil {
			return fmt.Errorf("failed to unmarshal existing progress: %w", err)
		}
	}
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
		SET state = @p1, modified_ts = @p2, progress_result = @p3, most_recent_progress = @p4
		WHERE command_id = @p5 AND ksuid = @p6 AND operation = @p7`

	result, err := d.conn.ExecContext(ctx, updateQuery,
		string(state), modifiedTs.UTC(), string(progressJSON), string(mostRecentJSON),
		commandID, ksuid, string(operation))
	if err != nil {
		return fmt.Errorf("failed to update resource update progress: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}
	return nil
}

// BatchUpdateResourceUpdateState updates many ResourceUpdate rows to the
// same state in one transaction.
func (d *DatastoreMSSQL) BatchUpdateResourceUpdateState(commandID string, refs []datastore.ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	ctx, span := mssqlTracer.Start(context.Background(), "BatchUpdateResourceUpdateState")
	defer span.End()

	if len(refs) == 0 {
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

	const query = `
		UPDATE resource_updates
		SET state = @p1, modified_ts = @p2
		WHERE command_id = @p3 AND ksuid = @p4 AND operation = @p5`

	for _, ref := range refs {
		if _, err = tx.ExecContext(ctx, query, string(state), modifiedTs.UTC(), commandID, ref.KSUID, string(ref.Operation)); err != nil {
			return fmt.Errorf("failed to update resource update: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true
	return nil
}

// ForceCancelResourceUpdates CAS-terminalizes in-flight resource updates to Canceled in one
// transaction. For InProgress rows it also writes force-cancel progress. Returns the rows
// transitioned (split by prior state) and those already terminal (Skipped). Idempotent.
func (d *DatastoreMSSQL) ForceCancelResourceUpdates(commandID string, inProgress []datastore.ForceCancelRow, notStarted []datastore.ResourceUpdateRef, modifiedTs time.Time) (datastore.ForceCancelResult, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "ForceCancelResourceUpdates")
	defer span.End()

	var result datastore.ForceCancelResult

	if len(inProgress) == 0 && len(notStarted) == 0 {
		return result, nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	modifiedTsUTC := modifiedTs.UTC()

	for _, row := range inProgress {
		ref := datastore.ResourceUpdateRef{KSUID: row.KSUID, Operation: row.Operation}
		res, execErr := tx.ExecContext(ctx, `
			UPDATE resource_updates
			SET state = 'Canceled', modified_ts = @p1, progress_result = @p2, most_recent_progress = @p3
			WHERE command_id = @p4 AND ksuid = @p5 AND operation = @p6 AND state = 'InProgress'
		`, modifiedTsUTC, string(row.ProgressJSON), string(row.MostRecentProgressJSON), commandID, row.KSUID, string(row.Operation))
		if execErr != nil {
			return result, fmt.Errorf("failed to force-cancel InProgress row %s: %w", row.KSUID, execErr)
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			result.CanceledInProgress = append(result.CanceledInProgress, ref)
		} else {
			result.Skipped = append(result.Skipped, ref)
		}
	}

	for _, ref := range notStarted {
		res, execErr := tx.ExecContext(ctx, `
			UPDATE resource_updates
			SET state = 'Canceled', modified_ts = @p1
			WHERE command_id = @p2 AND ksuid = @p3 AND operation = @p4 AND state = 'NotStarted'
		`, modifiedTsUTC, commandID, ref.KSUID, string(ref.Operation))
		if execErr != nil {
			return result, fmt.Errorf("failed to force-cancel NotStarted row %s: %w", ref.KSUID, execErr)
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			result.CanceledNotStarted = append(result.CanceledNotStarted, ref)
		} else {
			result.Skipped = append(result.Skipped, ref)
		}
	}

	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true
	return result, nil
}
