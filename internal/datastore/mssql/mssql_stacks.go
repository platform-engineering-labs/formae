// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/demula/mksuid/v2"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Stacks are versioned: every create/update/delete appends a new (id, version)
// row; a delete writes a tombstone with operation='delete'. The "current" row
// is the one with the greatest version KSUID for its id, and it counts as
// present only when that row is not a delete. KSUID byte-order comparison uses
// COLLATE Latin1_General_BIN2 (the MSSQL analogue of postgres COLLATE "C").

func (d *DatastoreMSSQL) CreateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "CreateStack")
	defer span.End()

	existing, err := d.GetStackByLabel(stack.Label)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return "", fmt.Errorf("stack already exists: %s", stack.Label)
	}

	id := stack.ID
	if id == "" {
		id = mksuid.New().String()
		stack.ID = id
	}
	version := mksuid.New().String()

	query := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`
	_, err = d.conn.ExecContext(ctx, query, id, version, commandID, "create", stack.Label, stack.Description)
	if err != nil {
		slog.Error("Failed to create stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d *DatastoreMSSQL) UpdateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "UpdateStack")
	defer span.End()

	query := `
		SELECT TOP (1) id, operation FROM stacks
		WHERE label = @p1
		ORDER BY version COLLATE Latin1_General_BIN2 DESC
	`
	row := d.conn.QueryRowContext(ctx, query, stack.Label)

	var id, operation string
	if err := row.Scan(&id, &operation); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("stack not found: %s", stack.Label)
		}
		return "", err
	}
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", stack.Label)
	}

	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`
	_, err := d.conn.ExecContext(ctx, insertQuery, id, version, commandID, "update", stack.Label, stack.Description)
	if err != nil {
		slog.Error("Failed to update stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d *DatastoreMSSQL) DeleteStack(label string, commandID string) (string, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "DeleteStack")
	defer span.End()

	query := `
		SELECT TOP (1) id, operation FROM stacks
		WHERE label = @p1
		ORDER BY version COLLATE Latin1_General_BIN2 DESC
	`
	row := d.conn.QueryRowContext(ctx, query, label)

	var id, operation string
	if err := row.Scan(&id, &operation); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("stack not found: %s", label)
		}
		return "", err
	}
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", label)
	}

	// Cascade: drop inline policies; best-effort, mirrors postgres/sqlite.
	if err := d.DeletePoliciesForStack(id, commandID); err != nil {
		slog.Warn("Failed to delete policies for stack", "error", err, "stackID", id, "label", label)
	}

	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`
	_, execErr := d.conn.ExecContext(ctx, insertQuery, id, version, commandID, "delete", label, "")
	if execErr != nil {
		slog.Error("Failed to delete stack", "error", execErr, "label", label)
		return "", execErr
	}

	return version, nil
}

func (d *DatastoreMSSQL) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "GetStackByLabel")
	defer span.End()

	query := `
		SELECT TOP (1) id, description, operation FROM stacks
		WHERE label = @p1
		ORDER BY version COLLATE Latin1_General_BIN2 DESC
	`
	row := d.conn.QueryRowContext(ctx, query, label)

	var id, description, operation string
	if err := row.Scan(&id, &description, &operation); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if operation == "delete" {
		return nil, nil
	}

	return &pkgmodel.Stack{
		ID:          id,
		Label:       label,
		Description: description,
	}, nil
}

func (d *DatastoreMSSQL) CountResourcesInStack(label string) (int, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "CountResourcesInStack")
	defer span.End()

	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE stack = @p1
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version COLLATE Latin1_General_BIN2 > r1.version COLLATE Latin1_General_BIN2
		)
		AND operation != @p2
	`
	row := d.conn.QueryRowContext(ctx, query, label, string(resource_update.OperationDelete))

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

func (d *DatastoreMSSQL) ListAllStacks() ([]*pkgmodel.Stack, error) {
	ctx, span := mssqlTracer.Start(context.Background(), "ListAllStacks")
	defer span.End()

	query := `
		SELECT id, label, description, valid_from FROM (
			SELECT id, label, description, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE Latin1_General_BIN2 DESC) as rn
			FROM stacks
		) sub
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`
	rows, err := d.conn.QueryContext(ctx, query)
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
