// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/demula/mksuid/v2"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/constants"
	metaConfig "github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	metaTypes "github.com/platform-engineering-labs/formae/internal/metastructure/types"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

type DatastoreAuroraDataAPI struct {
	client     *rdsdata.Client
	clusterARN string
	secretARN  string
	database   string
	agentID    string
	ctx        context.Context
}

func NewDatastoreAuroraDataAPI(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (Datastore, error) {
	var opts []func(*config.LoadOptions) error

	// When using a custom endpoint (e.g. local-data-api for testing),
	// use static dummy creds to avoid requiring real AWS creds
	if cfg.AuroraDataAPI.Endpoint != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	if cfg.AuroraDataAPI.Region != "" {
		awsCfg.Region = cfg.AuroraDataAPI.Region
	}

	client := rdsdata.NewFromConfig(awsCfg, func(o *rdsdata.Options) {
		if cfg.AuroraDataAPI.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.AuroraDataAPI.Endpoint)
		}
	})

	d := &DatastoreAuroraDataAPI{
		client:     client,
		clusterARN: cfg.AuroraDataAPI.ClusterARN,
		secretARN:  cfg.AuroraDataAPI.SecretARN,
		database:   cfg.AuroraDataAPI.Database,
		agentID:    agentID,
		ctx:        ctx,
	}

	// Run migrations
	if err := d.runMigrations(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	slog.Info("Started Aurora Data API datastore",
		"clusterARN", cfg.AuroraDataAPI.ClusterARN,
		"database", cfg.AuroraDataAPI.Database,
		"region", cfg.AuroraDataAPI.Region)

	return d, nil
}

func (d *DatastoreAuroraDataAPI) runMigrations() error {
	ctx := context.Background()

	// Ensure db_version table exists (goose's migration tracking table)
	if err := d.ensureVersionTable(ctx); err != nil {
		return fmt.Errorf("failed to create version table: %w", err)
	}

	// Get current version
	currentVersion, err := d.getCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	// Get all migration files from embedded postgres migrations
	migrations, err := d.collectMigrations()
	if err != nil {
		return fmt.Errorf("failed to collect migrations: %w", err)
	}

	if len(migrations) == 0 {
		return nil
	}

	targetVersion := migrations[len(migrations)-1].version
	if currentVersion >= targetVersion {
		slog.Debug("Database is up to date", "version", currentVersion)
		return nil
	}

	slog.Warn("Database migrations are starting via Data API",
		"currentVersion", currentVersion,
		"targetVersion", targetVersion)

	// Run pending migrations
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		slog.Info("Running migration", "version", m.version, "name", m.name)

		// Execute migration statements
		for _, stmt := range m.upStatements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}

			_, err := d.executeStatement(ctx, stmt, nil)
			if err != nil {
				return fmt.Errorf("migration %d failed on statement: %w", m.version, err)
			}
		}

		// Record migration version
		if err := d.recordVersion(ctx, m.version); err != nil {
			return fmt.Errorf("failed to record migration version %d: %w", m.version, err)
		}
	}

	slog.Info("Database migrations completed", "version", targetVersion)
	return nil
}

type migration struct {
	version      int64
	name         string
	upStatements []string
}

// ensureVersionTable creates the db_version table if it doesn't exist.
func (d *DatastoreAuroraDataAPI) ensureVersionTable(ctx context.Context) error {
	sql := `CREATE TABLE IF NOT EXISTS db_version (
		id SERIAL PRIMARY KEY,
		version_id BIGINT NOT NULL,
		is_applied BOOLEAN NOT NULL DEFAULT TRUE,
		tstamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
	_, err := d.executeStatement(ctx, sql, nil)
	return err
}

// getCurrentVersion returns the highest applied migration version.
func (d *DatastoreAuroraDataAPI) getCurrentVersion(ctx context.Context) (int64, error) {
	sql := `SELECT COALESCE(MAX(version_id), 0) FROM db_version WHERE is_applied = true`
	output, err := d.executeStatement(ctx, sql, nil)
	if err != nil {
		return 0, err
	}

	if len(output.Records) == 0 || len(output.Records[0]) == 0 {
		return 0, nil
	}

	// Parse the result - Data API returns typed fields
	field := output.Records[0][0]
	switch v := field.(type) {
	case *types.FieldMemberLongValue:
		return v.Value, nil
	case *types.FieldMemberStringValue:
		return strconv.ParseInt(v.Value, 10, 64)
	case *types.FieldMemberIsNull:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected field type for version: %T", field)
	}
}

// recordVersion records a migration version as applied.
func (d *DatastoreAuroraDataAPI) recordVersion(ctx context.Context, version int64) error {
	sql := `INSERT INTO db_version (version_id, is_applied) VALUES (:version, true)`
	params := []types.SqlParameter{
		{Name: aws.String("version"), Value: &types.FieldMemberLongValue{Value: version}},
	}
	_, err := d.executeStatement(ctx, sql, params)
	return err
}

// collectMigrations reads and parses the embedded postgres migrations.
func (d *DatastoreAuroraDataAPI) collectMigrations() ([]migration, error) {
	entries, err := embedMigrationsPostgres.ReadDir("migrations_postgres")
	if err != nil {
		return nil, err
	}

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version from filename (e.g., "00001_initial_schema.sql")
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		content, err := embedMigrationsPostgres.ReadFile("migrations_postgres/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}

		upStatements := parseGooseUp(string(content))
		migrations = append(migrations, migration{
			version:      version,
			name:         entry.Name(),
			upStatements: upStatements,
		})
	}

	// Sort by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

// parseGooseUp extracts the Up statements from a goose migration file.
func parseGooseUp(content string) []string {
	lines := strings.Split(content, "\n")
	var upStatements []string
	var currentStatement strings.Builder
	inUp := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "-- +goose Up") {
			inUp = true
			continue
		}
		if strings.HasPrefix(trimmed, "-- +goose Down") {
			// Flush any pending statement
			if currentStatement.Len() > 0 {
				upStatements = append(upStatements, currentStatement.String())
			}
			break
		}

		if !inUp {
			continue
		}

		// Skip comments
		if strings.HasPrefix(trimmed, "--") {
			continue
		}

		currentStatement.WriteString(line)
		currentStatement.WriteString("\n")

		// Statement ends with semicolon
		if strings.HasSuffix(trimmed, ";") {
			upStatements = append(upStatements, currentStatement.String())
			currentStatement.Reset()
		}
	}

	// Flush any remaining statement
	if currentStatement.Len() > 0 {
		stmt := strings.TrimSpace(currentStatement.String())
		if stmt != "" {
			upStatements = append(upStatements, stmt)
		}
	}

	return upStatements
}

// getStringField extracts a string value from a Data API field.
func getStringField(field types.Field) (string, error) {
	switch v := field.(type) {
	case *types.FieldMemberStringValue:
		return v.Value, nil
	case *types.FieldMemberIsNull:
		return "", nil
	default:
		return "", fmt.Errorf("unexpected field type for string: %T", field)
	}
}

// getIntField extracts an int value from a Data API field.
func getIntField(field types.Field) (int, error) {
	switch v := field.(type) {
	case *types.FieldMemberLongValue:
		return int(v.Value), nil
	case *types.FieldMemberStringValue:
		i, err := strconv.Atoi(v.Value)
		if err != nil {
			return 0, err
		}
		return i, nil
	case *types.FieldMemberIsNull:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected field type for int: %T", field)
	}
}

// getBoolField extracts a bool value from a Data API field.
func getBoolField(field types.Field) (bool, error) {
	switch v := field.(type) {
	case *types.FieldMemberBooleanValue:
		return v.Value, nil
	case *types.FieldMemberStringValue:
		return v.Value == "true" || v.Value == "t" || v.Value == "1", nil
	case *types.FieldMemberIsNull:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected field type for bool: %T", field)
	}
}

// getRawJSONField extracts a JSON value from a Data API field as json.RawMessage.
func getRawJSONField(field types.Field) ([]byte, error) {
	switch v := field.(type) {
	case *types.FieldMemberStringValue:
		return []byte(v.Value), nil
	case *types.FieldMemberIsNull:
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected field type for JSON: %T", field)
	}
}

func getTimestampField(field types.Field) (time.Time, error) {
	str, err := getStringField(field)
	if err != nil {
		return time.Time{}, err
	}
	if str == "" {
		return time.Time{}, nil
	}

	// Try parsing with various formats
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, str); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("failed to parse timestamp: %s", str)
}

// executeStatement is a helper to execute SQL via the Data API.
func (d *DatastoreAuroraDataAPI) executeStatement(ctx context.Context, sql string, params []types.SqlParameter) (*rdsdata.ExecuteStatementOutput, error) {
	input := &rdsdata.ExecuteStatementInput{
		ResourceArn: aws.String(d.clusterARN),
		SecretArn:   aws.String(d.secretARN),
		Database:    aws.String(d.database),
		Sql:         aws.String(sql),
		Parameters:  params,
	}

	return d.client.ExecuteStatement(ctx, input)
}

// beginTransaction starts a new transaction.
func (d *DatastoreAuroraDataAPI) beginTransaction(ctx context.Context) (string, error) {
	input := &rdsdata.BeginTransactionInput{
		ResourceArn: aws.String(d.clusterARN),
		SecretArn:   aws.String(d.secretARN),
		Database:    aws.String(d.database),
	}

	output, err := d.client.BeginTransaction(ctx, input)
	if err != nil {
		return "", err
	}

	return *output.TransactionId, nil
}

// commitTransaction commits a transaction.
func (d *DatastoreAuroraDataAPI) commitTransaction(ctx context.Context, transactionID string) error {
	input := &rdsdata.CommitTransactionInput{
		ResourceArn:   aws.String(d.clusterARN),
		SecretArn:     aws.String(d.secretARN),
		TransactionId: aws.String(transactionID),
	}

	_, err := d.client.CommitTransaction(ctx, input)
	return err
}

// rollbackTransaction rolls back a transaction.
func (d *DatastoreAuroraDataAPI) rollbackTransaction(ctx context.Context, transactionID string) error {
	input := &rdsdata.RollbackTransactionInput{
		ResourceArn:   aws.String(d.clusterARN),
		SecretArn:     aws.String(d.secretARN),
		TransactionId: aws.String(transactionID),
	}

	_, err := d.client.RollbackTransaction(ctx, input)
	return err
}

// executeStatementInTransaction executes SQL within a transaction.
func (d *DatastoreAuroraDataAPI) executeStatementInTransaction(ctx context.Context, transactionID string, sql string, params []types.SqlParameter) (*rdsdata.ExecuteStatementOutput, error) {
	input := &rdsdata.ExecuteStatementInput{
		ResourceArn:   aws.String(d.clusterARN),
		SecretArn:     aws.String(d.secretARN),
		Database:      aws.String(d.database),
		Sql:           aws.String(sql),
		Parameters:    params,
		TransactionId: aws.String(transactionID),
	}

	return d.client.ExecuteStatement(ctx, input)
}

func (d *DatastoreAuroraDataAPI) StoreFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	ctx := context.Background()

	targetUpdatesJSON, err := json.Marshal(fa.TargetUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal target updates: %w", err)
	}

	stackUpdatesJSON, err := json.Marshal(fa.StackUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal stack updates: %w", err)
	}

	query := fmt.Sprintf(`
	INSERT INTO %s (command_id, timestamp, command, state, agent_version, client_id, agent_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, modified_ts)
	VALUES (:command_id, :timestamp::timestamp, :command, :state, :agent_version, :client_id, :agent_id,
		:description_text, :description_confirm, :config_mode, :config_force, :config_simulate,
		:target_updates, :stack_updates, :modified_ts::timestamp)
	ON CONFLICT (command_id) DO UPDATE
	SET timestamp = EXCLUDED.timestamp,
	command = EXCLUDED.command,
	state = EXCLUDED.state,
	agent_version = EXCLUDED.agent_version,
	client_id = EXCLUDED.client_id,
	agent_id = EXCLUDED.agent_id,
	description_text = EXCLUDED.description_text,
	description_confirm = EXCLUDED.description_confirm,
	config_mode = EXCLUDED.config_mode,
	config_force = EXCLUDED.config_force,
	config_simulate = EXCLUDED.config_simulate,
	target_updates = EXCLUDED.target_updates,
	stack_updates = EXCLUDED.stack_updates,
	modified_ts = EXCLUDED.modified_ts
	`, CommandsTable)

	params := []types.SqlParameter{
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("timestamp"), Value: &types.FieldMemberStringValue{Value: fa.StartTs.UTC().Format(time.RFC3339Nano)}},
		{Name: aws.String("command"), Value: &types.FieldMemberStringValue{Value: string(fa.Command)}},
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(fa.State)}},
		{Name: aws.String("agent_version"), Value: &types.FieldMemberStringValue{Value: formae.Version}},
		{Name: aws.String("client_id"), Value: &types.FieldMemberStringValue{Value: fa.ClientID}},
		{Name: aws.String("agent_id"), Value: &types.FieldMemberStringValue{Value: d.agentID}},
		{Name: aws.String("description_text"), Value: &types.FieldMemberStringValue{Value: fa.Description.Text}},
		{Name: aws.String("description_confirm"), Value: &types.FieldMemberBooleanValue{Value: fa.Description.Confirm}},
		{Name: aws.String("config_mode"), Value: &types.FieldMemberStringValue{Value: string(fa.Config.Mode)}},
		{Name: aws.String("config_force"), Value: &types.FieldMemberBooleanValue{Value: fa.Config.Force}},
		{Name: aws.String("config_simulate"), Value: &types.FieldMemberBooleanValue{Value: fa.Config.Simulate}},
		{Name: aws.String("target_updates"), Value: &types.FieldMemberStringValue{Value: string(targetUpdatesJSON)}},
		{Name: aws.String("stack_updates"), Value: &types.FieldMemberStringValue{Value: string(stackUpdatesJSON)}},
		{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: fa.ModifiedTs.UTC().Format(time.RFC3339Nano)}},
	}

	_, err = d.executeStatement(ctx, query, params)
	if err != nil {
		slog.Error("failed to store FormaCommand", "error", err)
		return err
	}

	// Store ResourceUpdates in the normalized table
	if len(fa.ResourceUpdates) > 0 && fa.Command != pkgmodel.CommandSync {
		if err := d.BulkStoreResourceUpdates(commandID, fa.ResourceUpdates); err != nil {
			return fmt.Errorf("failed to store resource updates: %w", err)
		}
	}

	return nil
}

func (d *DatastoreAuroraDataAPI) LoadFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx := context.Background()

	// Load commands
	query := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, modified_ts
	FROM forma_commands
	ORDER BY timestamp DESC
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var commands []*forma_command.FormaCommand
	for _, record := range output.Records {
		cmd, err := d.parseFormaCommandRecord(record)
		if err != nil {
			return nil, err
		}

		// Load resource updates for this command
		updates, err := d.LoadResourceUpdates(cmd.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load resource updates for command %s: %w", cmd.ID, err)
		}
		cmd.ResourceUpdates = updates

		commands = append(commands, cmd)
	}

	return commands, nil
}

func (d *DatastoreAuroraDataAPI) LoadIncompleteFormaCommands() ([]*forma_command.FormaCommand, error) {
	ctx := context.Background()

	query := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, modified_ts
	FROM forma_commands
	WHERE command != :sync_command AND state = :state
	ORDER BY timestamp DESC
	`
	params := []types.SqlParameter{
		{Name: aws.String("sync_command"), Value: &types.FieldMemberStringValue{Value: string(pkgmodel.CommandSync)}},
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(forma_command.CommandStateInProgress)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var commands []*forma_command.FormaCommand
	for _, record := range output.Records {
		cmd, err := d.parseFormaCommandRecord(record)
		if err != nil {
			return nil, err
		}

		// Load resource updates for this command
		updates, err := d.LoadResourceUpdates(cmd.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load resource updates for command %s: %w", cmd.ID, err)
		}
		cmd.ResourceUpdates = updates

		commands = append(commands, cmd)
	}

	return commands, nil
}

// parseFormaCommandRecord parses a single forma_commands row into a FormaCommand.
func (d *DatastoreAuroraDataAPI) parseFormaCommandRecord(record []types.Field) (*forma_command.FormaCommand, error) {
	if len(record) < 13 {
		return nil, fmt.Errorf("unexpected record length: %d", len(record))
	}

	commandID, _ := getStringField(record[0])
	timestamp, _ := getTimestampField(record[1])
	command, _ := getStringField(record[2])
	state, _ := getStringField(record[3])
	clientID, _ := getStringField(record[4])
	descText, _ := getStringField(record[5])
	descConfirm, _ := getBoolField(record[6])
	configMode, _ := getStringField(record[7])
	configForce, _ := getBoolField(record[8])
	configSimulate, _ := getBoolField(record[9])
	targetUpdatesJSON, _ := getStringField(record[10])
	stackUpdatesJSON, _ := getStringField(record[11])
	modifiedTs, _ := getTimestampField(record[12])

	var targetUpdates []target_update.TargetUpdate
	if targetUpdatesJSON != "" {
		_ = json.Unmarshal([]byte(targetUpdatesJSON), &targetUpdates)
	}

	var stackUpdates []stack_update.StackUpdate
	if stackUpdatesJSON != "" {
		_ = json.Unmarshal([]byte(stackUpdatesJSON), &stackUpdates)
	}

	return &forma_command.FormaCommand{
		ID:       commandID,
		StartTs:  timestamp,
		Command:  pkgmodel.Command(command),
		State:    forma_command.CommandState(state),
		ClientID: clientID,
		Description: pkgmodel.Description{
			Text:    descText,
			Confirm: descConfirm,
		},
		Config: metaConfig.FormaCommandConfig{
			Mode:     pkgmodel.FormaApplyMode(configMode),
			Force:    configForce,
			Simulate: configSimulate,
		},
		TargetUpdates: targetUpdates,
		StackUpdates:  stackUpdates,
		ModifiedTs:    modifiedTs,
	}, nil
}

func (d *DatastoreAuroraDataAPI) DeleteFormaCommand(fa *forma_command.FormaCommand, commandID string) error {
	ctx := context.Background()

	// Delete resource_updates first
	deleteUpdatesQuery := `DELETE FROM resource_updates WHERE command_id = :command_id`
	deleteUpdatesParams := []types.SqlParameter{
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
	}
	_, err := d.executeStatement(ctx, deleteUpdatesQuery, deleteUpdatesParams)
	if err != nil {
		return fmt.Errorf("failed to delete resource_updates: %w", err)
	}

	// Delete the command
	deleteCommandQuery := fmt.Sprintf("DELETE FROM %s WHERE command_id = :command_id", CommandsTable)
	_, err = d.executeStatement(ctx, deleteCommandQuery, deleteUpdatesParams)
	return err
}

func (d *DatastoreAuroraDataAPI) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	ctx := context.Background()

	query := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, modified_ts
	FROM forma_commands
	WHERE command_id = :command_id
	`
	params := []types.SqlParameter{
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, fmt.Errorf("forma command not found: %v", commandID)
	}

	cmd, err := d.parseFormaCommandRecord(output.Records[0])
	if err != nil {
		return nil, err
	}

	// Load resource updates
	updates, err := d.LoadResourceUpdates(cmd.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load resource updates: %w", err)
	}
	cmd.ResourceUpdates = updates

	return cmd, nil
}

func (d *DatastoreAuroraDataAPI) GetMostRecentFormaCommandByClientID(clientID string) (*forma_command.FormaCommand, error) {
	ctx := context.Background()

	query := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, modified_ts
	FROM forma_commands
	WHERE client_id = :client_id
	ORDER BY timestamp DESC
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("client_id"), Value: &types.FieldMemberStringValue{Value: clientID}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, fmt.Errorf("no forma commands found for client: %v", clientID)
	}

	cmd, err := d.parseFormaCommandRecord(output.Records[0])
	if err != nil {
		return nil, err
	}

	// Load resource updates
	updates, err := d.LoadResourceUpdates(cmd.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to load resource updates: %w", err)
	}
	cmd.ResourceUpdates = updates

	return cmd, nil
}

func (d *DatastoreAuroraDataAPI) GetResourceModificationsSinceLastReconcile(stack string) ([]ResourceModification, error) {
	ctx := context.Background()

	query := `
	SELECT DISTINCT
	T2.type,
	T2.label,
	T2.operation
	FROM forma_commands AS T1
	JOIN resources AS T2
	ON T1.command_id = T2.command_id
	WHERE
	EXISTS (
		SELECT 1
		FROM resources AS r1
		WHERE r1.stack = :stack
		AND NOT EXISTS (
			SELECT 1
			FROM resources AS r2
			WHERE r1.ksuid = r2.ksuid
			AND r2.version COLLATE "C" > r1.version COLLATE "C"
		)
		AND r1.operation != 'delete'
	)
	AND T1.timestamp > (
		SELECT fc.timestamp
		FROM forma_commands fc
		WHERE fc.config_mode = 'reconcile'
		AND EXISTS (
			SELECT 1
			FROM resources r
			WHERE r.command_id = fc.command_id
			AND r.stack = :stack
		)
		ORDER BY fc.timestamp DESC
		LIMIT 1
	)
	AND T2.stack = :stack
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: stack}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	modifications := make(map[ResourceModification]struct{})
	for _, record := range output.Records {
		if len(record) < 3 {
			continue
		}
		resourceType, _ := getStringField(record[0])
		label, _ := getStringField(record[1])
		operation, _ := getStringField(record[2])
		modifications[ResourceModification{Stack: stack, Type: resourceType, Label: label, Operation: operation}] = struct{}{}
	}

	result := make([]ResourceModification, 0, len(modifications))
	for mod := range modifications {
		result = append(result, mod)
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) QueryFormaCommands(statusQuery *StatusQuery) ([]*forma_command.FormaCommand, error) {
	ctx := context.Background()

	// Build query with dynamic filters
	queryStr := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, modified_ts
	FROM forma_commands
	WHERE 1=1
	`
	params := []types.SqlParameter{}
	paramIdx := 1

	if statusQuery.CommandID != nil {
		paramName := fmt.Sprintf("command_id_%d", paramIdx)
		op := "="
		if statusQuery.CommandID.Constraint == Excluded {
			op = "!="
		}
		queryStr += fmt.Sprintf(" AND command_id %s :%s", op, paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: statusQuery.CommandID.Item},
		})
		paramIdx++
	}

	if statusQuery.ClientID != nil {
		paramName := fmt.Sprintf("client_id_%d", paramIdx)
		op := "="
		if statusQuery.ClientID.Constraint == Excluded {
			op = "!="
		}
		queryStr += fmt.Sprintf(" AND client_id %s :%s", op, paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: statusQuery.ClientID.Item},
		})
		paramIdx++
	}

	if statusQuery.Command != nil {
		paramName := fmt.Sprintf("command_%d", paramIdx)
		op := "="
		if statusQuery.Command.Constraint == Excluded {
			op = "!="
		}
		queryStr += fmt.Sprintf(" AND LOWER(command) %s LOWER(:%s)", op, paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: statusQuery.Command.Item},
		})
		paramIdx++
	} else {
		queryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}

	if statusQuery.Stack != nil {
		paramName := fmt.Sprintf("stack_%d", paramIdx)
		op := "="
		if statusQuery.Stack.Constraint == Excluded {
			op = "!="
		}
		queryStr += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM resource_updates ru WHERE ru.command_id = forma_commands.command_id AND ru.stack_label %s :%s)", op, paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: statusQuery.Stack.Item},
		})
		paramIdx++
	}

	if statusQuery.Status != nil {
		paramName := fmt.Sprintf("status_%d", paramIdx)
		op := "="
		if statusQuery.Status.Constraint == Excluded {
			op = "!="
		}
		queryStr += fmt.Sprintf(" AND LOWER(state) %s LOWER(:%s)", op, paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: statusQuery.Status.Item},
		})
	}

	queryStr += " ORDER BY timestamp DESC"

	limit := DefaultFormaCommandsQueryLimit
	if statusQuery.N > 0 && statusQuery.N < limit {
		limit = statusQuery.N
	}
	queryStr += fmt.Sprintf(" LIMIT %d", limit)

	output, err := d.executeStatement(ctx, queryStr, params)
	if err != nil {
		return nil, err
	}

	var commands []*forma_command.FormaCommand
	for _, record := range output.Records {
		cmd, err := d.parseFormaCommandRecord(record)
		if err != nil {
			return nil, err
		}

		// Load resource updates for this command
		updates, err := d.LoadResourceUpdates(cmd.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load resource updates for command %s: %w", cmd.ID, err)
		}
		cmd.ResourceUpdates = updates

		commands = append(commands, cmd)
	}

	return commands, nil
}

func (d *DatastoreAuroraDataAPI) QueryResources(query *ResourceQuery) ([]*pkgmodel.Resource, error) {
	ctx := context.Background()

	queryStr := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND r1.operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	// Build dynamic query with named parameters
	paramIdx := 1
	if query.NativeID != nil && query.NativeID.Constraint != Excluded {
		paramName := fmt.Sprintf("native_id_%d", paramIdx)
		if query.NativeID.Constraint == Required {
			queryStr += fmt.Sprintf(" AND native_id = :%s", paramName)
		} else {
			queryStr += fmt.Sprintf(" AND (native_id = :%s OR native_id IS NULL)", paramName)
		}
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: query.NativeID.Item},
		})
		paramIdx++
	}

	if query.Stack != nil && query.Stack.Constraint != Excluded {
		paramName := fmt.Sprintf("stack_%d", paramIdx)
		if query.Stack.Constraint == Required {
			queryStr += fmt.Sprintf(" AND stack = :%s", paramName)
		} else {
			queryStr += fmt.Sprintf(" AND (stack = :%s OR stack IS NULL)", paramName)
		}
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: query.Stack.Item},
		})
		paramIdx++
	}

	if query.Type != nil && query.Type.Constraint != Excluded {
		paramName := fmt.Sprintf("type_%d", paramIdx)
		if query.Type.Constraint == Required {
			queryStr += fmt.Sprintf(" AND LOWER(type) = LOWER(:%s)", paramName)
		} else {
			queryStr += fmt.Sprintf(" AND (LOWER(type) = LOWER(:%s) OR type IS NULL)", paramName)
		}
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: query.Type.Item},
		})
		paramIdx++
	}

	if query.Label != nil && query.Label.Constraint != Excluded {
		paramName := fmt.Sprintf("label_%d", paramIdx)
		if query.Label.Constraint == Required {
			queryStr += fmt.Sprintf(" AND label = :%s", paramName)
		} else {
			queryStr += fmt.Sprintf(" AND (label = :%s OR label IS NULL)", paramName)
		}
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: query.Label.Item},
		})
		paramIdx++
	}

	if query.Target != nil && query.Target.Constraint != Excluded {
		paramName := fmt.Sprintf("target_%d", paramIdx)
		if query.Target.Constraint == Required {
			queryStr += fmt.Sprintf(" AND target = :%s", paramName)
		} else {
			queryStr += fmt.Sprintf(" AND (target = :%s OR target IS NULL)", paramName)
		}
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: query.Target.Item},
		})
		paramIdx++
	}

	if query.Managed != nil && query.Managed.Constraint != Excluded {
		paramName := fmt.Sprintf("managed_%d", paramIdx)
		if query.Managed.Constraint == Required {
			queryStr += fmt.Sprintf(" AND managed = :%s", paramName)
		}
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberBooleanValue{Value: query.Managed.Item},
		})
	}

	queryStr += " ORDER BY type, label"

	output, err := d.executeStatement(ctx, queryStr, params)
	if err != nil {
		return nil, err
	}

	var resources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			continue
		}

		jsonData, err := getStringField(record[0])
		if err != nil {
			return nil, fmt.Errorf("failed to parse data: %w", err)
		}

		ksuid, err := getStringField(record[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse ksuid: %w", err)
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		resources = append(resources, &resource)
	}

	return resources, nil
}

func (d *DatastoreAuroraDataAPI) StoreResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	ctx := context.Background()

	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}

	return d.storeResource(ctx, resource, jsonData, commandID, string(resource_update.OperationUpdate))
}

func (d *DatastoreAuroraDataAPI) DeleteResource(resource *pkgmodel.Resource, commandID string) (string, error) {
	ctx := context.Background()

	return d.storeResource(ctx, resource, []byte("{}"), commandID, string(resource_update.OperationDelete))
}

func (d *DatastoreAuroraDataAPI) BulkStoreResources(resources []pkgmodel.Resource, commandID string) (string, error) {
	var ret string
	var err error
	for _, resource := range resources {
		if ret, err = d.StoreResource(&resource, commandID); err != nil {
			slog.Error("Failed to store resource", "error", err, "resourceURI", resource.URI())
			return "", err
		}
	}
	return ret, nil
}

func (d *DatastoreAuroraDataAPI) CountResourcesInStack(label string) (int, error) {
	ctx := context.Background()

	// Count only latest version of resources that haven't been deleted
	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE stack = :stack
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return 0, err
	}

	if len(output.Records) == 0 || len(output.Records[0]) == 0 {
		return 0, nil
	}

	if longVal, ok := output.Records[0][0].(*types.FieldMemberLongValue); ok {
		return int(longVal.Value), nil
	}

	return 0, fmt.Errorf("unexpected type for COUNT result")
}

func (d *DatastoreAuroraDataAPI) LoadAllResourcesByStack() (map[string][]*pkgmodel.Resource, error) {
	ctx := context.Background()

	query := `
		SELECT data, ksuid
		FROM resources r1
		WHERE NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var allResources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			continue
		}

		jsonData, err := getStringField(record[0])
		if err != nil {
			return nil, err
		}
		ksuid, err := getStringField(record[1])
		if err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		allResources = append(allResources, &resource)
	}

	// Group resources by stack label
	stackResourcesMap := make(map[string][]*pkgmodel.Resource)
	for _, resource := range allResources {
		if resource.Stack != "" {
			stackResourcesMap[resource.Stack] = append(stackResourcesMap[resource.Stack], resource)
		}
	}

	return stackResourcesMap, nil
}

func (d *DatastoreAuroraDataAPI) LoadResourcesByStack(stackLabel string) ([]*pkgmodel.Resource, error) {
	ctx := context.Background()

	query := `
		SELECT data, ksuid
		FROM resources r1
		WHERE stack = :stack
		AND NOT EXISTS (
			SELECT 1
			FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var resources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			continue
		}

		jsonData, err := getStringField(record[0])
		if err != nil {
			return nil, err
		}
		ksuid, err := getStringField(record[1])
		if err != nil {
			return nil, err
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		resources = append(resources, &resource)
	}

	return resources, nil
}

func (d *DatastoreAuroraDataAPI) CountResourcesInTarget(targetLabel string) (int, error) {
	ctx := context.Background()

	// Count only latest version of resources that haven't been deleted
	query := `
		SELECT COUNT(*) FROM resources r1
		WHERE target = :target
		AND NOT EXISTS (
			SELECT 1 FROM resources r2
			WHERE r1.uri = r2.uri
			AND r2.version > r1.version
		)
		AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("target"), Value: &types.FieldMemberStringValue{Value: targetLabel}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return 0, err
	}

	if len(output.Records) == 0 || len(output.Records[0]) == 0 {
		return 0, nil
	}

	if longVal, ok := output.Records[0][0].(*types.FieldMemberLongValue); ok {
		return int(longVal.Value), nil
	}

	return 0, fmt.Errorf("unexpected type for COUNT result")
}

func (d *DatastoreAuroraDataAPI) storeResource(ctx context.Context, resource *pkgmodel.Resource, data []byte, commandID string, operation string) (string, error) {
	if resource.Ksuid == "" {
		resource.Ksuid = metautil.NewID()
	}

	// Check if this resource already exists by native_id and type
	// Fetch all versions and find the max in Go code for reliability
	query := `SELECT ksuid, data, uri, version, managed FROM resources WHERE native_id = :native_id AND type = :type`
	params := []types.SqlParameter{
		{Name: aws.String("native_id"), Value: &types.FieldMemberStringValue{Value: resource.NativeID}},
		{Name: aws.String("type"), Value: &types.FieldMemberStringValue{Value: resource.Type}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", fmt.Errorf("failed to check existing resource: %w", err)
	}

	// Find the record with the highest version
	var maxVersion string
	var maxRecordIdx = -1
	for i, record := range output.Records {
		if len(record) < 5 {
			continue
		}
		version, err := getStringField(record[3])
		if err != nil {
			continue
		}
		if version > maxVersion {
			maxVersion = version
			maxRecordIdx = i
		}
	}

	// No existing resource found - insert new
	if maxRecordIdx == -1 {
		newVersion := mksuid.New().String()
		insertQuery := `
		INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
		VALUES (:uri, :version, :command_id, :operation, :native_id, :stack, :type, :label, :target, :data::jsonb, :managed, :ksuid)
		`
		insertParams := []types.SqlParameter{
			{Name: aws.String("uri"), Value: &types.FieldMemberStringValue{Value: string(resource.URI())}},
			{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: newVersion}},
			{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
			{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: operation}},
			{Name: aws.String("native_id"), Value: &types.FieldMemberStringValue{Value: resource.NativeID}},
			{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: resource.Stack}},
			{Name: aws.String("type"), Value: &types.FieldMemberStringValue{Value: resource.Type}},
			{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: resource.Label}},
			{Name: aws.String("target"), Value: &types.FieldMemberStringValue{Value: resource.Target}},
			{Name: aws.String("data"), Value: &types.FieldMemberStringValue{Value: string(data)}},
			{Name: aws.String("managed"), Value: &types.FieldMemberBooleanValue{Value: resource.Managed}},
			{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: resource.Ksuid}},
		}

		_, err = d.executeStatement(ctx, insertQuery, insertParams)
		if err != nil {
			slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
			return "", fmt.Errorf("failed to store resource: %w", err)
		}

		return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
	}

	// Parse existing resource data
	record := output.Records[maxRecordIdx]
	ksuid, _ := getStringField(record[0])
	existingData, _ := getStringField(record[1])
	version, _ := getStringField(record[3])
	managed, _ := getBoolField(record[4])

	// Handle unmanaged -> managed transition
	if !managed && operation != string(resource_update.OperationDelete) {
		slog.Debug("Resource discovered before apply completed, adopting discovered KSUID",
			"native_id", resource.NativeID,
			"type", resource.Type,
			"discovered_ksuid", ksuid,
			"original_ksuid", resource.Ksuid)
		resource.Ksuid = ksuid
	}

	var existingResource pkgmodel.Resource
	if err = json.Unmarshal([]byte(existingData), &existingResource); err != nil {
		return "", fmt.Errorf("failed to unmarshal existing resource: %w", err)
	}

	readWriteEqual, readOnlyEqual := resourcesAreEqual(resource, &existingResource)

	if operation == string(resource_update.OperationDelete) {
		// For delete operations, check if the latest version is already a delete
		latestOpQuery := `SELECT operation FROM resources WHERE uri = :uri ORDER BY version COLLATE "C" DESC LIMIT 1`
		latestOpParams := []types.SqlParameter{
			{Name: aws.String("uri"), Value: &types.FieldMemberStringValue{Value: string(resource.URI())}},
		}
		latestOutput, err := d.executeStatement(ctx, latestOpQuery, latestOpParams)
		if err != nil {
			return "", fmt.Errorf("failed to retrieve latest operation: %w", err)
		}

		if len(latestOutput.Records) > 0 {
			latestOp, _ := getStringField(latestOutput.Records[0][0])
			if latestOp == string(resource_update.OperationDelete) {
				return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
			}
		}
	} else {
		if readWriteEqual && readOnlyEqual {
			return fmt.Sprintf("%s_%s", resource.Ksuid, version), nil
		}
	}

	var newVersion string
	if readWriteEqual && !readOnlyEqual {
		newVersion = version
	} else {
		newVersion = mksuid.New().String()
	}

	upsertQuery := `
	INSERT INTO resources (uri, version, command_id, operation, native_id, stack, type, label, target, data, managed, ksuid)
	VALUES (:uri, :version, :command_id, :operation, :native_id, :stack, :type, :label, :target, :data::jsonb, :managed, :ksuid)
	ON CONFLICT (uri, version) DO UPDATE SET
	command_id = EXCLUDED.command_id,
	operation = EXCLUDED.operation,
	native_id = EXCLUDED.native_id,
	stack = EXCLUDED.stack,
	type = EXCLUDED.type,
	label = EXCLUDED.label,
	target = EXCLUDED.target,
	data = EXCLUDED.data,
	managed = EXCLUDED.managed,
	ksuid = EXCLUDED.ksuid
	`
	upsertParams := []types.SqlParameter{
		{Name: aws.String("uri"), Value: &types.FieldMemberStringValue{Value: string(resource.URI())}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: newVersion}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: operation}},
		{Name: aws.String("native_id"), Value: &types.FieldMemberStringValue{Value: resource.NativeID}},
		{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: resource.Stack}},
		{Name: aws.String("type"), Value: &types.FieldMemberStringValue{Value: resource.Type}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: resource.Label}},
		{Name: aws.String("target"), Value: &types.FieldMemberStringValue{Value: resource.Target}},
		{Name: aws.String("data"), Value: &types.FieldMemberStringValue{Value: string(data)}},
		{Name: aws.String("managed"), Value: &types.FieldMemberBooleanValue{Value: resource.Managed}},
		{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: resource.Ksuid}},
	}

	_, err = d.executeStatement(ctx, upsertQuery, upsertParams)
	if err != nil {
		slog.Error("failed to store resource", "error", err, "resourceURI", resource.URI())
		return "", fmt.Errorf("failed to store resource: %w", err)
	}

	return fmt.Sprintf("%s_%s", resource.Ksuid, newVersion), nil
}

func (d *DatastoreAuroraDataAPI) LoadResource(uri pkgmodel.FormaeURI) (*pkgmodel.Resource, error) {
	ctx := context.Background()

	query := `
	SELECT data, ksuid
	FROM resources
	WHERE uri = :uri
	AND operation != :operation
	ORDER BY version COLLATE "C" DESC
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("uri"), Value: &types.FieldMemberStringValue{Value: string(uri)}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, nil // Resource not found
	}

	record := output.Records[0]
	if len(record) < 2 {
		return nil, fmt.Errorf("unexpected record length: %d", len(record))
	}

	jsonData, err := getStringField(record[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse data: %w", err)
	}

	ksuid, err := getStringField(record[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse ksuid: %w", err)
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}

	resource.Ksuid = ksuid
	return &resource, nil
}

func (d *DatastoreAuroraDataAPI) LoadResourceByNativeID(nativeID string, resourceType string) (*pkgmodel.Resource, error) {
	ctx := context.Background()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE native_id = :native_id AND type = :type
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND r1.operation != :operation
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("native_id"), Value: &types.FieldMemberStringValue{Value: nativeID}},
		{Name: aws.String("type"), Value: &types.FieldMemberStringValue{Value: resourceType}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, nil // Resource not found
	}

	record := output.Records[0]
	if len(record) < 2 {
		return nil, nil
	}

	jsonData, err := getStringField(record[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse data: %w", err)
	}

	ksuid, err := getStringField(record[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse ksuid: %w", err)
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}

	resource.Ksuid = ksuid
	return &resource, nil
}

func (d *DatastoreAuroraDataAPI) LoadAllResources() ([]*pkgmodel.Resource, error) {
	ctx := context.Background()

	// Note: Large datasets may hit the 1 MiB response limit
	// Consider implementing pagination for production use
	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var resources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			continue
		}

		jsonData, err := getStringField(record[0])
		if err != nil {
			return nil, fmt.Errorf("failed to parse data: %w", err)
		}

		ksuid, err := getStringField(record[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse ksuid: %w", err)
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		resources = append(resources, &resource)
	}

	return resources, nil
}

func (d *DatastoreAuroraDataAPI) LatestLabelForResource(label string) (string, error) {
	ctx := context.Background()

	query := `
	SELECT label
	FROM resources
	WHERE label LIKE :label_pattern
	ORDER BY label DESC
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("label_pattern"), Value: &types.FieldMemberStringValue{Value: label + "%"}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}

	if len(output.Records) == 0 {
		return "", nil
	}

	return getStringField(output.Records[0][0])
}

func (d *DatastoreAuroraDataAPI) LoadResourceById(ksuid string) (*pkgmodel.Resource, error) {
	ctx := context.Background()

	query := `
	SELECT data, ksuid
	FROM resources
	WHERE ksuid = :ksuid
	AND operation != :operation
	ORDER BY version COLLATE "C" DESC
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ksuid}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, nil // Resource not found
	}

	record := output.Records[0]
	if len(record) < 2 {
		return nil, fmt.Errorf("unexpected record length: %d", len(record))
	}

	jsonData, err := getStringField(record[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse data: %w", err)
	}

	ksuidResult, err := getStringField(record[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse ksuid: %w", err)
	}

	var resource pkgmodel.Resource
	if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
		return nil, err
	}

	resource.Ksuid = ksuidResult
	return &resource, nil
}

// FindResourcesDependingOn finds resources that reference the given KSUID via $ref in their properties.
// This is essential for referential integrity — without it we risk leaving orphaned resources in an
// inconsistent state. Currently this requires a full table scan (LIKE on the data column) which will
// be slow for users with large resource counts.
// TODO: make the dependency graph discoverable from the schema so we can query edges directly.
func (d *DatastoreAuroraDataAPI) FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error) {
	ctx := context.Background()

	// Search for resources that contain a $ref to this KSUID in their properties
	// The format is: "formae://KSUID#/..." (JSON without spaces after colons)
	pattern := fmt.Sprintf("%%\"$ref\":\"formae://%s#%%", ksuid)

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE data LIKE :pattern
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("pattern"), Value: &types.FieldMemberStringValue{Value: pattern}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var resources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			return nil, fmt.Errorf("unexpected record length: %d", len(record))
		}

		jsonData, err := getStringField(record[0])
		if err != nil {
			return nil, fmt.Errorf("failed to parse data: %w", err)
		}

		ksuidResult, err := getStringField(record[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse ksuid: %w", err)
		}

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}
		resource.Ksuid = ksuidResult
		resources = append(resources, &resource)
	}

	return resources, nil
}

func (d *DatastoreAuroraDataAPI) StoreStack(stack *pkgmodel.Forma, commandID string) (string, error) {
	var lastVersionID string
	for _, resource := range stack.Resources {
		versionID, err := d.StoreResource(&resource, commandID)
		if err != nil {
			slog.Error("failed to store resource in stack", "error", err, "resourceURI", resource.URI())
			return "", err
		}
		lastVersionID = versionID
	}
	return lastVersionID, nil
}

func (d *DatastoreAuroraDataAPI) LoadStack(stackLabel string) (*pkgmodel.Forma, error) {
	ctx := context.Background()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE stack = :stack
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var stackResources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			continue
		}

		jsonData, _ := getStringField(record[0])
		ksuid, _ := getStringField(record[1])

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		if resource.Stack == stackLabel {
			stackResources = append(stackResources, &resource)
		}
	}

	if len(stackResources) == 0 {
		return nil, nil
	}

	forma := pkgmodel.FormaFromResources(stackResources)
	return forma, nil
}

func (d *DatastoreAuroraDataAPI) LoadAllStacks() ([]*pkgmodel.Forma, error) {
	ctx := context.Background()

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var allResources []*pkgmodel.Resource
	for _, record := range output.Records {
		if len(record) < 2 {
			continue
		}

		jsonData, _ := getStringField(record[0])
		ksuid, _ := getStringField(record[1])

		var resource pkgmodel.Resource
		if err := json.Unmarshal([]byte(jsonData), &resource); err != nil {
			return nil, err
		}

		resource.Ksuid = ksuid
		allResources = append(allResources, &resource)
	}

	// Group resources by stack label
	stackResourcesMap := make(map[string][]*pkgmodel.Resource)
	for _, resource := range allResources {
		if resource.Stack != "" {
			stackResourcesMap[resource.Stack] = append(stackResourcesMap[resource.Stack], resource)
		}
	}

	// Create Forma objects for each stack
	var stacks []*pkgmodel.Forma
	for _, stackResources := range stackResourcesMap {
		if len(stackResources) > 0 {
			forma := pkgmodel.FormaFromResources(stackResources)
			stacks = append(stacks, forma)
		}
	}

	return stacks, nil
}

func (d *DatastoreAuroraDataAPI) CreateTarget(target *pkgmodel.Target) (string, error) {
	ctx := context.Background()

	query := `
	INSERT INTO targets (label, version, namespace, config, discoverable)
	VALUES (:label, 1, :namespace, :config::jsonb, :discoverable)
	`

	configJSON, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: target.Label}},
		{Name: aws.String("namespace"), Value: &types.FieldMemberStringValue{Value: target.Namespace}},
		{Name: aws.String("config"), Value: &types.FieldMemberStringValue{Value: string(configJSON)}},
		{Name: aws.String("discoverable"), Value: &types.FieldMemberBooleanValue{Value: target.Discoverable}},
	}

	_, err = d.executeStatement(ctx, query, params)
	if err != nil {
		slog.Error("failed to create target", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d *DatastoreAuroraDataAPI) UpdateTarget(target *pkgmodel.Target) (string, error) {
	ctx := context.Background()

	// Get current max version
	query := `SELECT MAX(version) FROM targets WHERE label = :label`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: target.Label}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}

	if len(output.Records) == 0 || len(output.Records[0]) == 0 {
		return "", fmt.Errorf("target %s does not exist, cannot update", target.Label)
	}

	maxVersion, err := getIntField(output.Records[0][0])
	if err != nil {
		return "", err
	}
	if maxVersion == 0 {
		return "", fmt.Errorf("target %s does not exist, cannot update", target.Label)
	}

	newVersion := maxVersion + 1

	configJSON, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	insertQuery := `
	INSERT INTO targets (label, version, namespace, config, discoverable)
	VALUES (:label, :version, :namespace, :config::jsonb, :discoverable)
	`
	insertParams := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: target.Label}},
		{Name: aws.String("version"), Value: &types.FieldMemberLongValue{Value: int64(newVersion)}},
		{Name: aws.String("namespace"), Value: &types.FieldMemberStringValue{Value: target.Namespace}},
		{Name: aws.String("config"), Value: &types.FieldMemberStringValue{Value: string(configJSON)}},
		{Name: aws.String("discoverable"), Value: &types.FieldMemberBooleanValue{Value: target.Discoverable}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

func (d *DatastoreAuroraDataAPI) LoadTarget(targetLabel string) (*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	SELECT version, namespace, config, discoverable
	FROM targets
	WHERE label = :label
	ORDER BY version DESC
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: targetLabel}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, nil // Target not found
	}

	record := output.Records[0]
	if len(record) < 4 {
		return nil, fmt.Errorf("unexpected record length: %d", len(record))
	}

	version, err := getIntField(record[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse version: %w", err)
	}

	namespace, err := getStringField(record[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse namespace: %w", err)
	}

	config, err := getRawJSONField(record[2])
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	discoverable, err := getBoolField(record[3])
	if err != nil {
		return nil, fmt.Errorf("failed to parse discoverable: %w", err)
	}

	return &pkgmodel.Target{
		Label:        targetLabel,
		Namespace:    namespace,
		Config:       config,
		Discoverable: discoverable,
		Version:      version,
	}, nil
}

func (d *DatastoreAuroraDataAPI) LoadAllTargets() ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	SELECT label, version, namespace, config, discoverable
	FROM targets t1
	WHERE NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 5 {
			continue
		}

		label, err := getStringField(record[0])
		if err != nil {
			return nil, fmt.Errorf("failed to parse label: %w", err)
		}

		version, err := getIntField(record[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse version: %w", err)
		}

		namespace, err := getStringField(record[2])
		if err != nil {
			return nil, fmt.Errorf("failed to parse namespace: %w", err)
		}

		config, err := getRawJSONField(record[3])
		if err != nil {
			return nil, fmt.Errorf("failed to parse config: %w", err)
		}

		discoverable, err := getBoolField(record[4])
		if err != nil {
			return nil, fmt.Errorf("failed to parse discoverable: %w", err)
		}

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) LoadTargetsByLabels(targetNames []string) ([]*pkgmodel.Target, error) {
	if len(targetNames) == 0 {
		return []*pkgmodel.Target{}, nil
	}

	ctx := context.Background()

	// Build placeholders for the IN clause
	placeholders := make([]string, len(targetNames))
	params := []types.SqlParameter{}
	for i, name := range targetNames {
		paramName := fmt.Sprintf("label_%d", i)
		placeholders[i] = ":" + paramName
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: name},
		})
	}

	query := fmt.Sprintf(`
	SELECT t1.label, t1.version, t1.namespace, t1.config, t1.discoverable
	FROM targets t1
	WHERE t1.label IN (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`, strings.Join(placeholders, ","))

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 5 {
			continue
		}

		label, _ := getStringField(record[0])
		version, _ := getIntField(record[1])
		namespace, _ := getStringField(record[2])
		config, _ := getRawJSONField(record[3])
		discoverable, _ := getBoolField(record[4])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	WITH latest_targets AS (
		SELECT label, version, namespace, config, discoverable
		FROM targets t1
		WHERE discoverable = TRUE
		AND NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)
	)
	SELECT DISTINCT ON (config) label, version, namespace, config, discoverable
	FROM latest_targets
	ORDER BY config, version DESC
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 5 {
			continue
		}

		label, _ := getStringField(record[0])
		version, _ := getIntField(record[1])
		namespace, _ := getStringField(record[2])
		config, _ := getRawJSONField(record[3])
		discoverable, _ := getBoolField(record[4])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) QueryTargets(targetQuery *TargetQuery) ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	queryStr := `
	SELECT label, version, namespace, config, discoverable
	FROM targets t1
	WHERE NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`
	params := []types.SqlParameter{}
	paramIdx := 1

	if targetQuery.Label != nil && targetQuery.Label.Constraint != Excluded {
		paramName := fmt.Sprintf("label_%d", paramIdx)
		queryStr += fmt.Sprintf(" AND label = :%s", paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: targetQuery.Label.Item},
		})
		paramIdx++
	}

	if targetQuery.Namespace != nil && targetQuery.Namespace.Constraint != Excluded {
		paramName := fmt.Sprintf("namespace_%d", paramIdx)
		queryStr += fmt.Sprintf(" AND namespace = :%s", paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: targetQuery.Namespace.Item},
		})
		paramIdx++
	}

	if targetQuery.Discoverable != nil && targetQuery.Discoverable.Constraint != Excluded {
		paramName := fmt.Sprintf("discoverable_%d", paramIdx)
		queryStr += fmt.Sprintf(" AND discoverable = :%s", paramName)
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberBooleanValue{Value: targetQuery.Discoverable.Item},
		})
	}

	queryStr += " ORDER BY label"

	output, err := d.executeStatement(ctx, queryStr, params)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 5 {
			continue
		}

		label, _ := getStringField(record[0])
		version, _ := getIntField(record[1])
		namespace, _ := getStringField(record[2])
		config, _ := getRawJSONField(record[3])
		discoverable, _ := getBoolField(record[4])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			Discoverable: discoverable,
			Version:      version,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) Stats() (*stats.Stats, error) {
	ctx := context.Background()
	res := stats.Stats{}

	// Count distinct clients
	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", CommandsTable)
	output, err := d.executeStatement(ctx, clientsQuery, nil)
	if err != nil {
		return nil, err
	}
	if len(output.Records) > 0 && len(output.Records[0]) > 0 {
		res.Clients, _ = getIntField(output.Records[0][0])
	}

	// Count commands by type
	commandsQuery := fmt.Sprintf(`
	SELECT command, COUNT(*)
	FROM %s
	WHERE command != :sync_command
	GROUP BY command
	`, CommandsTable)
	commandsParams := []types.SqlParameter{
		{Name: aws.String("sync_command"), Value: &types.FieldMemberStringValue{Value: string(pkgmodel.CommandSync)}},
	}
	output, err = d.executeStatement(ctx, commandsQuery, commandsParams)
	if err != nil {
		return nil, err
	}

	res.Commands = make(map[string]int)
	for _, record := range output.Records {
		if len(record) >= 2 {
			command, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			res.Commands[command] = count
		}
	}

	// Count states by type
	statusQuery := fmt.Sprintf(`
	SELECT state, COUNT(*)
	FROM %s
	WHERE command != :sync_command
	GROUP BY state
	`, CommandsTable)
	output, err = d.executeStatement(ctx, statusQuery, commandsParams)
	if err != nil {
		return nil, err
	}

	res.States = make(map[string]int)
	for _, record := range output.Records {
		if len(record) >= 2 {
			state, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			res.States[state] = count
		}
	}

	// Count distinct stacks
	stacksQuery := fmt.Sprintf(`
	SELECT COUNT(DISTINCT stack)
	FROM resources r1
	WHERE stack IS NOT NULL
	AND stack != '%s'
	AND operation != :operation
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	`, constants.UnmanagedStack)
	stacksParams := []types.SqlParameter{
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}
	output, err = d.executeStatement(ctx, stacksQuery, stacksParams)
	if err != nil {
		return nil, err
	}
	if len(output.Records) > 0 && len(output.Records[0]) > 0 {
		res.Stacks, _ = getIntField(output.Records[0][0])
	}

	// Count managed resources by namespace
	res.ManagedResources = make(map[string]int)
	managedResourcesQuery := fmt.Sprintf(`
	SELECT SPLIT_PART(type, '::', 1) as namespace, COUNT(*)
	FROM resources r1
	WHERE stack IS NOT NULL
	AND stack != '%s'
	AND operation != :operation
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	GROUP BY namespace
	`, constants.UnmanagedStack)
	output, err = d.executeStatement(ctx, managedResourcesQuery, stacksParams)
	if err != nil {
		return nil, err
	}
	for _, record := range output.Records {
		if len(record) >= 2 {
			namespace, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			res.ManagedResources[namespace] = count
		}
	}

	// Count unmanaged resources by namespace
	res.UnmanagedResources = make(map[string]int)
	unmanagedResourcesQuery := fmt.Sprintf(`
	SELECT SPLIT_PART(type, '::', 1) as namespace, COUNT(*)
	FROM resources r1
	WHERE stack = '%s'
	AND operation != :operation
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	GROUP BY namespace
	`, constants.UnmanagedStack)
	output, err = d.executeStatement(ctx, unmanagedResourcesQuery, stacksParams)
	if err != nil {
		return nil, err
	}
	for _, record := range output.Records {
		if len(record) >= 2 {
			namespace, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			res.UnmanagedResources[namespace] = count
		}
	}

	// Count targets by namespace
	res.Targets = make(map[string]int)
	targetsQuery := `
	SELECT namespace, COUNT(*)
	FROM targets t1
	WHERE NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	GROUP BY namespace
	`
	output, err = d.executeStatement(ctx, targetsQuery, nil)
	if err != nil {
		return nil, err
	}
	for _, record := range output.Records {
		if len(record) >= 2 {
			namespace, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			res.Targets[namespace] = count
		}
	}

	// Count resource types
	res.ResourceTypes = make(map[string]int)
	resourceTypesQuery := `
	SELECT type, COUNT(*)
	FROM resources r1
	WHERE operation != :operation
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	GROUP BY type
	`
	output, err = d.executeStatement(ctx, resourceTypesQuery, stacksParams)
	if err != nil {
		return nil, err
	}
	for _, record := range output.Records {
		if len(record) >= 2 {
			resourceType, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			res.ResourceTypes[resourceType] = count
		}
	}

	// Count resource errors by resource type
	res.ResourceErrors = make(map[string]int)
	errorQuery := `
	SELECT resource::jsonb->>'Type' as resource_type, COUNT(*)
	FROM resource_updates
	WHERE state = :state
	AND resource IS NOT NULL
	GROUP BY resource_type
	`
	errorParams := []types.SqlParameter{
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(metaTypes.ResourceUpdateStateFailed)}},
	}
	output, err = d.executeStatement(ctx, errorQuery, errorParams)
	if err != nil {
		return nil, err
	}
	for _, record := range output.Records {
		if len(record) >= 2 {
			resourceType, _ := getStringField(record[0])
			count, _ := getIntField(record[1])
			if resourceType != "" {
				res.ResourceErrors[resourceType] = count
			}
		}
	}

	return &res, nil
}

func (d *DatastoreAuroraDataAPI) GetKSUIDByTriplet(stack, label, resourceType string) (string, error) {
	ctx := context.Background()

	query := `
	SELECT ksuid
	FROM resources
	WHERE stack = :stack AND label = :label AND LOWER(type) = LOWER(:type)
	AND operation != :operation
	ORDER BY version COLLATE "C" DESC
	LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack"), Value: &types.FieldMemberStringValue{Value: stack}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("type"), Value: &types.FieldMemberStringValue{Value: resourceType}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}

	if len(output.Records) == 0 {
		return "", nil
	}

	return getStringField(output.Records[0][0])
}

func (d *DatastoreAuroraDataAPI) BatchGetKSUIDsByTriplets(triplets []pkgmodel.TripletKey) (map[pkgmodel.TripletKey]string, error) {
	if len(triplets) == 0 {
		return make(map[pkgmodel.TripletKey]string), nil
	}

	ctx := context.Background()

	// Build placeholders for the IN clause
	placeholders := make([]string, len(triplets))
	params := []types.SqlParameter{}
	for i, triplet := range triplets {
		stackParam := fmt.Sprintf("stack_%d", i)
		labelParam := fmt.Sprintf("label_%d", i)
		typeParam := fmt.Sprintf("type_%d", i)
		placeholders[i] = fmt.Sprintf("(:%s, :%s, :%s)", stackParam, labelParam, typeParam)
		params = append(params,
			types.SqlParameter{Name: aws.String(stackParam), Value: &types.FieldMemberStringValue{Value: triplet.Stack}},
			types.SqlParameter{Name: aws.String(labelParam), Value: &types.FieldMemberStringValue{Value: triplet.Label}},
			types.SqlParameter{Name: aws.String(typeParam), Value: &types.FieldMemberStringValue{Value: triplet.Type}},
		)
	}

	query := fmt.Sprintf(`
	SELECT stack, label, type, ksuid
	FROM resources r1
	WHERE (stack, label, type) IN (%s)
	AND r1.operation != :op_delete
	AND NOT EXISTS (
		SELECT 1 FROM resources r2
		WHERE r1.stack = r2.stack AND r1.label = r2.label AND r1.type = r2.type
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	`, strings.Join(placeholders, ","))

	params = append(params, types.SqlParameter{
		Name:  aws.String("op_delete"),
		Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)},
	})

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	result := make(map[pkgmodel.TripletKey]string)
	for _, record := range output.Records {
		if len(record) < 4 {
			continue
		}
		stack, _ := getStringField(record[0])
		label, _ := getStringField(record[1])
		resourceType, _ := getStringField(record[2])
		ksuid, _ := getStringField(record[3])

		key := pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}
		result[key] = ksuid
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) BatchGetTripletsByKSUIDs(ksuids []string) (map[string]pkgmodel.TripletKey, error) {
	if len(ksuids) == 0 {
		return make(map[string]pkgmodel.TripletKey), nil
	}

	ctx := context.Background()

	// Build placeholders for the IN clause
	placeholders := make([]string, len(ksuids))
	params := []types.SqlParameter{}
	for i, ksuid := range ksuids {
		paramName := fmt.Sprintf("ksuid_%d", i)
		placeholders[i] = ":" + paramName
		params = append(params, types.SqlParameter{
			Name: aws.String(paramName), Value: &types.FieldMemberStringValue{Value: ksuid},
		})
	}

	query := fmt.Sprintf(`
	WITH latest_resources AS (
		SELECT ksuid, stack, label, type, managed, version,
		       ROW_NUMBER() OVER (PARTITION BY ksuid ORDER BY managed DESC, version COLLATE "C" DESC) as rn
		FROM resources
		WHERE ksuid IN (%s)
		AND operation != :operation
	)
	SELECT ksuid, stack, label, type
	FROM latest_resources
	WHERE rn = 1
	`, strings.Join(placeholders, ","))

	params = append(params, types.SqlParameter{
		Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)},
	})

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	result := make(map[string]pkgmodel.TripletKey)
	for _, record := range output.Records {
		if len(record) < 4 {
			continue
		}
		ksuid, _ := getStringField(record[0])
		stack, _ := getStringField(record[1])
		label, _ := getStringField(record[2])
		resourceType, _ := getStringField(record[3])

		result[ksuid] = pkgmodel.TripletKey{Stack: stack, Label: label, Type: resourceType}
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) BulkStoreResourceUpdates(commandID string, updates []resource_update.ResourceUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	ctx := context.Background()

	// Start transaction
	txID, err := d.beginTransaction(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	for _, ru := range updates {
		resourceJSON, _ := json.Marshal(ru.DesiredState)
		resourceTargetJSON, _ := json.Marshal(ru.ResourceTarget)
		existingResourceJSON, _ := json.Marshal(ru.PriorState)
		existingTargetJSON, _ := json.Marshal(ru.ExistingTarget)
		progressResultJSON, _ := json.Marshal(ru.ProgressResult)
		mostRecentProgressJSON, _ := json.Marshal(ru.MostRecentProgressResult)
		remainingResolvablesJSON, _ := json.Marshal(ru.RemainingResolvables)
		referenceLabelsJSON, _ := json.Marshal(ru.ReferenceLabels)
		previousPropertiesJSON, _ := json.Marshal(ru.PreviousProperties)

		query := `
			INSERT INTO resource_updates (
				command_id, ksuid, operation, state, start_ts, modified_ts,
				retries, remaining, version, stack_label, group_id, source,
				resource, resource_target, existing_resource, existing_target,
				progress_result, most_recent_progress,
				remaining_resolvables, reference_labels, previous_properties
			) VALUES (:command_id, :ksuid, :operation, :state, :start_ts::timestamp, :modified_ts::timestamp,
				:retries, :remaining, :version, :stack_label, :group_id, :source,
				:resource, :resource_target, :existing_resource, :existing_target,
				:progress_result, :most_recent_progress,
				:remaining_resolvables, :reference_labels, :previous_properties)
			ON CONFLICT (command_id, ksuid, operation) DO UPDATE SET
				state = EXCLUDED.state,
				modified_ts = EXCLUDED.modified_ts
		`

		params := []types.SqlParameter{
			{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
			{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ru.DesiredState.Ksuid}},
			{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(ru.Operation)}},
			{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(ru.State)}},
			{Name: aws.String("start_ts"), Value: &types.FieldMemberStringValue{Value: ru.StartTs.UTC().Format(time.RFC3339Nano)}},
			{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: ru.ModifiedTs.UTC().Format(time.RFC3339Nano)}},
			{Name: aws.String("retries"), Value: &types.FieldMemberLongValue{Value: int64(ru.Retries)}},
			{Name: aws.String("remaining"), Value: &types.FieldMemberLongValue{Value: int64(ru.Remaining)}},
			{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: ru.Version}},
			{Name: aws.String("stack_label"), Value: &types.FieldMemberStringValue{Value: ru.StackLabel}},
			{Name: aws.String("group_id"), Value: &types.FieldMemberStringValue{Value: ru.GroupID}},
			{Name: aws.String("source"), Value: &types.FieldMemberStringValue{Value: string(ru.Source)}},
			{Name: aws.String("resource"), Value: &types.FieldMemberStringValue{Value: string(resourceJSON)}},
			{Name: aws.String("resource_target"), Value: &types.FieldMemberStringValue{Value: string(resourceTargetJSON)}},
			{Name: aws.String("existing_resource"), Value: &types.FieldMemberStringValue{Value: string(existingResourceJSON)}},
			{Name: aws.String("existing_target"), Value: &types.FieldMemberStringValue{Value: string(existingTargetJSON)}},
			{Name: aws.String("progress_result"), Value: &types.FieldMemberStringValue{Value: string(progressResultJSON)}},
			{Name: aws.String("most_recent_progress"), Value: &types.FieldMemberStringValue{Value: string(mostRecentProgressJSON)}},
			{Name: aws.String("remaining_resolvables"), Value: &types.FieldMemberStringValue{Value: string(remainingResolvablesJSON)}},
			{Name: aws.String("reference_labels"), Value: &types.FieldMemberStringValue{Value: string(referenceLabelsJSON)}},
			{Name: aws.String("previous_properties"), Value: &types.FieldMemberStringValue{Value: string(previousPropertiesJSON)}},
		}

		_, err := d.executeStatementInTransaction(ctx, txID, query, params)
		if err != nil {
			_ = d.rollbackTransaction(ctx, txID)
			return fmt.Errorf("failed to store resource update: %w", err)
		}
	}

	if err := d.commitTransaction(ctx, txID); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (d *DatastoreAuroraDataAPI) LoadResourceUpdates(commandID string) ([]resource_update.ResourceUpdate, error) {
	ctx := context.Background()

	query := `
	SELECT ksuid, operation, state, start_ts, modified_ts,
		retries, remaining, version, stack_label, group_id, source,
		resource, resource_target, existing_resource, existing_target,
		progress_result, most_recent_progress,
		remaining_resolvables, reference_labels, previous_properties
	FROM resource_updates
	WHERE command_id = :command_id
	ORDER BY ksuid ASC
	`
	params := []types.SqlParameter{
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var updates []resource_update.ResourceUpdate
	for _, record := range output.Records {
		if len(record) < 20 {
			continue
		}

		ksuid, _ := getStringField(record[0])
		operation, _ := getStringField(record[1])
		state, _ := getStringField(record[2])
		startTs, _ := getTimestampField(record[3])
		modifiedTs, _ := getTimestampField(record[4])
		retries, _ := getIntField(record[5])
		remaining, _ := getIntField(record[6])
		version, _ := getStringField(record[7])
		stackLabel, _ := getStringField(record[8])
		groupID, _ := getStringField(record[9])
		source, _ := getStringField(record[10])
		resourceJSON, _ := getStringField(record[11])
		resourceTargetJSON, _ := getStringField(record[12])
		existingResourceJSON, _ := getStringField(record[13])
		existingTargetJSON, _ := getStringField(record[14])
		progressResultJSON, _ := getStringField(record[15])
		mostRecentProgressJSON, _ := getStringField(record[16])
		remainingResolvablesJSON, _ := getStringField(record[17])
		referenceLabelsJSON, _ := getStringField(record[18])
		previousPropertiesJSON, _ := getStringField(record[19])

		var desiredState pkgmodel.Resource
		var resourceTarget pkgmodel.Target
		var priorState pkgmodel.Resource
		var existingTarget pkgmodel.Target
		var progressResult []plugin.TrackedProgress
		var mostRecentProgress plugin.TrackedProgress
		var remainingResolvables []pkgmodel.FormaeURI
		var referenceLabels map[string]string
		var previousProperties json.RawMessage

		_ = json.Unmarshal([]byte(resourceJSON), &desiredState)
		_ = json.Unmarshal([]byte(resourceTargetJSON), &resourceTarget)
		_ = json.Unmarshal([]byte(existingResourceJSON), &priorState)
		_ = json.Unmarshal([]byte(existingTargetJSON), &existingTarget)
		_ = json.Unmarshal([]byte(progressResultJSON), &progressResult)
		_ = json.Unmarshal([]byte(mostRecentProgressJSON), &mostRecentProgress)
		_ = json.Unmarshal([]byte(remainingResolvablesJSON), &remainingResolvables)
		_ = json.Unmarshal([]byte(referenceLabelsJSON), &referenceLabels)
		_ = json.Unmarshal([]byte(previousPropertiesJSON), &previousProperties)
		if string(previousProperties) == "null" {
			previousProperties = nil
		}

		desiredState.Ksuid = ksuid

		updates = append(updates, resource_update.ResourceUpdate{
			DesiredState:             desiredState,
			Operation:                metaTypes.OperationType(operation),
			State:                    resource_update.ResourceUpdateState(state),
			StartTs:                  startTs,
			ModifiedTs:               modifiedTs,
			Retries:                  uint16(retries),
			Remaining:                int16(remaining),
			Version:                  version,
			StackLabel:               stackLabel,
			GroupID:                  groupID,
			Source:                   resource_update.FormaCommandSource(source),
			ResourceTarget:           resourceTarget,
			PriorState:               priorState,
			ExistingTarget:           existingTarget,
			ProgressResult:           progressResult,
			MostRecentProgressResult: mostRecentProgress,
			RemainingResolvables:     remainingResolvables,
			ReferenceLabels:          referenceLabels,
			PreviousProperties:       previousProperties,
		})
	}

	return updates, nil
}

func (d *DatastoreAuroraDataAPI) UpdateResourceUpdateState(commandID string, ksuid string, operation metaTypes.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	ctx := context.Background()

	query := `
	UPDATE resource_updates
	SET state = :state, modified_ts = :modified_ts::timestamp
	WHERE command_id = :command_id AND ksuid = :ksuid AND operation = :operation
	`
	params := []types.SqlParameter{
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(state)}},
		{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTs.UTC().Format(time.RFC3339Nano)}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ksuid}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(operation)}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to update resource update state: %w", err)
	}

	if output.NumberOfRecordsUpdated == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}

	return nil
}

func (d *DatastoreAuroraDataAPI) UpdateResourceUpdateProgress(commandID string, ksuid string, operation metaTypes.OperationType, state resource_update.ResourceUpdateState, modifiedTs time.Time, progress plugin.TrackedProgress) error {
	ctx := context.Background()

	// First, load existing progress results to append to
	loadQuery := `SELECT progress_result FROM resource_updates WHERE command_id = :command_id AND ksuid = :ksuid AND operation = :operation`
	loadParams := []types.SqlParameter{
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ksuid}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(operation)}},
	}

	output, err := d.executeStatement(ctx, loadQuery, loadParams)
	if err != nil {
		return fmt.Errorf("failed to load existing progress: %w", err)
	}

	var existingProgress []plugin.TrackedProgress
	if len(output.Records) > 0 && len(output.Records[0]) > 0 {
		progressJSON, _ := getStringField(output.Records[0][0])
		if progressJSON != "" {
			_ = json.Unmarshal([]byte(progressJSON), &existingProgress)
		}
	}

	// Append new progress
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
	SET state = :state, modified_ts = :modified_ts::timestamp, progress_result = :progress_result, most_recent_progress = :most_recent_progress
	WHERE command_id = :command_id AND ksuid = :ksuid AND operation = :operation
	`
	updateParams := []types.SqlParameter{
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(state)}},
		{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTs.UTC().Format(time.RFC3339Nano)}},
		{Name: aws.String("progress_result"), Value: &types.FieldMemberStringValue{Value: string(progressJSON)}},
		{Name: aws.String("most_recent_progress"), Value: &types.FieldMemberStringValue{Value: string(mostRecentJSON)}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ksuid}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(operation)}},
	}

	updateOutput, err := d.executeStatement(ctx, updateQuery, updateParams)
	if err != nil {
		return fmt.Errorf("failed to update resource update progress: %w", err)
	}

	if updateOutput.NumberOfRecordsUpdated == 0 {
		return fmt.Errorf("resource update not found: command_id=%s, ksuid=%s, operation=%s", commandID, ksuid, operation)
	}

	return nil
}

func (d *DatastoreAuroraDataAPI) BatchUpdateResourceUpdateState(commandID string, refs []ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
	if len(refs) == 0 {
		return nil
	}

	ctx := context.Background()

	// Start transaction
	txID, err := d.beginTransaction(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	for _, ref := range refs {
		query := `
		UPDATE resource_updates
		SET state = :state, modified_ts = :modified_ts::timestamp
		WHERE command_id = :command_id AND ksuid = :ksuid AND operation = :operation
		`
		params := []types.SqlParameter{
			{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(state)}},
			{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTs.UTC().Format(time.RFC3339Nano)}},
			{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
			{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ref.KSUID}},
			{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(ref.Operation)}},
		}

		_, err := d.executeStatementInTransaction(ctx, txID, query, params)
		if err != nil {
			_ = d.rollbackTransaction(ctx, txID)
			return fmt.Errorf("failed to update resource update: %w", err)
		}
	}

	if err := d.commitTransaction(ctx, txID); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (d *DatastoreAuroraDataAPI) UpdateFormaCommandProgress(commandID string, state forma_command.CommandState, modifiedTs time.Time) error {
	ctx := context.Background()

	query := `UPDATE forma_commands SET state = :state, modified_ts = :modified_ts::timestamp WHERE command_id = :command_id`
	params := []types.SqlParameter{
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(state)}},
		{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTs.UTC().Format(time.RFC3339Nano)}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to update forma command meta: %w", err)
	}

	if output.NumberOfRecordsUpdated == 0 {
		return fmt.Errorf("forma command not found: %s", commandID)
	}

	return nil
}

func (d *DatastoreAuroraDataAPI) CreateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	ctx := context.Background()

	// Check if a non-deleted stack with this label already exists
	existing, err := d.GetStackByLabel(stack.Label)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return "", fmt.Errorf("stack already exists: %s", stack.Label)
	}

	// Generate KSUIDs for both id (stable identifier) and version (ordering)
	id := mksuid.New().String()
	version := mksuid.New().String()

	query := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (:id, :version, :command_id, :operation, :label, :description)`
	params := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "create"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: stack.Label}},
		{Name: aws.String("description"), Value: &types.FieldMemberStringValue{Value: stack.Description}},
	}

	_, err = d.executeStatement(ctx, query, params)
	if err != nil {
		slog.Error("Failed to create stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d *DatastoreAuroraDataAPI) UpdateStack(stack *pkgmodel.Stack, commandID string) (string, error) {
	ctx := context.Background()

	// Get the existing stack to find its id (most recent version of any operation)
	// Note: Use COLLATE "C" for binary ordering of KSUID strings (en_US.utf8 collation breaks ASCII ordering)
	query := `
		SELECT id, operation FROM stacks
		WHERE label = :label
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: stack.Label}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}

	if len(output.Records) == 0 {
		return "", fmt.Errorf("stack not found: %s", stack.Label)
	}

	id, err := getStringField(output.Records[0][0])
	if err != nil {
		return "", err
	}
	operation, err := getStringField(output.Records[0][1])
	if err != nil {
		return "", err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", stack.Label)
	}

	// Insert new version with same id
	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (:id, :version, :command_id, :operation, :label, :description)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "update"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: stack.Label}},
		{Name: aws.String("description"), Value: &types.FieldMemberStringValue{Value: stack.Description}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		slog.Error("Failed to update stack", "error", err, "label", stack.Label)
		return "", err
	}

	return version, nil
}

func (d *DatastoreAuroraDataAPI) DeleteStack(label string, commandID string) (string, error) {
	ctx := context.Background()

	// Get the existing stack to find its id (most recent version of any operation)
	// Note: Use COLLATE "C" for binary ordering of KSUID strings (en_US.utf8 collation breaks ASCII ordering)
	query := `
		SELECT id, operation FROM stacks
		WHERE label = :label
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}

	if len(output.Records) == 0 {
		return "", fmt.Errorf("stack not found: %s", label)
	}

	id, err := getStringField(output.Records[0][0])
	if err != nil {
		return "", err
	}
	operation, err := getStringField(output.Records[0][1])
	if err != nil {
		return "", err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return "", fmt.Errorf("stack not found: %s", label)
	}

	// Insert tombstone version
	version := mksuid.New().String()
	insertQuery := `INSERT INTO stacks (id, version, command_id, operation, label, description) VALUES (:id, :version, :command_id, :operation, :label, :description)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "delete"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
		{Name: aws.String("description"), Value: &types.FieldMemberStringValue{Value: ""}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		slog.Error("Failed to delete stack", "error", err, "label", label)
		return "", err
	}

	return version, nil
}

func (d *DatastoreAuroraDataAPI) GetStackByLabel(label string) (*pkgmodel.Stack, error) {
	ctx := context.Background()

	// Get the latest version of the stack, return nil if deleted
	// We need to check if the MOST RECENT version is a delete operation
	// Note: Use COLLATE "C" for binary ordering of KSUID strings (en_US.utf8 collation breaks ASCII ordering)
	query := `
		SELECT id, description, operation FROM stacks
		WHERE label = :label
		ORDER BY version COLLATE "C" DESC
		LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(output.Records) == 0 {
		return nil, nil // Stack not found, return nil without error
	}

	id, err := getStringField(output.Records[0][0])
	if err != nil {
		return nil, err
	}
	description, err := getStringField(output.Records[0][1])
	if err != nil {
		return nil, err
	}
	operation, err := getStringField(output.Records[0][2])
	if err != nil {
		return nil, err
	}

	// If the most recent version is a delete, the stack doesn't exist
	if operation == "delete" {
		return nil, nil
	}

	return &pkgmodel.Stack{
		ID:          id,
		Label:       label,
		Description: description,
	}, nil
}

func (d *DatastoreAuroraDataAPI) ListAllStacks() ([]*pkgmodel.Stack, error) {
	ctx := context.Background()

	// Get all stacks at their latest version that aren't deleted
	// Uses window function to reliably get the most recent version per stack id
	// Note: Use COLLATE "C" for binary ordering of KSUID strings (en_US.utf8 collation breaks ASCII ordering)
	query := `
		SELECT id, label, description FROM (
			SELECT id, label, description, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		) sub
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var stacks []*pkgmodel.Stack
	for _, record := range output.Records {
		if len(record) < 3 {
			continue
		}

		id, err := getStringField(record[0])
		if err != nil {
			return nil, err
		}
		label, err := getStringField(record[1])
		if err != nil {
			return nil, err
		}
		description, err := getStringField(record[2])
		if err != nil {
			return nil, err
		}

		stacks = append(stacks, &pkgmodel.Stack{
			ID:          id,
			Label:       label,
			Description: description,
		})
	}

	return stacks, nil
}

func (d *DatastoreAuroraDataAPI) DeleteTarget(targetLabel string) (string, error) {
	ctx := context.Background()

	// Hard delete all versions of the target
	query := `DELETE FROM targets WHERE label = :label`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: targetLabel}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		slog.Error("Failed to delete target", "error", err, "label", targetLabel)
		return "", err
	}

	if output.NumberOfRecordsUpdated == 0 {
		return "", fmt.Errorf("target %s does not exist, cannot delete", targetLabel)
	}

	return fmt.Sprintf("%s_deleted", targetLabel), nil
}

func (d *DatastoreAuroraDataAPI) Close() {
	// Data API is stateless - no persistent connections to close
	slog.Info("Closed Aurora Data API datastore")
}

// This can be only used in tests or in setups where we have access to admin (non-production)
func (d *DatastoreAuroraDataAPI) CleanUp() error {
	ctx := context.Background()

	tables := []string{"stacks", "resource_updates", "resources", "targets", "forma_commands"}

	for _, table := range tables {
		query := fmt.Sprintf("DELETE FROM %s", table)
		_, err := d.executeStatement(ctx, query, nil)
		if err != nil {
			// Table might not exist, continue
			slog.Debug("Failed to clean table", "table", table, "error", err)
		}
	}

	return nil
}
