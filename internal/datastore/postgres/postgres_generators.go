// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/demula/mksuid/v2"
	"github.com/jackc/pgx/v5"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// CreateGenerator persists a new generator. stack_id stores the stack's
// resolved KSUID — like policy_id on an inline policy, not the label — read
// off gen.GetStackID(). Unlike CreatePolicy the column is never NULL: a
// generator is always inline to exactly one stack.
func (d DatastorePostgres) CreateGenerator(gen pkgmodel.Generator, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "CreateGenerator")
	defer span.End()

	id := mksuid.New().String()
	version := mksuid.New().String()

	data, err := datastore.GeneratorData(gen)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = d.pool.Exec(ctx, query, id, version, commandID, "create", gen.GetLabel(), gen.GetType(), gen.GetStackID(), string(data))
	if err != nil {
		slog.Error("Failed to create generator", "error", err, "label", gen.GetLabel())
		return "", err
	}

	return version, nil
}

// UpdateGenerator persists a new version of an existing generator. The
// existing row is found by label and stack ID — a generator has no
// standalone form, so unlike UpdatePolicy there is no NULL-stack branch —
// and the new version row carries forward the same id.
//
// A miss on the current label falls back to a lookup by gen.GetAlias(), the
// generator's previous label: this is the rename path. Without it, a renamed
// generator would find no row to update, and the caller would have to fall
// back to Create, minting a fresh id and losing the identity a later
// rotation schedule keys off.
func (d DatastorePostgres) UpdateGenerator(gen pkgmodel.Generator, commandID string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "UpdateGenerator")
	defer span.End()

	id, err := d.findGeneratorID(ctx, gen.GetLabel(), gen.GetStackID())
	if err != nil && gen.GetAlias() != "" {
		id, err = d.findGeneratorID(ctx, gen.GetAlias(), gen.GetStackID())
	}
	if err != nil {
		return "", fmt.Errorf("failed to find existing generator: %w", err)
	}

	version := mksuid.New().String()
	data, err := datastore.GeneratorData(gen)
	if err != nil {
		return "", err
	}

	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data)
	                VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = d.pool.Exec(ctx, insertQuery, id, version, commandID, "update", gen.GetLabel(), gen.GetType(), gen.GetStackID(), string(data))
	if err != nil {
		slog.Error("Failed to update generator", "error", err, "label", gen.GetLabel())
		return "", err
	}

	return version, nil
}

// findGeneratorID returns the id of the latest generator row matching label
// and stackID. Shared by UpdateGenerator's current-label lookup and its
// alias fallback.
func (d DatastorePostgres) findGeneratorID(ctx context.Context, label, stackID string) (string, error) {
	query := `
		SELECT id FROM generators
		WHERE label = $1 AND stack_id = $2
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	var id string
	err := d.pool.QueryRow(ctx, query, label, stackID).Scan(&id)
	return id, err
}

// DeleteGenerator soft-deletes the generator with the given label on the
// given stack. The stack is resolved from its label the same way
// GetGenerator does; a stack that doesn't exist has nothing to delete. A
// label with no live match is a no-op success that returns an empty version,
// mirroring DeletePolicy.
//
// The candidate row is the latest version *per id* across the whole stack,
// filtered by label only after that windowing — not the latest version
// among rows already filtered to this label. A rename (UpdateGenerator's
// alias fallback) leaves the old label's row in place with an older version
// number; filtering by label first would still find and re-delete that
// stale row instead of correctly reporting no live match.
func (d DatastorePostgres) DeleteGenerator(label, stackLabel string) (string, error) {
	ctx, span := tracer.Start(context.Background(), "DeleteGenerator")
	defer span.End()

	stack, err := d.GetStackByLabel(stackLabel)
	if err != nil {
		return "", fmt.Errorf("failed to resolve stack %q: %w", stackLabel, err)
	}
	if stack == nil {
		return "", nil
	}

	query := `
		WITH latest_generators AS (
			SELECT id, label, generator_type, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE stack_id = $1
		)
		SELECT id, generator_type
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = $2
	`
	var id, generatorType string
	err = d.pool.QueryRow(ctx, query, stack.ID, label).Scan(&id, &generatorType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get generator for deletion: %w", err)
	}

	version := mksuid.New().String()
	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = d.pool.Exec(ctx, insertQuery, id, version, "", "delete", label, generatorType, stack.ID, "{}")
	if err != nil {
		return "", fmt.Errorf("failed to delete generator: %w", err)
	}

	slog.Debug("Deleted generator", "label", label, "id", id, "stackLabel", stackLabel)

	return version, nil
}

// GetGenerator retrieves the current (latest, non-deleted) generator with the
// given label on the given stack. The stack label is resolved to its
// current KSUID first, since generators.stack_id stores the stack's id, not
// its label — mirroring how a policy's inline lookups are scoped by stack
// ID. Returns nil, nil if no live stack or no live generator matches.
//
// As with DeleteGenerator, the label filter is applied after windowing to
// the latest version per id, not before: a renamed generator's previous
// label must not resolve just because its now-superseded row is still the
// newest one under that label.
func (d DatastorePostgres) GetGenerator(label, stackLabel string) (pkgmodel.Generator, error) {
	ctx, span := tracer.Start(context.Background(), "GetGenerator")
	defer span.End()

	stack, err := d.GetStackByLabel(stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve stack %q: %w", stackLabel, err)
	}
	if stack == nil {
		return nil, nil
	}

	query := `
		WITH latest_generators AS (
			SELECT label, generator_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE stack_id = $1
		)
		SELECT generator_data
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = $2
	`
	var dataStr string
	err = d.pool.QueryRow(ctx, query, stack.ID, label).Scan(&dataStr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get generator: %w", err)
	}

	return datastore.GeneratorFromData([]byte(dataStr))
}

// LoadGeneratorsByStack returns all non-deleted generators owned by a stack.
// The stack label is resolved to its current KSUID first, for the same
// reason GetGenerator does. A stack that doesn't exist owns no generators.
func (d DatastorePostgres) LoadGeneratorsByStack(stackLabel string) ([]pkgmodel.Generator, error) {
	ctx, span := tracer.Start(context.Background(), "LoadGeneratorsByStack")
	defer span.End()

	stack, err := d.GetStackByLabel(stackLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve stack %q: %w", stackLabel, err)
	}
	if stack == nil {
		return nil, nil
	}

	query := `
		WITH latest_generators AS (
			SELECT id, generator_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE stack_id = $1
		)
		SELECT generator_data
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete'
	`
	rows, err := d.pool.Query(ctx, query, stack.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var generators []pkgmodel.Generator
	for rows.Next() {
		var dataStr string
		if err := rows.Scan(&dataStr); err != nil {
			return nil, err
		}
		gen, err := datastore.GeneratorFromData([]byte(dataStr))
		if err != nil {
			slog.Warn("Failed to deserialize generator, skipping", "error", err, "stackLabel", stackLabel)
			continue
		}
		generators = append(generators, gen)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return generators, nil
}
