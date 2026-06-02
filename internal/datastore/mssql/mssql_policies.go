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
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Standalone policies carry a NULL or empty-string stack_id, so every
// "is standalone" predicate matches `stack_id IS NULL OR stack_id = ''`.
// DeletePolicy's tombstone writes a genuine NULL.

func marshalPolicyData(policy pkgmodel.Policy) ([]byte, error) {
	switch p := policy.(type) {
	case *pkgmodel.TTLPolicy:
		return json.Marshal(map[string]any{
			"TTLSeconds":   p.TTLSeconds,
			"OnDependents": p.OnDependents,
		})
	case *pkgmodel.AutoReconcilePolicy:
		return json.Marshal(map[string]any{
			"IntervalSeconds": p.IntervalSeconds,
		})
	default:
		return nil, fmt.Errorf("unsupported policy type: %T", policy)
	}
}

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

func (d *DatastoreMSSQL) CreatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "CreatePolicy")
	defer span.End()

	id := mksuid.New().String()
	version := mksuid.New().String()

	policyData, err := marshalPolicyData(policy)
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy data: %w", err)
	}

	query := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	          VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`
	_, err = d.conn.ExecContext(ctx, query, id, version, commandID, "create",
		policy.GetLabel(), policy.GetType(), policy.GetStackID(), string(policyData))
	if err != nil {
		slog.Error("Failed to create policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d *DatastoreMSSQL) UpdatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdatePolicy")
	defer span.End()

	// Look up the existing policy's id so the new version shares it.
	var id string
	var err error
	if policy.GetStackID() == "" {
		query := `
			SELECT TOP (1) id FROM policies
			WHERE label = @p1 AND (stack_id IS NULL OR stack_id = '')
			ORDER BY version COLLATE Latin1_General_BIN2 DESC`
		err = d.conn.QueryRowContext(ctx, query, policy.GetLabel()).Scan(&id)
	} else {
		query := `
			SELECT TOP (1) id FROM policies
			WHERE label = @p1 AND stack_id = @p2
			ORDER BY version COLLATE Latin1_General_BIN2 DESC`
		err = d.conn.QueryRowContext(ctx, query, policy.GetLabel(), policy.GetStackID()).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("failed to find existing policy: %w", err)
	}

	version := mksuid.New().String()

	policyData, err := marshalPolicyData(policy)
	if err != nil {
		return "", fmt.Errorf("failed to marshal policy data: %w", err)
	}

	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	                VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`
	_, err = d.conn.ExecContext(ctx, insertQuery, id, version, commandID, "update",
		policy.GetLabel(), policy.GetType(), policy.GetStackID(), string(policyData))
	if err != nil {
		slog.Error("Failed to update policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d *DatastoreMSSQL) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetPoliciesForStack")
	defer span.End()

	// Inline policies (stack_id on the policy) ∪ standalone policies attached
	// via the stack_policies junction.
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, stack_id, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
		),
		inline_policies AS (
			SELECT label, policy_type, policy_data, stack_id
			FROM latest_policies
			WHERE stack_id = @p1 AND rn = 1 AND operation != 'delete'
		),
		standalone_policies AS (
			SELECT lp.label, lp.policy_type, lp.policy_data, lp.stack_id
			FROM latest_policies lp
			JOIN stack_policies sp ON sp.policy_id = lp.id
			WHERE sp.stack_id = @p2 AND lp.rn = 1 AND lp.operation != 'delete'
			AND (lp.stack_id IS NULL OR lp.stack_id = '')
		)
		SELECT label, policy_type, policy_data, stack_id FROM inline_policies
		UNION
		SELECT label, policy_type, policy_data, stack_id FROM standalone_policies`

	rows, err := d.conn.QueryContext(ctx, query, stackID, stackID)
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

		// Standalone policy rows carry no stack_id; fall back to the queried one.
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

func (d *DatastoreMSSQL) GetStandalonePolicy(label string) (pkgmodel.Policy, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetStandalonePolicy")
	defer span.End()

	query := `
		WITH latest_policy AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE label = @p1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'`

	var policyLabel, policyType, policyDataStr string
	err := d.conn.QueryRowContext(ctx, query, label).Scan(&policyLabel, &policyType, &policyDataStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return deserializePolicy(policyLabel, policyType, policyDataStr, "")
}

func (d *DatastoreMSSQL) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "ListAllStandalonePolicies")
	defer span.End()

	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label`

	rows, err := d.conn.QueryContext(ctx, query)
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

func (d *DatastoreMSSQL) AttachPolicyToStack(stackID, policyLabel string) error {
	ctx, span := mssqlTracer.Start(context.Background(), "AttachPolicyToStack")
	defer span.End()

	policyQuery := `
		WITH latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE label = @p1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'`
	var policyID string
	err := d.conn.QueryRowContext(ctx, policyQuery, policyLabel).Scan(&policyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("standalone policy not found: %s", policyLabel)
		}
		return fmt.Errorf("failed to get policy ID: %w", err)
	}

	// IF NOT EXISTS guard: MSSQL has no ON CONFLICT.
	insertQuery := `
		IF NOT EXISTS (SELECT 1 FROM stack_policies WHERE stack_id = @p1 AND policy_id = @p2)
			INSERT INTO stack_policies (stack_id, policy_id) VALUES (@p1, @p2)`
	if _, err = d.conn.ExecContext(ctx, insertQuery, stackID, policyID); err != nil {
		return fmt.Errorf("failed to attach policy to stack: %w", err)
	}

	slog.Debug("Attached standalone policy to stack",
		"stackID", stackID, "policyLabel", policyLabel, "policyID", policyID)

	return nil
}

func (d *DatastoreMSSQL) IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "IsPolicyAttachedToStack")
	defer span.End()

	query := `
		WITH latest_stack AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM stacks
			WHERE label = @p1
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE label = @p2 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT TOP (1) 1 FROM stack_policies sp
		JOIN latest_stack s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'`
	var exists int
	err := d.conn.QueryRowContext(ctx, query, stackLabel, policyLabel).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check policy attachment: %w", err)
	}
	return true, nil
}

func (d *DatastoreMSSQL) GetStacksReferencingPolicy(policyLabel string) ([]string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetStacksReferencingPolicy")
	defer span.End()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM stacks
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE label = @p1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT s.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY s.label`
	rows, err := d.conn.QueryContext(ctx, query, policyLabel)
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

func (d *DatastoreMSSQL) GetAttachedPolicyLabelsForStack(stackLabel string) ([]string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetAttachedPolicyLabelsForStack")
	defer span.End()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM stacks
			WHERE label = @p1
		),
		latest_policies AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT p.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policies p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY p.label`
	rows, err := d.conn.QueryContext(ctx, query, stackLabel)
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

func (d *DatastoreMSSQL) DetachPolicyFromStack(stackLabel, policyLabel string) error {
	ctx, span := mssqlTracer.Start(context.Background(), "DetachPolicyFromStack")
	defer span.End()

	query := `
		DELETE FROM stack_policies
		WHERE stack_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
				FROM stacks
				WHERE label = @p1
			) sub WHERE rn = 1 AND operation != 'delete'
		)
		AND policy_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
				FROM policies
				WHERE label = @p2 AND (stack_id IS NULL OR stack_id = '')
			) sub WHERE rn = 1 AND operation != 'delete'
		)`
	if _, err := d.conn.ExecContext(ctx, query, stackLabel, policyLabel); err != nil {
		return fmt.Errorf("failed to detach policy from stack: %w", err)
	}

	slog.Debug("Detached standalone policy from stack",
		"stackLabel", stackLabel, "policyLabel", policyLabel)

	return nil
}

func (d *DatastoreMSSQL) DeletePolicy(policyLabel string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "DeletePolicy")
	defer span.End()

	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE label = @p1 AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'`
	var id, policyType string
	err := d.conn.QueryRowContext(ctx, query, policyLabel).Scan(&id, &policyType)
	if err != nil {
		return "", fmt.Errorf("failed to get policy for deletion: %w", err)
	}

	// Tombstone: NULL stack_id, empty policy_data.
	version := mksuid.New().String()
	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	                VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`
	_, err = d.conn.ExecContext(ctx, insertQuery, id, version, "", "delete", policyLabel, policyType, nil, "{}")
	if err != nil {
		return "", fmt.Errorf("failed to delete policy: %w", err)
	}

	slog.Debug("Deleted standalone policy", "label", policyLabel, "id", id)

	return version, nil
}

func (d *DatastoreMSSQL) DeletePoliciesForStack(stackID string, commandID string) error {
	ctx, span := mssqlTracer.Start(context.Background(), "DeletePoliciesForStack")
	defer span.End()

	// Detach standalone attachments; leave the standalone policies themselves alone.
	if _, err := d.conn.ExecContext(ctx, "DELETE FROM stack_policies WHERE stack_id = @p1", stackID); err != nil {
		slog.Warn("Failed to delete policy attachments for stack", "error", err, "stackID", stackID)
	}

	// Tombstone the inline policies bound to this stack.
	inlinePoliciesQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
			WHERE stack_id = @p1
		)
		SELECT id, label, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'`
	rows, err := d.conn.QueryContext(ctx, inlinePoliciesQuery, stackID)
	if err != nil {
		return fmt.Errorf("failed to get inline policies for stack: %w", err)
	}

	type inlinePolicy struct{ id, label, policyType string }
	var toDelete []inlinePolicy
	for rows.Next() {
		var p inlinePolicy
		if err := rows.Scan(&p.id, &p.label, &p.policyType); err != nil {
			slog.Warn("Failed to scan policy for deletion", "error", err)
			continue
		}
		toDelete = append(toDelete, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("error iterating inline policies: %w", err)
	}
	_ = rows.Close()

	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	                VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`
	for _, p := range toDelete {
		version := mksuid.New().String()
		if _, err := d.conn.ExecContext(ctx, insertQuery, p.id, version, commandID, "delete", p.label, p.policyType, stackID, "{}"); err != nil {
			slog.Error("Failed to delete inline policy", "error", err, "label", p.label)
			continue
		}
		slog.Debug("Deleted inline policy as part of stack deletion", "label", p.label, "stackID", stackID)
	}

	return nil
}

func (d *DatastoreMSSQL) GetExpiredStacks() ([]datastore.ExpiredStackInfo, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetExpiredStacks")
	defer span.End()

	// Stacks whose TTL (inline or attached standalone) has elapsed, excluding any
	// with active commands. JSON_VALUE + DATEADD stand in for postgres interval math.
	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
		),
		inline_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       JSON_VALUE(p.policy_data, '$.OnDependents') as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND DATEADD(SECOND, CAST(JSON_VALUE(p.policy_data, '$.TTLSeconds') AS INT), s.valid_from) < SYSUTCDATETIME()
		),
		standalone_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       JSON_VALUE(p.policy_data, '$.OnDependents') as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN stack_policies sp ON sp.stack_id = s.id
			JOIN latest_policies p ON p.id = sp.policy_id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND (p.stack_id IS NULL OR p.stack_id = '')
			AND DATEADD(SECOND, CAST(JSON_VALUE(p.policy_data, '$.TTLSeconds') AS INT), s.valid_from) < SYSUTCDATETIME()
		),
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
		ORDER BY valid_from`

	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []datastore.ExpiredStackInfo
	for rows.Next() {
		var info datastore.ExpiredStackInfo
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

func (d *DatastoreMSSQL) GetStacksWithAutoReconcilePolicy() ([]datastore.StackReconcileInfo, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetStacksWithAutoReconcilePolicy")
	defer span.End()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM policies
		),
		inline_auto_reconcile AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       CAST(JSON_VALUE(p.policy_data, '$.IntervalSeconds') AS BIGINT) as interval_seconds
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'auto-reconcile'
		),
		standalone_auto_reconcile AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       CAST(JSON_VALUE(p.policy_data, '$.IntervalSeconds') AS BIGINT) as interval_seconds
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
		       COALESCE(lr.last_reconcile_at, CAST('1970-01-01 00:00:00' AS datetime2)) as last_reconcile_at
		FROM all_auto_reconcile ar
		LEFT JOIN last_reconcile lr ON ar.stack_label = lr.stack_label`

	rows, err := d.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []datastore.StackReconcileInfo
	for rows.Next() {
		var info datastore.StackReconcileInfo
		var lastReconcileAt time.Time
		if err := rows.Scan(&info.StackLabel, &info.StackID, &info.IntervalSeconds, &lastReconcileAt); err != nil {
			return nil, err
		}
		info.LastReconcileAt = lastReconcileAt.UTC()
		result = append(result, info)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (d *DatastoreMSSQL) GetResourcesAtLastReconcile(stackLabel string) ([]datastore.ResourceSnapshot, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetResourcesAtLastReconcile")
	defer span.End()

	// Declared state: resources from the last user-initiated reconcile apply.
	// Filter source='user' so auto-reconcile/sync don't shift the baseline.
	query := `
		WITH last_user_reconcile_for_stack AS (
			SELECT TOP (1) fc.command_id, fc.timestamp
			FROM forma_commands fc
			INNER JOIN resource_updates ru ON ru.command_id = fc.command_id
			WHERE fc.config_mode = 'reconcile'
			AND fc.state = 'Success'
			AND fc.command = 'apply'
			AND ru.source = 'user'
			AND ru.stack_label = @p1
			GROUP BY fc.command_id, fc.timestamp
			ORDER BY fc.timestamp DESC
		)
		SELECT r.ksuid, r.type, r.label, r.target,
		       JSON_QUERY(r.data, '$.Properties') as properties,
		       JSON_QUERY(r.data, '$.Schema') as [schema],
		       r.native_id
		FROM resources r
		WHERE r.command_id = (SELECT command_id FROM last_user_reconcile_for_stack)
		AND r.stack = @p2
		AND r.operation != 'delete'`

	rows, err := d.conn.QueryContext(ctx, query, stackLabel, stackLabel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []datastore.ResourceSnapshot
	for rows.Next() {
		var snapshot datastore.ResourceSnapshot
		var propsData, schemaData, nativeID sql.NullString
		if err := rows.Scan(&snapshot.KSUID, &snapshot.Type, &snapshot.Label, &snapshot.Target, &propsData, &schemaData, &nativeID); err != nil {
			return nil, err
		}
		if propsData.Valid {
			snapshot.Properties = json.RawMessage(propsData.String)
		}
		if schemaData.Valid {
			if err := json.Unmarshal([]byte(schemaData.String), &snapshot.Schema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal schema for resource %s: %w", snapshot.Label, err)
			}
		}
		if nativeID.Valid {
			snapshot.NativeID = nativeID.String
		}
		result = append(result, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (d *DatastoreMSSQL) StackHasActiveCommands(stackLabel string) (bool, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "StackHasActiveCommands")
	defer span.End()

	query := `
		SELECT CASE WHEN EXISTS (
			SELECT 1 FROM resource_updates ru
			JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE ru.stack_label = @p1
			AND fc.state NOT IN ('Success', 'Failed', 'Canceled')
		) THEN 1 ELSE 0 END`

	var exists bool
	if err := d.conn.QueryRowContext(ctx, query, stackLabel).Scan(&exists); err != nil {
		return false, err
	}

	return exists, nil
}
