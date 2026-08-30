// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package aurora

import (
	"context"
	"encoding/json"
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
	          VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data, :generation_id, :generation_spec)`
	params := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "create"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: gen.GetLabel()}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: gen.GetType()}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: gen.GetStackID()}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: string(data)}},
		{Name: aws.String("generation_id"), Value: &types.FieldMemberStringValue{Value: ""}},
		{Name: aws.String("generation_spec"), Value: &types.FieldMemberStringValue{Value: "{}"}},
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
	                VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data, :generation_id, :generation_spec)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "update"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: gen.GetLabel()}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: gen.GetType()}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: gen.GetStackID()}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: string(data)}},
		{Name: aws.String("generation_id"), Value: &types.FieldMemberStringValue{Value: generationID}},
		{Name: aws.String("generation_spec"), Value: &types.FieldMemberStringValue{Value: generationSpec}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
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
func (d *DatastoreAuroraDataAPI) findGeneratorForUpdate(ctx context.Context, label, stackID string) (id, generationID, generationSpec string, err error) {
	selectQuery := `
		WITH latest_generators AS (
			SELECT id, label, generation_id, generation_spec, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE stack_id = :stack_id
		)
		SELECT id, generation_id, generation_spec
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = :label
	`
	selectParams := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}

	result, err := d.executeStatement(ctx, selectQuery, selectParams)
	if err != nil {
		return "", "", "", err
	}
	if len(result.Records) == 0 {
		return "", "", "", fmt.Errorf("generator not found: %s", label)
	}

	id, err = getStringField(result.Records[0][0])
	if err != nil {
		return "", "", "", err
	}
	generationID, err = getStringField(result.Records[0][1])
	if err != nil {
		return "", "", "", err
	}
	generationSpec, err = getStringField(result.Records[0][2])
	if err != nil {
		return "", "", "", err
	}

	return id, generationID, generationSpec, nil
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
	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data, generation_id, generation_spec)
	                VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data, :generation_id, :generation_spec)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: ""}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "delete"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: generatorType}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: "{}"}},
		{Name: aws.String("generation_id"), Value: &types.FieldMemberStringValue{Value: ""}},
		{Name: aws.String("generation_spec"), Value: &types.FieldMemberStringValue{Value: "{}"}},
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
func (d *DatastoreAuroraDataAPI) GetGeneratorIdentity(label, stackLabel string) (datastore.GeneratorIdentity, error) {
	ctx := context.Background()

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
			WHERE stack_id = :stack_id
		)
		SELECT id, generation_id, generation_spec
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete' AND label = :label
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stack.ID}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generator identity: %w", err)
	}
	if len(result.Records) == 0 {
		return datastore.GeneratorIdentity{}, nil
	}

	id, err := getStringField(result.Records[0][0])
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generator id: %w", err)
	}
	generationID, err := getStringField(result.Records[0][1])
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generation id: %w", err)
	}
	generationSpec, err := getStringField(result.Records[0][2])
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generation spec: %w", err)
	}

	return generatorIdentityFromRow(id, generationID, generationSpec), nil
}

// GetGeneratorIdentityByID returns the identity of the live generator with
// the given KSUID, whichever stack owns it. Windows on id directly rather
// than resolving a stack first: the id alone determines the row family.
func (d *DatastoreAuroraDataAPI) GetGeneratorIdentityByID(generatorID string) (datastore.GeneratorIdentity, error) {
	ctx := context.Background()

	query := `
		WITH latest_generators AS (
			SELECT id, generation_id, generation_spec, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM generators
			WHERE id = :id
		)
		SELECT id, generation_id, generation_spec
		FROM latest_generators
		WHERE rn = 1 AND operation != 'delete'
	`
	params := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: generatorID}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generator identity by id: %w", err)
	}
	if len(result.Records) == 0 {
		return datastore.GeneratorIdentity{}, nil
	}

	id, err := getStringField(result.Records[0][0])
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generator id: %w", err)
	}
	generationID, err := getStringField(result.Records[0][1])
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generation id: %w", err)
	}
	generationSpec, err := getStringField(result.Records[0][2])
	if err != nil {
		return datastore.GeneratorIdentity{}, fmt.Errorf("failed to get generation spec: %w", err)
	}

	return generatorIdentityFromRow(id, generationID, generationSpec), nil
}

// AdvanceGeneration records that a new generation was drawn for this
// generator, under this spec. Writes a new version row that carries forward
// the existing label/type/stack/generator_data unchanged — only the
// generation columns change. Errors if drawnUnder is empty, or if the
// generator's latest row is a tombstone: a deleted id is not resurrected.
//
// No production caller in this slice: the executable generator node that
// draws generations arrives in a later slice. It ships here because the
// generation columns are inert without a writer, and a test-only backdoor
// would misrepresent a mechanism we are shipping for real use.
func (d *DatastoreAuroraDataAPI) AdvanceGeneration(generatorID, generationID string, drawnUnder json.RawMessage) error {
	ctx := context.Background()

	if len(drawnUnder) == 0 {
		return fmt.Errorf("advance generation: drawnUnder spec must not be empty")
	}

	selectQuery := `SELECT label, generator_type, stack_id, generator_data, operation FROM generators WHERE id = :id ORDER BY version COLLATE "C" DESC LIMIT 1`
	selectParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: generatorID}},
	}
	result, err := d.executeStatement(ctx, selectQuery, selectParams)
	if err != nil {
		return fmt.Errorf("failed to find generator %q: %w", generatorID, err)
	}
	if len(result.Records) == 0 {
		return fmt.Errorf("generator not found: %s", generatorID)
	}

	label, err := getStringField(result.Records[0][0])
	if err != nil {
		return fmt.Errorf("failed to get label: %w", err)
	}
	generatorType, err := getStringField(result.Records[0][1])
	if err != nil {
		return fmt.Errorf("failed to get generator type: %w", err)
	}
	stackID, err := getStringField(result.Records[0][2])
	if err != nil {
		return fmt.Errorf("failed to get stack id: %w", err)
	}
	generatorData, err := getStringField(result.Records[0][3])
	if err != nil {
		return fmt.Errorf("failed to get generator data: %w", err)
	}
	operation, err := getStringField(result.Records[0][4])
	if err != nil {
		return fmt.Errorf("failed to get operation: %w", err)
	}
	if operation == "delete" {
		return fmt.Errorf("generator %q not found", generatorID)
	}

	version := mksuid.New().String()
	insertQuery := `INSERT INTO generators (id, version, command_id, operation, label, generator_type, stack_id, generator_data, generation_id, generation_spec)
	                VALUES (:id, :version, :command_id, :operation, :label, :generator_type, :stack_id, :generator_data, :generation_id, :generation_spec)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: generatorID}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: ""}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "update"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("generator_type"), Value: &types.FieldMemberStringValue{Value: generatorType}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
		{Name: aws.String("generator_data"), Value: &types.FieldMemberStringValue{Value: generatorData}},
		{Name: aws.String("generation_id"), Value: &types.FieldMemberStringValue{Value: generationID}},
		{Name: aws.String("generation_spec"), Value: &types.FieldMemberStringValue{Value: string(drawnUnder)}},
	}
	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		return fmt.Errorf("failed to advance generation: %w", err)
	}

	return nil
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
