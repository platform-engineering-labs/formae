// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"
	"encoding/json"
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

	// Honor a KSUID translation already assigned (see pkgmodel.Generator.GetID
	// and generator_update.GenerateGeneratorUpdates), so a $gen reference
	// resolved in the same command that creates this generator names the
	// exact row this call persists, rather than an independently minted one
	// — mirrors storeResource's identical id-already-assigned handling.
	id := gen.GetID()
	if id == "" {
		id = mksuid.New().String()
	}
	version := mksuid.New().String()

	data, err := datastore.GeneratorData(gen)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data, generation_id, generation_spec)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err = d.pool.Exec(ctx, query, id, version, commandID, "create", gen.GetLabel(), gen.GetType(), gen.GetStackID(), string(data), "", "{}")
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

	id, generationID, generationSpec, err := d.findGeneratorForUpdate(ctx, gen.GetLabel(), gen.GetStackID())
	if err != nil && gen.GetAlias() != "" {
		id, generationID, generationSpec, err = d.findGeneratorForUpdate(ctx, gen.GetAlias(), gen.GetStackID())
	}
	if err != nil {
		return "", fmt.Errorf("failed to find existing generator: %w", err)
	}

	version := mksuid.New().String()
	data, err := datastore.GeneratorData(gen)
	if err != nil {
		return "", err
	}

	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data, generation_id, generation_spec)
	                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err = d.pool.Exec(ctx, insertQuery, id, version, commandID, "update", gen.GetLabel(), gen.GetType(), gen.GetStackID(), string(data), generationID, generationSpec)
	if err != nil {
		slog.Error("Failed to update generator", "error", err, "label", gen.GetLabel())
		return "", err
	}

	return version, nil
}

// findGeneratorForUpdate returns the id and current generation fields of the
// live generator row matching label and stackID. Shared by UpdateGenerator's
// current-label lookup and its alias fallback.
//
// Windows to the latest version *per id* first, filters out tombstones, and
// only then matches label — the same ordering GetGenerator and
// DeleteGenerator use, for the same reason: a label can be shared across a
// dead row (superseded by a rename) and a live one (a rename-back, or a
// fresh generator created under a freed label), and matching label before
// windowing can resolve the wrong id entirely, or a live id's stale,
// pre-rename generation.
//
// The generation fields are read here so UpdateGenerator can copy them
// forward onto the new version row it writes: a spec edit or an alias rename
// must not drop the generation a generator currently holds — dropping it
// would make the next apply see no generation and regenerate, silently
// rotating a live credential.
func (d DatastorePostgres) findGeneratorForUpdate(ctx context.Context, label, stackID string) (id, generationID, generationSpec string, err error) {
	query := `
		WITH latest_generators AS (
			SELECT id, label, generation_id, generation_spec, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE stack_id = $1
		)
		SELECT id, generation_id, generation_spec
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = $2
	`
	err = d.pool.QueryRow(ctx, query, stackID, label).Scan(&id, &generationID, &generationSpec)
	return id, generationID, generationSpec, err
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
	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data, generation_id, generation_spec) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err = d.pool.Exec(ctx, insertQuery, id, version, "", "delete", label, generatorType, stack.ID, "{}", "", "{}")
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

// generatorIdentityFromRow builds a GeneratorIdentity from the raw columns a
// generator row query returns. generation_spec is stored as '{}' on a row
// that has never had a generation drawn; GenerationSpec must read back as
// nil, not as an empty-but-non-nil json.RawMessage, so the zero-value case is
// handled explicitly rather than by just wrapping whatever was stored.
func generatorIdentityFromRow(id, generationID, generationSpec string) datastore.GeneratorIdentity {
	if generationID == "" {
		return datastore.GeneratorIdentity{ID: id}
	}
	return datastore.GeneratorIdentity{ID: id, GenerationID: generationID, GenerationSpec: json.RawMessage(generationSpec)}
}

// GetGeneratorIdentity returns the identity of the live generator with the
// given label on the given stack. Uses the same windowing and label-after-rn
// ordering as GetGenerator, for the same reason: a renamed generator's
// previous label must not resolve just because its now-superseded row is
// still the newest one under that label.
func (d DatastorePostgres) GetGeneratorIdentity(label, stackLabel string) (datastore.GeneratorIdentity, error) {
	ctx, span := tracer.Start(context.Background(), "GetGeneratorIdentity")
	defer span.End()

	stack, err := d.GetStackByLabel(stackLabel)
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to resolve stack %q: %w", stackLabel, err)
	}
	if stack == nil {
		return datastore.GeneratorIdentity{}, nil
	}

	query := `
		WITH latest_generators AS (
			SELECT id, label, generation_id, generation_spec, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE stack_id = $1
		)
		SELECT id, generation_id, generation_spec
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = $2
	`
	var id, generationID, generationSpec string
	err = d.pool.QueryRow(ctx, query, stack.ID, label).Scan(&id, &generationID, &generationSpec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return datastore.GeneratorIdentity{}, nil
		}
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generator identity: %w", err)
	}

	return generatorIdentityFromRow(id, generationID, generationSpec), nil
}

// GetGeneratorIdentityByID returns the identity of the live generator with
// the given KSUID, whichever stack owns it. Windows on id directly rather
// than resolving a stack first: the id alone determines the row family.
func (d DatastorePostgres) GetGeneratorIdentityByID(generatorID string) (datastore.GeneratorIdentity, error) {
	ctx, span := tracer.Start(context.Background(), "GetGeneratorIdentityByID")
	defer span.End()

	query := `
		WITH latest_generators AS (
			SELECT id, generation_id, generation_spec, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE id = $1
		)
		SELECT id, generation_id, generation_spec
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete'
	`
	var id, generationID, generationSpec string
	err := d.pool.QueryRow(ctx, query, generatorID).Scan(&id, &generationID, &generationSpec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return datastore.GeneratorIdentity{}, nil
		}
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generator identity by id: %w", err)
	}

	return generatorIdentityFromRow(id, generationID, generationSpec), nil
}

// AdvanceGeneration records that a new generation was drawn for this
// generator, under this spec. Writes a new version row that carries forward
// the existing label/type/stack/generator_data unchanged — only the
// generation columns change. Errors if generationID is empty, if drawnUnder
// is not valid JSON, or if the generator's latest row is a tombstone: a
// deleted id is not resurrected.
//
// The caller is the generator update actor
// (generator_update.GeneratorUpdater): it calls this once it has drawn a
// value, so the generation the value came from is durable before any
// destination is stamped with it.
func (d DatastorePostgres) AdvanceGeneration(generatorID, generationID, commandID string, drawnUnder json.RawMessage) error {
	ctx, span := tracer.Start(context.Background(), "AdvanceGeneration")
	defer span.End()

	if generationID == "" {
		return fmt.Errorf("advance generation: generationID must not be empty")
	}
	if !json.Valid(drawnUnder) {
		return fmt.Errorf("advance generation: drawnUnder spec must be valid JSON")
	}

	var label, generatorType, stackID, generatorData, operation string
	err := d.pool.QueryRow(ctx,
		`SELECT label, generator_type, stack_id, generator_data, operation FROM generators WHERE id = $1 ORDER BY version COLLATE "C" DESC LIMIT 1`,
		generatorID,
	).Scan(&label, &generatorType, &stackID, &generatorData, &operation)
	if err != nil {
		return fmt.Errorf("failed to find generator %q: %w", generatorID, err)
	}
	if operation == "delete" {
		return fmt.Errorf("generator %q not found", generatorID)
	}

	version := mksuid.New().String()
	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data, generation_id, generation_spec)
	                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err = d.pool.Exec(ctx, insertQuery, generatorID, version, commandID, "update", label, generatorType, stackID, generatorData, generationID, string(drawnUnder))
	if err != nil {
		return fmt.Errorf("failed to advance generation: %w", err)
	}

	return nil
}
