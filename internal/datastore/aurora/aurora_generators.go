// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package aurora

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/demula/mksuid/v2"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// CreateGenerator persists a new generator. stack_id stores the stack's
// resolved KSUID — like policy_id on an inline policy, not the label — read
// off gen.GetStackID(). Unlike CreatePolicy the column is never NULL: a
// generator is always inline to exactly one stack.
func (d *DatastoreAuroraDataAPI) CreateGenerator(gen pkgmodel.Generator, commandID string) (string, error) {
	ctx := context.Background()

	id := mksuid.New().String()
	version := mksuid.New().String()

	data, err := datastore.GeneratorData(gen)
	if err != nil {
		return "", err
	}

	query := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data)
	          VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data)`
	params := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "create"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: gen.GetLabel()}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: gen.GetType()}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: gen.GetStackID()}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: string(data)}},
	}

	_, err = d.executeStatement(ctx, query, params)
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
func (d *DatastoreAuroraDataAPI) UpdateGenerator(gen pkgmodel.Generator, commandID string) (string, error) {
	ctx := context.Background()

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
	                VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "update"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: gen.GetLabel()}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: gen.GetType()}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: gen.GetStackID()}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: string(data)}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		slog.Error("Failed to update generator", "error", err, "label", gen.GetLabel())
		return "", err
	}

	return version, nil
}

// findGeneratorID returns the id of the latest generator row matching label
// and stackID. Shared by UpdateGenerator's current-label lookup and its
// alias fallback.
func (d *DatastoreAuroraDataAPI) findGeneratorID(ctx context.Context, label, stackID string) (string, error) {
	selectQuery := `
		SELECT id FROM generators
		WHERE label = :label AND stack_id = :stack_id
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	selectParams := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
	}

	result, err := d.executeStatement(ctx, selectQuery, selectParams)
	if err != nil {
		return "", err
	}
	if len(result.Records) == 0 {
		return "", fmt.Errorf("generator not found: %s", label)
	}

	return getStringField(result.Records[0][0])
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
func (d *DatastoreAuroraDataAPI) DeleteGenerator(label, stackLabel string) (string, error) {
	ctx := context.Background()

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
			WHERE stack_id = :stack_id
		)
		SELECT id, generator_type
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = :label
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", fmt.Errorf("failed to get generator for deletion: %w", err)
	}
	if len(result.Records) == 0 {
		return "", nil
	}

	record := result.Records[0]
	id, err := getStringField(record[0])
	if err != nil {
		return "", fmt.Errorf("failed to get generator id: %w", err)
	}
	generatorType, err := getStringField(record[1])
	if err != nil {
		return "", fmt.Errorf("failed to get generator type: %w", err)
	}

	version := mksuid.New().String()
	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data)
	                VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: ""}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "delete"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: generatorType}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: "{}"}},
	}
	_, err = d.executeStatement(ctx, insertQuery, insertParams)
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
func (d *DatastoreAuroraDataAPI) GetGenerator(label, stackLabel string) (pkgmodel.Generator, error) {
	ctx := context.Background()

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
			WHERE stack_id = :stack_id
		)
		SELECT generator_data
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = :label
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get generator: %w", err)
	}
	if len(result.Records) == 0 {
		return nil, nil
	}

	dataStr, err := getStringField(result.Records[0][0])
	if err != nil {
		return nil, fmt.Errorf("failed to get generator data: %w", err)
	}

	return datastore.GeneratorFromData([]byte(dataStr))
}

// LoadGeneratorsByStack returns all non-deleted generators owned by a stack.
// The stack label is resolved to its current KSUID first, for the same
// reason GetGenerator does. A stack that doesn't exist owns no generators.
func (d *DatastoreAuroraDataAPI) LoadGeneratorsByStack(stackLabel string) ([]pkgmodel.Generator, error) {
	ctx := context.Background()

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
			WHERE stack_id = :stack_id
		)
		SELECT generator_data
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete'
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var generators []pkgmodel.Generator
	for _, record := range result.Records {
		if len(record) < 1 {
			continue
		}
		dataStr, err := getStringField(record[0])
		if err != nil {
			slog.Warn("Failed to read generator data, skipping", "error", err, "stackLabel", stackLabel)
			continue
		}
		gen, err := datastore.GeneratorFromData([]byte(dataStr))
		if err != nil {
			slog.Warn("Failed to deserialize generator, skipping", "error", err, "stackLabel", stackLabel)
			continue
		}
		generators = append(generators, gen)
	}

	return generators, nil
}

// GeneratorIDForTesting returns the internal KSUID identity (the id column,
// stable across CreateGenerator/UpdateGenerator) of the current (max-version)
// generator row with the given label on the given stack, or "" if none
// exists. Generator has no public API that exposes this id — the Datastore
// interface returns only version strings — so the dstest suite needs a
// direct accessor to prove the id survives an update unchanged.
func (d *DatastoreAuroraDataAPI) GeneratorIDForTesting(label, stackLabel string) (string, error) {
	ctx := context.Background()

	stack, err := d.GetStackByLabel(stackLabel)
	if err != nil {
		return "", fmt.Errorf("failed to resolve stack %q: %w", stackLabel, err)
	}
	if stack == nil {
		return "", nil
	}

	query := `
		SELECT id FROM generators
		WHERE stack_id = :stack_id AND label = :label
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}
	if len(result.Records) == 0 {
		return "", nil
	}

	return getStringField(result.Records[0][0])
}
