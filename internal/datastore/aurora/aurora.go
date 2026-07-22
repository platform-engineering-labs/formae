// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package aurora

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
	"github.com/platform-engineering-labs/formae/internal/datastore"
	metaConfig "github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stats"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	metaTypes "github.com/platform-engineering-labs/formae/internal/metastructure/types"
	metautil "github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func init() {
	// Register Aurora Data API factory with the datastore extension registry
	datastore.DefaultRegistry.Register("auroradataapi", func(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
		return NewDatastoreAuroraDataAPI(ctx, cfg, agentID)
	})
}

// appendAuroraStringClause appends a WHERE clause for a string-valued query
// item (with optional multi-value via ExtraItems and `*` wildcards). column
// is the SQL column name; paramPrefix is used for the named-parameter labels
// (`:<paramPrefix>_N`). lowerWrap wraps both column and placeholder in
// LOWER() for case-insensitive matching. Returns the extended query string,
// extended params, and the next paramIdx.
func appendAuroraStringClause(
	queryStr string,
	params []types.SqlParameter,
	paramIdx int,
	column, paramPrefix string,
	lowerWrap bool,
	qi *datastore.QueryItem[string],
) (string, []types.SqlParameter, int) {
	if qi == nil {
		return queryStr, params, paramIdx
	}

	values := append([]string{qi.Item}, qi.ExtraItems...)
	isExcluded := qi.Constraint == datastore.Excluded

	clauses := make([]string, 0, len(values))
	for _, v := range values {
		op, operand, _ := auroraOpAndOperand(v, isExcluded)
		paramName := fmt.Sprintf("%s_%d", paramPrefix, paramIdx)
		paramIdx++

		lhs := column
		rhs := ":" + paramName
		if lowerWrap {
			lhs = "LOWER(" + lhs + ")"
			rhs = "LOWER(" + rhs + ")"
		}
		clauses = append(clauses, fmt.Sprintf("%s %s %s", lhs, op, rhs))
		params = append(params, types.SqlParameter{
			Name:  aws.String(paramName),
			Value: &types.FieldMemberStringValue{Value: operand},
		})
	}

	glue := " OR "
	if isExcluded {
		glue = " AND "
	}
	if len(clauses) == 1 {
		queryStr += " AND " + clauses[0]
	} else {
		queryStr += " AND (" + strings.Join(clauses, glue) + ")"
	}
	return queryStr, params, paramIdx
}

// appendAuroraExistsClause appends a WHERE clause whose check is wrapped in
// an EXISTS sub-query — used for `stack:` filtering against resource_updates
// in QueryFormaCommands. existsBody is the SQL between EXISTS ( and the
// `<op> :<param>)` tail. Multi-value support emits multiple OR'd EXISTS
// sub-queries; for typical single-value queries it produces one.
func appendAuroraExistsClause(
	queryStr string,
	params []types.SqlParameter,
	paramIdx int,
	existsBody, paramPrefix string,
	qi *datastore.QueryItem[string],
) (string, []types.SqlParameter, int) {
	if qi == nil {
		return queryStr, params, paramIdx
	}
	values := append([]string{qi.Item}, qi.ExtraItems...)
	isExcluded := qi.Constraint == datastore.Excluded

	clauses := make([]string, 0, len(values))
	for _, v := range values {
		op, operand, _ := auroraOpAndOperand(v, isExcluded)
		paramName := fmt.Sprintf("%s_%d", paramPrefix, paramIdx)
		paramIdx++
		clauses = append(clauses, fmt.Sprintf("EXISTS (%s %s :%s)", existsBody, op, paramName))
		params = append(params, types.SqlParameter{
			Name:  aws.String(paramName),
			Value: &types.FieldMemberStringValue{Value: operand},
		})
	}

	glue := " OR "
	if isExcluded {
		glue = " AND "
	}
	if len(clauses) == 1 {
		queryStr += " AND " + clauses[0]
	} else {
		queryStr += " AND (" + strings.Join(clauses, glue) + ")"
	}
	return queryStr, params, paramIdx
}

// appendAuroraBoolClause appends a WHERE clause for a bool-valued query item.
// Bool fields don't support multi-value or wildcards, so this is the simple
// equality/inequality form. Returns the extended query string, extended
// params, and the next paramIdx.
func appendAuroraBoolClause(
	queryStr string,
	params []types.SqlParameter,
	paramIdx int,
	column, paramPrefix string,
	qi *datastore.QueryItem[bool],
) (string, []types.SqlParameter, int) {
	if qi == nil {
		return queryStr, params, paramIdx
	}
	op := "="
	if qi.Constraint == datastore.Excluded {
		op = "!="
	}
	paramName := fmt.Sprintf("%s_%d", paramPrefix, paramIdx)
	queryStr += fmt.Sprintf(" AND %s %s :%s", column, op, paramName)
	params = append(params, types.SqlParameter{
		Name:  aws.String(paramName),
		Value: &types.FieldMemberBooleanValue{Value: qi.Item},
	})
	return queryStr, params, paramIdx + 1
}

// auroraOpAndOperand resolves the SQL operator and bound value for one
// string term, accounting for exclusion and `*` wildcards. Any `*` in the
// value (anywhere) flips the operator to LIKE and translates to `%`.
func auroraOpAndOperand(s string, isExcluded bool) (op string, operand string, isLike bool) {
	if !strings.Contains(s, "*") {
		if isExcluded {
			return "!=", s, false
		}
		return "=", s, false
	}
	likeOp := "LIKE"
	if isExcluded {
		likeOp = "NOT LIKE"
	}
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "%", "\\%")
	escaped = strings.ReplaceAll(escaped, "_", "\\_")
	return likeOp, strings.ReplaceAll(escaped, "*", "%"), true
}

type DatastoreAuroraDataAPI struct {
	client     *rdsdata.Client
	clusterARN string
	secretARN  string
	database   string
	agentID    string
	ctx        context.Context
}

func NewDatastoreAuroraDataAPI(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
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

	// Run pending migrations.
	// Each migration runs inside a transaction so that TEMP tables and other
	// session-scoped objects survive across the individual SQL statements
	// (Aurora Data API creates a new session per ExecuteStatement call).
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		slog.Info("Running migration", "version", m.version, "name", m.name)

		txID, err := d.beginTransaction(ctx)
		if err != nil {
			return fmt.Errorf("migration %d: failed to begin transaction: %w", m.version, err)
		}

		// Execute migration statements within the transaction
		for _, stmt := range m.upStatements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}

			_, err := d.executeStatementInTransaction(ctx, txID, stmt, nil)
			if err != nil {
				_ = d.rollbackTransaction(ctx, txID)
				return fmt.Errorf("migration %d failed on statement: %w", m.version, err)
			}
		}

		if err := d.commitTransaction(ctx, txID); err != nil {
			_ = d.rollbackTransaction(ctx, txID)
			return fmt.Errorf("migration %d: failed to commit transaction: %w", m.version, err)
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
	entries, err := datastore.EmbedMigrationsPostgres.ReadDir("migrations_postgres")
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

		content, err := datastore.EmbedMigrationsPostgres.ReadFile("migrations_postgres/" + entry.Name())
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

// unmarshalConfigSchema extracts and unmarshals a nullable JSONB config_schema field.
func unmarshalConfigSchema(field types.Field) (pkgmodel.ConfigSchema, error) {
	raw, err := getRawJSONField(field)
	if err != nil {
		return pkgmodel.ConfigSchema{}, err
	}
	if raw == nil {
		return pkgmodel.ConfigSchema{}, nil
	}
	var cs pkgmodel.ConfigSchema
	if err := json.Unmarshal(raw, &cs); err != nil {
		return pkgmodel.ConfigSchema{}, fmt.Errorf("failed to unmarshal config schema: %w", err)
	}
	return cs, nil
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

	policyUpdatesJSON, err := json.Marshal(fa.PolicyUpdates)
	if err != nil {
		return fmt.Errorf("failed to marshal policy updates: %w", err)
	}

	query := fmt.Sprintf(`
	INSERT INTO %s (command_id, timestamp, command, state, agent_version, client_id, agent_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, policy_updates, modified_ts)
	VALUES (:command_id, :timestamp::timestamp, :command, :state, :agent_version, :client_id, :agent_id,
		:description_text, :description_confirm, :config_mode, :config_force, :config_simulate,
		:target_updates, :stack_updates, :policy_updates, :modified_ts::timestamp)
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
	policy_updates = EXCLUDED.policy_updates,
	modified_ts = EXCLUDED.modified_ts
	`, datastore.CommandsTable)

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
		{Name: aws.String("policy_updates"), Value: &types.FieldMemberStringValue{Value: string(policyUpdatesJSON)}},
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
		target_updates, stack_updates, policy_updates, modified_ts
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
		target_updates, stack_updates, policy_updates, modified_ts
	FROM forma_commands
	WHERE command != :sync_command AND state IN (:state_not_started, :state_in_progress)
	ORDER BY timestamp DESC
	`
	params := []types.SqlParameter{
		{Name: aws.String("sync_command"), Value: &types.FieldMemberStringValue{Value: string(pkgmodel.CommandSync)}},
		{Name: aws.String("state_not_started"), Value: &types.FieldMemberStringValue{Value: string(forma_command.CommandStateNotStarted)}},
		{Name: aws.String("state_in_progress"), Value: &types.FieldMemberStringValue{Value: string(forma_command.CommandStateInProgress)}},
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
	policyUpdatesJSON, _ := getStringField(record[12])
	modifiedTs, _ := getTimestampField(record[13])

	var targetUpdates []target_update.TargetUpdate
	if targetUpdatesJSON != "" {
		_ = json.Unmarshal([]byte(targetUpdatesJSON), &targetUpdates)
	}

	var stackUpdates []stack_update.StackUpdate
	if stackUpdatesJSON != "" {
		_ = json.Unmarshal([]byte(stackUpdatesJSON), &stackUpdates)
	}

	var policyUpdates []policy_update.PolicyUpdate
	if policyUpdatesJSON != "" {
		_ = json.Unmarshal([]byte(policyUpdatesJSON), &policyUpdates)
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
		PolicyUpdates: policyUpdates,
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
	deleteCommandQuery := fmt.Sprintf("DELETE FROM %s WHERE command_id = :command_id", datastore.CommandsTable)
	_, err = d.executeStatement(ctx, deleteCommandQuery, deleteUpdatesParams)
	return err
}

func (d *DatastoreAuroraDataAPI) GetFormaCommandByCommandID(commandID string) (*forma_command.FormaCommand, error) {
	ctx := context.Background()

	query := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, policy_updates, modified_ts
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
		target_updates, stack_updates, policy_updates, modified_ts
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

func (d *DatastoreAuroraDataAPI) GetResourceModificationsSinceLastReconcile(stack string) ([]datastore.ResourceModification, error) {
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

	modifications := make(map[datastore.ResourceModification]struct{})
	for _, record := range output.Records {
		if len(record) < 3 {
			continue
		}
		resourceType, _ := getStringField(record[0])
		label, _ := getStringField(record[1])
		operation, _ := getStringField(record[2])
		modifications[datastore.ResourceModification{Stack: stack, Type: resourceType, Label: label, Operation: operation}] = struct{}{}
	}

	result := make([]datastore.ResourceModification, 0, len(modifications))
	for mod := range modifications {
		result = append(result, mod)
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) QueryFormaCommands(statusQuery *datastore.StatusQuery) ([]*forma_command.FormaCommand, error) {
	ctx := context.Background()

	// Build query with dynamic filters
	queryStr := `
	SELECT command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, policy_updates, modified_ts
	FROM forma_commands
	WHERE 1=1
	`
	params := []types.SqlParameter{}
	paramIdx := 1

	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "command_id", "command_id", false, statusQuery.CommandID)
	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "client_id", "client_id", false, statusQuery.ClientID)

	if statusQuery.Command != nil {
		queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "command", "command", true, statusQuery.Command)
	} else {
		queryStr += fmt.Sprintf(" AND command != '%s'", pkgmodel.CommandSync)
	}

	// stack filter routes through a sub-EXISTS against resource_updates.
	if statusQuery.Stack != nil {
		queryStr, params, paramIdx = appendAuroraExistsClause(
			queryStr, params, paramIdx,
			"SELECT 1 FROM resource_updates ru WHERE ru.command_id = forma_commands.command_id AND ru.stack_label",
			"stack",
			statusQuery.Stack,
		)
	}

	queryStr, params, _ = appendAuroraStringClause(queryStr, params, paramIdx, "state", "status", true, statusQuery.Status)

	queryStr += " ORDER BY timestamp DESC"

	limit := datastore.DefaultFormaCommandsQueryLimit
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

func (d *DatastoreAuroraDataAPI) QueryResources(query *datastore.ResourceQuery) ([]*pkgmodel.Resource, error) {
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
	paramIdx := 1

	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "native_id", "native_id", false, query.NativeID)
	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "stack", "stack", false, query.Stack)
	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "type", "type", true, query.Type)
	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "label", "label", false, query.Label)
	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "target", "target", false, query.Target)
	queryStr, params, _ = appendAuroraBoolClause(queryStr, params, paramIdx, "managed", "managed", query.Managed)

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
			AND r2.version COLLATE "C" > r1.version COLLATE "C"
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
			AND r2.version COLLATE "C" > r1.version COLLATE "C"
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

	readWriteEqual, readOnlyEqual := datastore.ResourcesAreEqual(resource, &existingResource)

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
		if readWriteEqual && readOnlyEqual && resource.Ksuid == ksuid {
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

	// Search for resources that contain a $ref to this KSUID in their properties.
	// Use a regex to handle PostgreSQL's jsonb::text formatting, which adds spaces after colons.
	pattern := fmt.Sprintf(`"\$ref"\s*:\s*"formae://%s#`, ksuid)

	query := `
	SELECT data, ksuid
	FROM resources r1
	WHERE data::text ~ :pattern
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

func (d *DatastoreAuroraDataAPI) FindResourcesDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Resource, error) {
	ctx := context.Background()

	if len(ksuids) == 0 {
		return make(map[string][]*pkgmodel.Resource), nil
	}

	// Build OR conditions for each KSUID pattern with named parameters.
	// Use regex to handle PostgreSQL's jsonb::text formatting, which adds spaces after colons.
	var conditions []string
	var params []types.SqlParameter
	for i, ksuid := range ksuids {
		pattern := fmt.Sprintf(`"\$ref"\s*:\s*"formae://%s#`, ksuid)
		paramName := fmt.Sprintf("pattern%d", i)
		conditions = append(conditions, fmt.Sprintf("data::text ~ :%s", paramName))
		params = append(params, types.SqlParameter{
			Name:  aws.String(paramName),
			Value: &types.FieldMemberStringValue{Value: pattern},
		})
	}
	params = append(params, types.SqlParameter{
		Name:  aws.String("operation"),
		Value: &types.FieldMemberStringValue{Value: string(resource_update.OperationDelete)},
	})

	query := fmt.Sprintf(`
	SELECT data, ksuid
	FROM resources r1
	WHERE (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM resources r2
		WHERE r1.uri = r2.uri
		AND r2.version COLLATE "C" > r1.version COLLATE "C"
	)
	AND operation != :operation
	`, strings.Join(conditions, " OR "))

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	// Build a map of KSUID -> resources that depend on it
	result := make(map[string][]*pkgmodel.Resource)
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

		// Find which of the input KSUIDs this resource depends on.
		// jsonb::text output has spaces after colons, so check both forms.
		for _, ksuid := range ksuids {
			withSpace := fmt.Sprintf("\"$ref\": \"formae://%s#", ksuid)
			withoutSpace := fmt.Sprintf("\"$ref\":\"formae://%s#", ksuid)
			if strings.Contains(jsonData, withSpace) || strings.Contains(jsonData, withoutSpace) {
				result[ksuid] = append(result[ksuid], &resource)
			}
		}
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) FindTargetsDependingOnMany(ksuids []string) (map[string][]*pkgmodel.Target, error) {
	ctx := context.Background()

	if len(ksuids) == 0 {
		return make(map[string][]*pkgmodel.Target), nil
	}

	// Build OR conditions for each KSUID pattern with named parameters.
	// Use regex to handle PostgreSQL's jsonb::text formatting, which adds spaces after colons.
	var conditions []string
	var params []types.SqlParameter
	for i, ksuid := range ksuids {
		pattern := fmt.Sprintf(`"\$ref"\s*:\s*"formae://%s#`, ksuid)
		paramName := fmt.Sprintf("pattern%d", i)
		conditions = append(conditions, fmt.Sprintf("config::text ~ :%s", paramName))
		params = append(params, types.SqlParameter{
			Name:  aws.String(paramName),
			Value: &types.FieldMemberStringValue{Value: pattern},
		})
	}

	query := fmt.Sprintf(`
	SELECT label, version, namespace, config, discoverable, config_schema,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
	FROM targets t1
	WHERE (%s)
	AND NOT EXISTS (
		SELECT 1
		FROM targets t2
		WHERE t1.label = t2.label
		AND t2.version > t1.version
	)
	`, strings.Join(conditions, " OR "))

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	// Build a map of KSUID -> targets that depend on it
	result := make(map[string][]*pkgmodel.Target)
	for _, record := range output.Records {
		if len(record) < 16 {
			return nil, fmt.Errorf("unexpected record length: %d", len(record))
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

		configSchema, _ := unmarshalConfigSchema(record[5])
		health, _ := scanAuroraTargetHealth(record[6:])
		reapingRaw := auroraReapingFromFields(record[14], record[15])

		target := &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      reapingRaw,
			Health:       health,
		}

		// Find which of the input KSUIDs this target depends on.
		// jsonb::text output has spaces after colons, so check both forms.
		configStr := string(config)
		for _, ksuid := range ksuids {
			withSpace := fmt.Sprintf("\"$ref\": \"formae://%s#", ksuid)
			withoutSpace := fmt.Sprintf("\"$ref\":\"formae://%s#", ksuid)
			if strings.Contains(configStr, withSpace) || strings.Contains(configStr, withoutSpace) {
				result[ksuid] = append(result[ksuid], target)
			}
		}
	}

	return result, nil
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
	INSERT INTO targets (label, version, namespace, config, discoverable, config_schema,
	                     target_incarnation_id, health_state, unreachable_accum_seconds,
	                     reap_kind, reap_max_unreachable_seconds)
	VALUES (:label, 1, :namespace, :config::jsonb, :discoverable, :config_schema::jsonb,
	        :target_incarnation_id, 'unknown', 0, :reap_kind, :reap_max_unreachable_seconds)
	`

	configJSON, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	var configSchemaParam types.Field
	if len(target.ConfigSchema.Hints) > 0 {
		csJSON, err := json.Marshal(target.ConfigSchema)
		if err != nil {
			return "", fmt.Errorf("failed to marshal config schema: %w", err)
		}
		configSchemaParam = &types.FieldMemberStringValue{Value: string(csJSON)}
	} else {
		configSchemaParam = &types.FieldMemberIsNull{Value: true}
	}

	incarnationID := mksuid.New().String()

	reapKind, reapMaxUnreachableSeconds, err := pkgmodel.ReapingToColumns(target.Reaping)
	if err != nil {
		return "", err
	}

	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: target.Label}},
		{Name: aws.String("namespace"), Value: &types.FieldMemberStringValue{Value: target.Namespace}},
		{Name: aws.String("config"), Value: &types.FieldMemberStringValue{Value: string(configJSON)}},
		{Name: aws.String("discoverable"), Value: &types.FieldMemberBooleanValue{Value: target.Discoverable}},
		{Name: aws.String("config_schema"), Value: configSchemaParam},
		{Name: aws.String("target_incarnation_id"), Value: &types.FieldMemberStringValue{Value: incarnationID}},
		{Name: aws.String("reap_kind"), Value: &types.FieldMemberStringValue{Value: reapKind}},
		{Name: aws.String("reap_max_unreachable_seconds"), Value: &types.FieldMemberLongValue{Value: reapMaxUnreachableSeconds}},
	}

	_, err = d.executeStatement(ctx, query, params)
	if err != nil {
		slog.Debug("failed to create target (may be retried as update)", "error", err, "label", target.Label)
		return "", err
	}

	return fmt.Sprintf("%s_1", target.Label), nil
}

func (d *DatastoreAuroraDataAPI) UpdateTarget(target *pkgmodel.Target) (string, error) {
	ctx := context.Background()

	// Load the latest row to carry health state forward onto the new version.
	query := `
	SELECT version, target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code
	FROM targets WHERE label = :label ORDER BY version DESC LIMIT 1`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: target.Label}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", err
	}

	if len(output.Records) == 0 {
		return "", fmt.Errorf("target %s does not exist, cannot update", target.Label)
	}

	record := output.Records[0]
	maxVersion, err := getIntField(record[0])
	if err != nil {
		return "", err
	}

	incarnationID, _ := getStringField(record[1])
	healthState, _ := getStringField(record[2])

	// Carry nullable timestamp fields forward.
	nullableTimestampParam := func(field types.Field) types.Field {
		str, _ := getStringField(field)
		if str == "" {
			return &types.FieldMemberIsNull{Value: true}
		}
		return &types.FieldMemberStringValue{Value: str}
	}
	lastSeenAtParam := nullableTimestampParam(record[3])
	observedAtParam := nullableTimestampParam(record[4])
	firstUnreachableAtParam := nullableTimestampParam(record[5])
	lastSampleAtParam := nullableTimestampParam(record[6])

	accumSeconds, _ := getIntField(record[7])
	lastErrorCode, _ := getStringField(record[8])
	var lastErrorCodeParam types.Field
	if lastErrorCode == "" {
		lastErrorCodeParam = &types.FieldMemberIsNull{Value: true}
	} else {
		lastErrorCodeParam = &types.FieldMemberStringValue{Value: lastErrorCode}
	}

	// Recovery: a reaped current row is brought back to life on re-declare —
	// fresh incarnation, health reset to 'unknown', accrual and timestamps cleared.
	if healthState == pkgmodel.TargetHealthStateReaped {
		incarnationID = mksuid.New().String()
		healthState = pkgmodel.TargetHealthStateUnknown
		lastSeenAtParam = &types.FieldMemberIsNull{Value: true}
		observedAtParam = &types.FieldMemberIsNull{Value: true}
		firstUnreachableAtParam = &types.FieldMemberIsNull{Value: true}
		lastSampleAtParam = &types.FieldMemberIsNull{Value: true}
		accumSeconds = 0
		lastErrorCodeParam = &types.FieldMemberIsNull{Value: true}
	}

	newVersion := maxVersion + 1

	configJSON, err := json.Marshal(target.Config)
	if err != nil {
		return "", err
	}

	var configSchemaParam types.Field
	if len(target.ConfigSchema.Hints) > 0 {
		csJSON, err := json.Marshal(target.ConfigSchema)
		if err != nil {
			return "", fmt.Errorf("failed to marshal config schema: %w", err)
		}
		configSchemaParam = &types.FieldMemberStringValue{Value: string(csJSON)}
	} else {
		configSchemaParam = &types.FieldMemberIsNull{Value: true}
	}

	reapKind, reapMaxUnreachableSeconds, err := pkgmodel.ReapingToColumns(target.Reaping)
	if err != nil {
		return "", err
	}

	insertQuery := `
	INSERT INTO targets (label, version, namespace, config, discoverable, config_schema,
	                     target_incarnation_id, health_state, last_seen_at, observed_at,
	                     first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	                     reap_kind, reap_max_unreachable_seconds)
	VALUES (:label, :version, :namespace, :config::jsonb, :discoverable, :config_schema::jsonb,
	        :target_incarnation_id, :health_state, :last_seen_at::timestamptz, :observed_at::timestamptz,
	        :first_unreachable_at::timestamptz, :last_sample_at::timestamptz, :unreachable_accum_seconds, :last_error_code,
	        :reap_kind, :reap_max_unreachable_seconds)
	`
	insertParams := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: target.Label}},
		{Name: aws.String("version"), Value: &types.FieldMemberLongValue{Value: int64(newVersion)}},
		{Name: aws.String("namespace"), Value: &types.FieldMemberStringValue{Value: target.Namespace}},
		{Name: aws.String("config"), Value: &types.FieldMemberStringValue{Value: string(configJSON)}},
		{Name: aws.String("discoverable"), Value: &types.FieldMemberBooleanValue{Value: target.Discoverable}},
		{Name: aws.String("config_schema"), Value: configSchemaParam},
		{Name: aws.String("target_incarnation_id"), Value: &types.FieldMemberStringValue{Value: incarnationID}},
		{Name: aws.String("health_state"), Value: &types.FieldMemberStringValue{Value: healthState}},
		{Name: aws.String("last_seen_at"), Value: lastSeenAtParam},
		{Name: aws.String("observed_at"), Value: observedAtParam},
		{Name: aws.String("first_unreachable_at"), Value: firstUnreachableAtParam},
		{Name: aws.String("last_sample_at"), Value: lastSampleAtParam},
		{Name: aws.String("unreachable_accum_seconds"), Value: &types.FieldMemberLongValue{Value: int64(accumSeconds)}},
		{Name: aws.String("last_error_code"), Value: lastErrorCodeParam},
		{Name: aws.String("reap_kind"), Value: &types.FieldMemberStringValue{Value: reapKind}},
		{Name: aws.String("reap_max_unreachable_seconds"), Value: &types.FieldMemberLongValue{Value: reapMaxUnreachableSeconds}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		slog.Error("failed to update target", "error", err, "label", target.Label, "version", newVersion)
		return "", err
	}

	return fmt.Sprintf("%s_%d", target.Label, newVersion), nil
}

func (d *DatastoreAuroraDataAPI) UpdateTargetHealth(obs pkgmodel.TargetHealthObservation) (bool, error) {
	ctx := context.Background()

	observedAt := obs.ObservedAt.UTC().Format(time.RFC3339Nano)

	var lastSeenAtParam types.Field
	if obs.LastSeenAt != nil {
		lastSeenAtParam = &types.FieldMemberStringValue{Value: obs.LastSeenAt.UTC().Format(time.RFC3339Nano)}
	} else {
		lastSeenAtParam = &types.FieldMemberIsNull{Value: true}
	}

	var lastErrorCodeParam types.Field
	if obs.LastErrorCode != "" {
		lastErrorCodeParam = &types.FieldMemberStringValue{Value: obs.LastErrorCode}
	} else {
		lastErrorCodeParam = &types.FieldMemberIsNull{Value: true}
	}

	// A reachable ("success") observation clears any accrued unreachability.
	accrualReset := ""
	if obs.State == pkgmodel.TargetHealthStateReachable {
		accrualReset = `,
			first_unreachable_at = NULL,
			unreachable_accum_seconds = 0`
	}

	var query string
	var params []types.SqlParameter
	if obs.IncarnationID != "" {
		query = fmt.Sprintf(`
		UPDATE targets SET
			health_state = :health_state,
			observed_at = :observed_at::timestamptz,
			last_seen_at = COALESCE(:last_seen_at::timestamptz, last_seen_at),
			last_error_code = :last_error_code%s
		WHERE label = :label
		  AND version = (SELECT MAX(version) FROM targets WHERE label = :label)
		  AND health_state <> 'reaped'
		  AND (observed_at IS NULL OR observed_at < :observed_at::timestamptz)
		  AND target_incarnation_id = :incarnation_id`, accrualReset)
		params = []types.SqlParameter{
			{Name: aws.String("health_state"), Value: &types.FieldMemberStringValue{Value: obs.State}},
			{Name: aws.String("observed_at"), Value: &types.FieldMemberStringValue{Value: observedAt}},
			{Name: aws.String("last_seen_at"), Value: lastSeenAtParam},
			{Name: aws.String("last_error_code"), Value: lastErrorCodeParam},
			{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: obs.TargetLabel}},
			{Name: aws.String("incarnation_id"), Value: &types.FieldMemberStringValue{Value: obs.IncarnationID}},
		}
	} else {
		query = fmt.Sprintf(`
		UPDATE targets SET
			health_state = :health_state,
			observed_at = :observed_at::timestamptz,
			last_seen_at = COALESCE(:last_seen_at::timestamptz, last_seen_at),
			last_error_code = :last_error_code%s
		WHERE label = :label
		  AND version = (SELECT MAX(version) FROM targets WHERE label = :label)
		  AND health_state <> 'reaped'
		  AND (observed_at IS NULL OR observed_at < :observed_at::timestamptz)`, accrualReset)
		params = []types.SqlParameter{
			{Name: aws.String("health_state"), Value: &types.FieldMemberStringValue{Value: obs.State}},
			{Name: aws.String("observed_at"), Value: &types.FieldMemberStringValue{Value: observedAt}},
			{Name: aws.String("last_seen_at"), Value: lastSeenAtParam},
			{Name: aws.String("last_error_code"), Value: lastErrorCodeParam},
			{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: obs.TargetLabel}},
		}
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return false, err
	}
	return output.NumberOfRecordsUpdated == 1, nil
}

func (d *DatastoreAuroraDataAPI) AdvanceTargetAccrual(targetLabel, incarnationID string, lastSampleAt time.Time, deltaSeconds int64) (bool, error) {
	ctx := context.Background()

	query := `
	UPDATE targets SET
		unreachable_accum_seconds = unreachable_accum_seconds + :delta_seconds,
		last_sample_at = :last_sample_at::timestamptz
	WHERE label = :label
	  AND version = (SELECT MAX(version) FROM targets WHERE label = :label)
	  AND health_state = 'unreachable'
	  AND target_incarnation_id = :incarnation_id`
	params := []types.SqlParameter{
		{Name: aws.String("delta_seconds"), Value: &types.FieldMemberLongValue{Value: deltaSeconds}},
		{Name: aws.String("last_sample_at"), Value: &types.FieldMemberStringValue{Value: lastSampleAt.UTC().Format(time.RFC3339Nano)}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: targetLabel}},
		{Name: aws.String("incarnation_id"), Value: &types.FieldMemberStringValue{Value: incarnationID}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return false, err
	}
	return output.NumberOfRecordsUpdated == 1, nil
}

func (d *DatastoreAuroraDataAPI) GetUnreachableTargets() ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	SELECT label, version, namespace, config, discoverable, config_schema,
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
	AND t1.health_state = 'unreachable'
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 16 {
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

		configSchema, err := unmarshalConfigSchema(record[5])
		if err != nil {
			return nil, fmt.Errorf("failed to parse config_schema: %w", err)
		}

		health, _ := scanAuroraTargetHealth(record[6:])
		reapingRaw := auroraReapingFromFields(record[14], record[15])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      reapingRaw,
			Health:       health,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) LoadTarget(targetLabel string) (*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	SELECT version, namespace, config, discoverable, config_schema,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
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
	if len(record) < 15 {
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

	configSchema, err := unmarshalConfigSchema(record[4])
	if err != nil {
		return nil, fmt.Errorf("failed to parse config_schema: %w", err)
	}

	health, err := scanAuroraTargetHealth(record[5:])
	if err != nil {
		return nil, fmt.Errorf("failed to parse target health: %w", err)
	}
	reapingRaw := auroraReapingFromFields(record[13], record[14])

	return &pkgmodel.Target{
		Label:        targetLabel,
		Namespace:    namespace,
		Config:       config,
		ConfigSchema: configSchema,
		Discoverable: discoverable,
		Version:      version,
		Reaping:      reapingRaw,
		Health:       health,
	}, nil
}

// scanAuroraTargetHealth extracts health fields from a Data API record slice
// starting at index 0: [target_incarnation_id, health_state, last_seen_at,
// observed_at, first_unreachable_at, last_sample_at, unreachable_accum_seconds,
// last_error_code].
func scanAuroraTargetHealth(fields []types.Field) (*pkgmodel.TargetHealth, error) {
	if len(fields) < 8 {
		return &pkgmodel.TargetHealth{State: "unknown"}, nil
	}

	incarnationID, _ := getStringField(fields[0])
	healthState, _ := getStringField(fields[1])

	parseNullableTime := func(f types.Field) *time.Time {
		t, err := getTimestampField(f)
		if err != nil || t.IsZero() {
			return nil
		}
		return &t
	}

	accumSeconds, _ := getIntField(fields[6])
	lastErrCode, _ := getStringField(fields[7])

	return &pkgmodel.TargetHealth{
		IncarnationID:           incarnationID,
		State:                   healthState,
		LastSeenAt:              parseNullableTime(fields[2]),
		ObservedAt:              parseNullableTime(fields[3]),
		FirstUnreachableAt:      parseNullableTime(fields[4]),
		LastSampleAt:            parseNullableTime(fields[5]),
		UnreachableAccumSeconds: int64(accumSeconds),
		LastErrorCode:           lastErrCode,
	}, nil
}

// auroraReapingFromFields reconstructs a target's raw reaping message from the
// reap_kind and reap_max_unreachable_seconds Data API fields. An empty kind
// falls back to the global default ('after').
func auroraReapingFromFields(kindField, maxField types.Field) json.RawMessage {
	kind, _ := getStringField(kindField)
	maxUnreachableSeconds, _ := getIntField(maxField)
	if kind == "" {
		kind = "after"
	}
	return pkgmodel.ReapingRawFromColumns(kind, int64(maxUnreachableSeconds))
}

func (d *DatastoreAuroraDataAPI) LoadAllTargets() ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	SELECT label, version, namespace, config, discoverable, config_schema,
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
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 16 {
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

		configSchema, err := unmarshalConfigSchema(record[5])
		if err != nil {
			return nil, fmt.Errorf("failed to parse config_schema: %w", err)
		}

		health, _ := scanAuroraTargetHealth(record[6:])
		reapingRaw := auroraReapingFromFields(record[14], record[15])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      reapingRaw,
			Health:       health,
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
	SELECT t1.label, t1.version, t1.namespace, t1.config, t1.discoverable, t1.config_schema,
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
	)
	`, strings.Join(placeholders, ","))

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 16 {
			continue
		}

		label, _ := getStringField(record[0])
		version, _ := getIntField(record[1])
		namespace, _ := getStringField(record[2])
		config, _ := getRawJSONField(record[3])
		discoverable, _ := getBoolField(record[4])
		configSchema, _ := unmarshalConfigSchema(record[5])
		health, _ := scanAuroraTargetHealth(record[6:])
		reapingRaw := auroraReapingFromFields(record[14], record[15])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      reapingRaw,
			Health:       health,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) LoadDiscoverableTargets() ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	query := `
	WITH latest_targets AS (
		SELECT label, version, namespace, config, discoverable, config_schema,
		       target_incarnation_id, health_state, last_seen_at, observed_at,
		       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
		       reap_kind, reap_max_unreachable_seconds
		FROM targets t1
		WHERE discoverable = TRUE
		AND NOT EXISTS (
			SELECT 1
			FROM targets t2
			WHERE t1.label = t2.label
			AND t2.version > t1.version
		)
	)
	SELECT DISTINCT ON (config) label, version, namespace, config, discoverable, config_schema,
	       target_incarnation_id, health_state, last_seen_at, observed_at,
	       first_unreachable_at, last_sample_at, unreachable_accum_seconds, last_error_code,
	       reap_kind, reap_max_unreachable_seconds
	FROM latest_targets
	ORDER BY config, version DESC
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 16 {
			continue
		}

		label, _ := getStringField(record[0])
		version, _ := getIntField(record[1])
		namespace, _ := getStringField(record[2])
		config, _ := getRawJSONField(record[3])
		discoverable, _ := getBoolField(record[4])
		configSchema, _ := unmarshalConfigSchema(record[5])
		health, _ := scanAuroraTargetHealth(record[6:])
		reapingRaw := auroraReapingFromFields(record[14], record[15])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      reapingRaw,
			Health:       health,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) QueryTargets(targetQuery *datastore.TargetQuery) ([]*pkgmodel.Target, error) {
	ctx := context.Background()

	queryStr := `
	SELECT label, version, namespace, config, discoverable, config_schema,
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
	`
	params := []types.SqlParameter{}
	paramIdx := 1

	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "label", "label", false, targetQuery.Label)
	queryStr, params, paramIdx = appendAuroraStringClause(queryStr, params, paramIdx, "namespace", "namespace", false, targetQuery.Namespace)
	queryStr, params, _ = appendAuroraBoolClause(queryStr, params, paramIdx, "discoverable", "discoverable", targetQuery.Discoverable)

	queryStr += " ORDER BY label"

	output, err := d.executeStatement(ctx, queryStr, params)
	if err != nil {
		return nil, err
	}

	var targets []*pkgmodel.Target
	for _, record := range output.Records {
		if len(record) < 16 {
			continue
		}

		label, _ := getStringField(record[0])
		version, _ := getIntField(record[1])
		namespace, _ := getStringField(record[2])
		config, _ := getRawJSONField(record[3])
		discoverable, _ := getBoolField(record[4])
		configSchema, _ := unmarshalConfigSchema(record[5])
		health, _ := scanAuroraTargetHealth(record[6:])
		reapingRaw := auroraReapingFromFields(record[14], record[15])

		targets = append(targets, &pkgmodel.Target{
			Label:        label,
			Namespace:    namespace,
			Config:       config,
			ConfigSchema: configSchema,
			Discoverable: discoverable,
			Version:      version,
			Reaping:      reapingRaw,
			Health:       health,
		})
	}

	return targets, nil
}

func (d *DatastoreAuroraDataAPI) Stats() (*stats.Stats, error) {
	ctx := context.Background()
	res := stats.Stats{}

	// Count distinct clients
	clientsQuery := fmt.Sprintf("SELECT COUNT(DISTINCT client_id) FROM %s", datastore.CommandsTable)
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
	`, datastore.CommandsTable)
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
	`, datastore.CommandsTable)
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
	  AND state NOT IN ('Success','Failed','Rejected','Canceled')
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
		slog.Debug("UpdateResourceUpdateState: row already in terminal state or not found, no-op", "commandID", commandID, "ksuid", ksuid)
		return nil
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

func (d *DatastoreAuroraDataAPI) BatchUpdateResourceUpdateState(commandID string, refs []datastore.ResourceUpdateRef, state resource_update.ResourceUpdateState, modifiedTs time.Time) error {
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
		  AND state NOT IN ('Success','Failed','Rejected','Canceled')
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

func (d *DatastoreAuroraDataAPI) UpdateFormaCommandTargetUpdates(commandID string, targetUpdatesJSON json.RawMessage, state forma_command.CommandState, modifiedTs time.Time) error {
	ctx := context.Background()

	query := `UPDATE forma_commands SET target_updates = :target_updates, state = :state, modified_ts = :modified_ts::timestamp WHERE command_id = :command_id`
	params := []types.SqlParameter{
		{Name: aws.String("target_updates"), Value: &types.FieldMemberStringValue{Value: string(targetUpdatesJSON)}},
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: string(state)}},
		{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTs.UTC().Format(time.RFC3339Nano)}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to update forma command target updates: %w", err)
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

	// Use pre-generated ID if available, otherwise generate one
	id := stack.ID
	if id == "" {
		id = mksuid.New().String()
		stack.ID = id
	}
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

	// Cascade delete: delete all inline policies associated with this stack
	if err := d.DeletePoliciesForStack(id, commandID); err != nil {
		slog.Warn("Failed to delete policies for stack", "error", err, "stackID", id, "label", label)
		// Continue with stack deletion even if policy deletion fails
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

	stack := &pkgmodel.Stack{
		ID:          id,
		Label:       label,
		Description: description,
	}

	// Load policies for this stack (both inline and standalone references)
	policies, err := d.loadPoliciesForStackAsJSON(ctx, id)
	if err != nil {
		slog.Warn("Failed to load policies for stack", "label", label, "error", err)
		// Don't fail the whole operation, just return stack without policies
	} else {
		stack.Policies = policies
	}

	return stack, nil
}

// loadPoliciesForStackAsJSON loads all policies for a stack and returns them as JSON.
// For inline policies, returns the full policy JSON.
// For standalone policies, returns {"$ref": "policy://label"} format.
func (d *DatastoreAuroraDataAPI) loadPoliciesForStackAsJSON(ctx context.Context, stackID string) ([]json.RawMessage, error) {
	var policies []json.RawMessage

	// 1. Load inline policies (stack_id set directly on the policy)
	inlineQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE stack_id = :stack_id
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	inlineParams := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
	}
	output, err := d.executeStatement(ctx, inlineQuery, inlineParams)
	if err != nil {
		return nil, fmt.Errorf("failed to query inline policies: %w", err)
	}

	for _, record := range output.Records {
		if len(record) < 3 {
			continue
		}
		label, _ := getStringField(record[0])
		policyType, _ := getStringField(record[1])
		policyData, _ := getStringField(record[2])
		if policyData != "" {
			// Reconstruct full policy JSON by merging stored data with type and label
			fullPolicyJSON, err := reconstructPolicyJSONAurora(label, policyType, policyData)
			if err != nil {
				slog.Warn("Failed to reconstruct policy JSON", "label", label, "error", err)
				continue
			}
			policies = append(policies, fullPolicyJSON)
		}
	}

	// 2. Load standalone policy references (via stack_policies junction table)
	standaloneQuery := `
		WITH latest_policies AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT p.label FROM stack_policies sp
		JOIN latest_policies p ON p.id = sp.policy_id
		WHERE sp.stack_id = :stack_id
		AND p.rn = 1 AND p.operation != 'delete'
	`
	standaloneParams := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
	}
	output2, err := d.executeStatement(ctx, standaloneQuery, standaloneParams)
	if err != nil {
		return policies, fmt.Errorf("failed to query standalone policies: %w", err)
	}

	for _, record := range output2.Records {
		if len(record) < 1 {
			continue
		}
		policyLabel, _ := getStringField(record[0])
		if policyLabel != "" {
			// Create a $ref entry for the standalone policy
			refJSON := fmt.Sprintf(`{"$ref":"policy://%s"}`, policyLabel)
			policies = append(policies, json.RawMessage(refJSON))
		}
	}

	return policies, nil
}

// reconstructPolicyJSONAurora merges policy_data with type and label to create full policy JSON
func reconstructPolicyJSONAurora(label, policyType, policyData string) (json.RawMessage, error) {
	// Parse the stored policy data
	var data map[string]any
	if err := json.Unmarshal([]byte(policyData), &data); err != nil {
		return nil, fmt.Errorf("failed to parse policy data: %w", err)
	}

	// Add Type and Label
	data["Type"] = policyType
	data["Label"] = label

	// Marshal back to JSON
	result, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal full policy: %w", err)
	}
	return result, nil
}

func (d *DatastoreAuroraDataAPI) ListAllStacks() ([]*pkgmodel.Stack, error) {
	ctx := context.Background()

	// Get all stacks at their latest version that aren't deleted
	// Uses window function to reliably get the most recent version per stack id
	// Note: Use COLLATE "C" for binary ordering of KSUID strings (en_US.utf8 collation breaks ASCII ordering)
	query := `
		SELECT id, label, description, valid_from FROM (
			SELECT id, label, description, valid_from, operation,
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
		if len(record) < 4 {
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
		validFrom, _ := getTimestampField(record[3])

		stacks = append(stacks, &pkgmodel.Stack{
			ID:          id,
			Label:       label,
			Description: description,
			CreatedAt:   validFrom,
		})
	}

	return stacks, nil
}

// Policy operations

func (d *DatastoreAuroraDataAPI) CreatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	ctx := context.Background()

	// Generate KSUIDs for both id and version
	id := mksuid.New().String()
	version := mksuid.New().String()

	// Serialize policy-specific data as JSON
	var policyData string
	switch p := policy.(type) {
	case *pkgmodel.TTLPolicy:
		data, err := json.Marshal(map[string]any{
			"TTLSeconds":   p.TTLSeconds,
			"OnDependents": p.OnDependents,
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal policy data: %w", err)
		}
		policyData = string(data)
	case *pkgmodel.AutoReconcilePolicy:
		data, err := json.Marshal(map[string]any{
			"IntervalSeconds": p.IntervalSeconds,
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal policy data: %w", err)
		}
		policyData = string(data)
	default:
		return "", fmt.Errorf("unsupported policy type: %T", policy)
	}

	query := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	          VALUES (:id, :version, :command_id, :operation, :label, :policy_type, :stack_id, :policy_data)`
	params := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "create"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: policy.GetLabel()}},
		{Name: aws.String("policy_type"), Value: &types.FieldMemberStringValue{Value: policy.GetType()}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: policy.GetStackID()}},
		{Name: aws.String("policy_data"), Value: &types.FieldMemberStringValue{Value: policyData}},
	}

	_, err := d.executeStatement(ctx, query, params)
	if err != nil {
		slog.Error("Failed to create policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d *DatastoreAuroraDataAPI) UpdatePolicy(policy pkgmodel.Policy, commandID string) (string, error) {
	ctx := context.Background()

	// Get the existing policy to find its id
	// For standalone policies (empty stack_id), check for both NULL and empty string
	var selectQuery string
	var selectParams []types.SqlParameter

	if policy.GetStackID() == "" {
		// Standalone policy - check for NULL or empty string
		selectQuery = `
			SELECT id FROM policies
			WHERE label = :label AND (stack_id IS NULL OR stack_id = '')
			ORDER BY version COLLATE "C" DESC
			LIMIT 1
		`
		selectParams = []types.SqlParameter{
			{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: policy.GetLabel()}},
		}
	} else {
		// Inline policy - exact match on stack_id
		selectQuery = `
			SELECT id FROM policies
			WHERE label = :label AND stack_id = :stack_id
			ORDER BY version COLLATE "C" DESC
			LIMIT 1
		`
		selectParams = []types.SqlParameter{
			{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: policy.GetLabel()}},
			{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: policy.GetStackID()}},
		}
	}

	result, err := d.executeStatement(ctx, selectQuery, selectParams)
	if err != nil {
		return "", fmt.Errorf("failed to find existing policy: %w", err)
	}
	if len(result.Records) == 0 {
		return "", fmt.Errorf("policy not found: %s", policy.GetLabel())
	}

	id, err := getStringField(result.Records[0][0])
	if err != nil {
		return "", fmt.Errorf("failed to get policy id: %w", err)
	}

	// Generate new version
	version := mksuid.New().String()

	// Serialize policy-specific data as JSON
	var policyData string
	switch p := policy.(type) {
	case *pkgmodel.TTLPolicy:
		data, err := json.Marshal(map[string]any{
			"TTLSeconds":   p.TTLSeconds,
			"OnDependents": p.OnDependents,
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal policy data: %w", err)
		}
		policyData = string(data)
	case *pkgmodel.AutoReconcilePolicy:
		data, err := json.Marshal(map[string]any{
			"IntervalSeconds": p.IntervalSeconds,
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal policy data: %w", err)
		}
		policyData = string(data)
	default:
		return "", fmt.Errorf("unsupported policy type: %T", policy)
	}

	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data)
	                VALUES (:id, :version, :command_id, :operation, :label, :policy_type, :stack_id, :policy_data)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "update"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: policy.GetLabel()}},
		{Name: aws.String("policy_type"), Value: &types.FieldMemberStringValue{Value: policy.GetType()}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: policy.GetStackID()}},
		{Name: aws.String("policy_data"), Value: &types.FieldMemberStringValue{Value: policyData}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		slog.Error("Failed to update policy", "error", err, "label", policy.GetLabel())
		return "", err
	}

	return version, nil
}

func (d *DatastoreAuroraDataAPI) GetPoliciesForStack(stackID string) ([]pkgmodel.Policy, error) {
	ctx := context.Background()

	// Get the latest non-deleted version of each policy for the given stack
	// This includes both:
	// 1. Inline policies (stack_id set directly on the policy)
	// 2. Standalone policies (attached via stack_policies junction table)
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, stack_id, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
		),
		-- Inline policies: stack_id is set directly on the policy
		inline_policies AS (
			SELECT label, policy_type, policy_data, stack_id
			FROM latest_policies
			WHERE stack_id = :stack_id AND rn = 1 AND operation != 'delete'
		),
		-- Standalone policies: attached via stack_policies junction table
		standalone_policies AS (
			SELECT lp.label, lp.policy_type, lp.policy_data, lp.stack_id
			FROM latest_policies lp
			JOIN stack_policies sp ON sp.policy_id = lp.id
			WHERE sp.stack_id = :stack_id AND lp.rn = 1 AND lp.operation != 'delete'
			AND (lp.stack_id IS NULL OR lp.stack_id = '')
		)
		SELECT label, policy_type, policy_data, stack_id FROM inline_policies
		UNION
		SELECT label, policy_type, policy_data, stack_id FROM standalone_policies
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
	}

	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var policies []pkgmodel.Policy
	for _, record := range result.Records {
		if len(record) < 4 {
			continue
		}

		label, _ := getStringField(record[0])
		policyType, _ := getStringField(record[1])
		policyDataStr, _ := getStringField(record[2])
		policyStackID, _ := getStringField(record[3])

		// For standalone policies, use the queried stackID for display purposes
		effectiveStackID := stackID
		if policyStackID != "" {
			effectiveStackID = policyStackID
		}
		policy, err := deserializePolicyAurora(label, policyType, policyDataStr, effectiveStackID)
		if err != nil {
			slog.Warn("Failed to deserialize policy, skipping", "error", err, "label", label, "type", policyType)
			continue
		}
		policies = append(policies, policy)
	}

	return policies, nil
}

func (d *DatastoreAuroraDataAPI) GetStandalonePolicy(label string) (pkgmodel.Policy, error) {
	ctx := context.Background()

	// Get the latest non-deleted version of the standalone policy with this label
	query := `
		WITH latest_policy AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = :label AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'
	`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}

	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	if len(result.Records) == 0 {
		return nil, nil
	}

	record := result.Records[0]
	if len(record) < 3 {
		return nil, nil
	}

	policyLabel, _ := getStringField(record[0])
	policyType, _ := getStringField(record[1])
	policyDataStr, _ := getStringField(record[2])

	return deserializePolicyAurora(policyLabel, policyType, policyDataStr, "")
}

func (d *DatastoreAuroraDataAPI) ListAllStandalonePolicies() ([]pkgmodel.Policy, error) {
	ctx := context.Background()

	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT label, policy_type, policy_data
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
		ORDER BY label
	`
	result, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list standalone policies: %w", err)
	}

	var policies []pkgmodel.Policy
	for _, record := range result.Records {
		if len(record) < 3 {
			continue
		}
		label, _ := getStringField(record[0])
		policyType, _ := getStringField(record[1])
		policyDataStr, _ := getStringField(record[2])

		policy, err := deserializePolicyAurora(label, policyType, policyDataStr, "")
		if err != nil {
			slog.Warn("Failed to deserialize policy", "label", label, "error", err)
			continue
		}
		policies = append(policies, policy)
	}

	return policies, nil
}

func (d *DatastoreAuroraDataAPI) AttachPolicyToStack(stackID, policyLabel string) error {
	ctx := context.Background()

	// First, get the policy ID from the label
	policyQuery := `
		WITH latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = :label AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id FROM latest_policy
		WHERE rn = 1 AND operation != 'delete'
	`
	params := []types.SqlParameter{
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: policyLabel}},
	}

	result, err := d.executeStatement(ctx, policyQuery, params)
	if err != nil {
		return fmt.Errorf("failed to get policy ID: %w", err)
	}

	if len(result.Records) == 0 {
		return fmt.Errorf("standalone policy not found: %s", policyLabel)
	}

	policyID, _ := getStringField(result.Records[0][0])

	// Insert into stack_policies (ignore if already exists)
	insertQuery := `INSERT INTO stack_policies (stack_id, policy_id) VALUES (:stack_id, :policy_id) ON CONFLICT DO NOTHING`
	insertParams := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
		{Name: aws.String("policy_id"), Value: &types.FieldMemberStringValue{Value: policyID}},
	}

	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		return fmt.Errorf("failed to attach policy to stack: %w", err)
	}

	slog.Debug("Attached standalone policy to stack",
		"stackID", stackID,
		"policyLabel", policyLabel,
		"policyID", policyID)

	return nil
}

func (d *DatastoreAuroraDataAPI) IsPolicyAttachedToStack(stackLabel, policyLabel string) (bool, error) {
	ctx := context.Background()

	// Check if the attachment exists in stack_policies by looking up stack and policy by label
	query := `
		WITH latest_stack AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
			WHERE label = :stack_label
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = :policy_label AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT 1 FROM stack_policies sp
		JOIN latest_stack s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		LIMIT 1
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_label"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
		{Name: aws.String("policy_label"), Value: &types.FieldMemberStringValue{Value: policyLabel}},
	}

	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return false, fmt.Errorf("failed to check policy attachment: %w", err)
	}

	return len(result.Records) > 0, nil
}

func (d *DatastoreAuroraDataAPI) GetStacksReferencingPolicy(policyLabel string) ([]string, error) {
	ctx := context.Background()

	// Get all stack labels that reference this standalone policy via the junction table
	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		),
		latest_policy AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = :policy_label AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT s.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policy p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY s.label
	`
	params := []types.SqlParameter{
		{Name: aws.String("policy_label"), Value: &types.FieldMemberStringValue{Value: policyLabel}},
	}

	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get stacks referencing policy: %w", err)
	}

	var stackLabels []string
	for _, record := range result.Records {
		if len(record) < 1 {
			continue
		}
		label, err := getStringField(record[0])
		if err != nil {
			return nil, fmt.Errorf("failed to get stack label: %w", err)
		}
		stackLabels = append(stackLabels, label)
	}

	return stackLabels, nil
}

func (d *DatastoreAuroraDataAPI) GetAttachedPolicyLabelsForStack(stackLabel string) ([]string, error) {
	ctx := context.Background()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
			WHERE label = :stack_label
		),
		latest_policies AS (
			SELECT id, label, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE (stack_id IS NULL OR stack_id = '')
		)
		SELECT p.label FROM stack_policies sp
		JOIN latest_stacks s ON s.id = sp.stack_id
		JOIN latest_policies p ON p.id = sp.policy_id
		WHERE s.rn = 1 AND s.operation != 'delete'
		AND p.rn = 1 AND p.operation != 'delete'
		ORDER BY p.label
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_label"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
	}

	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get attached policies for stack: %w", err)
	}

	var policyLabels []string
	for _, record := range result.Records {
		if len(record) < 1 {
			continue
		}
		label, err := getStringField(record[0])
		if err != nil {
			return nil, fmt.Errorf("failed to get policy label: %w", err)
		}
		policyLabels = append(policyLabels, label)
	}

	return policyLabels, nil
}

func (d *DatastoreAuroraDataAPI) DetachPolicyFromStack(stackLabel, policyLabel string) error {
	ctx := context.Background()

	query := `
		DELETE FROM stack_policies
		WHERE stack_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
				FROM stacks
				WHERE label = :stack_label
			) sub WHERE rn = 1 AND operation != 'delete'
		)
		AND policy_id IN (
			SELECT id FROM (
				SELECT id, label, operation,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
				FROM policies
				WHERE label = :policy_label AND (stack_id IS NULL OR stack_id = '')
			) sub WHERE rn = 1 AND operation != 'delete'
		)
	`
	params := []types.SqlParameter{
		{Name: aws.String("stack_label"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
		{Name: aws.String("policy_label"), Value: &types.FieldMemberStringValue{Value: policyLabel}},
	}

	_, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to detach policy from stack: %w", err)
	}

	slog.Debug("Detached standalone policy from stack",
		"stackLabel", stackLabel,
		"policyLabel", policyLabel)

	return nil
}

func (d *DatastoreAuroraDataAPI) DeletePolicy(policyLabel string) (string, error) {
	ctx := context.Background()

	// Get the current policy to get its ID and type
	query := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE label = :policy_label AND (stack_id IS NULL OR stack_id = '')
		)
		SELECT id, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	params := []types.SqlParameter{
		{Name: aws.String("policy_label"), Value: &types.FieldMemberStringValue{Value: policyLabel}},
	}
	result, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return "", fmt.Errorf("failed to get policy for deletion: %w", err)
	}
	if len(result.Records) == 0 {
		return "", fmt.Errorf("policy not found: %s", policyLabel)
	}

	record := result.Records[0]
	id, _ := getStringField(record[0])
	policyType, _ := getStringField(record[1])

	// Insert tombstone version
	version := mksuid.New().String()
	insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data) VALUES (:id, :version, :command_id, :operation, :label, :policy_type, :stack_id, :policy_data)`
	insertParams := []types.SqlParameter{
		{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
		{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
		{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: ""}},
		{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "delete"}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: policyLabel}},
		{Name: aws.String("policy_type"), Value: &types.FieldMemberStringValue{Value: policyType}},
		{Name: aws.String("stack_id"), Value: &types.FieldMemberIsNull{Value: true}},
		{Name: aws.String("policy_data"), Value: &types.FieldMemberStringValue{Value: "{}"}},
	}
	_, err = d.executeStatement(ctx, insertQuery, insertParams)
	if err != nil {
		return "", fmt.Errorf("failed to delete policy: %w", err)
	}

	slog.Debug("Deleted standalone policy", "label", policyLabel, "id", id)

	return version, nil
}

func (d *DatastoreAuroraDataAPI) DeletePoliciesForStack(stackID string, commandID string) error {
	ctx := context.Background()

	// First, remove any standalone policy attachments from the junction table
	// This doesn't delete the standalone policies themselves, just the association
	deleteAttachmentsQuery := `DELETE FROM stack_policies WHERE stack_id = :stack_id`
	deleteAttachmentsParams := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
	}
	_, err := d.executeStatement(ctx, deleteAttachmentsQuery, deleteAttachmentsParams)
	if err != nil {
		slog.Warn("Failed to delete policy attachments for stack", "error", err, "stackID", stackID)
	}

	// Now get and delete only inline policies (policies with stack_id set to this stack)
	// We query directly for inline policies rather than using GetPoliciesForStack
	// since that also returns standalone policies which shouldn't be deleted
	inlinePoliciesQuery := `
		WITH latest_policies AS (
			SELECT id, label, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
			WHERE stack_id = :stack_id
		)
		SELECT id, label, policy_type
		FROM latest_policies
		WHERE rn = 1 AND operation != 'delete'
	`
	inlinePoliciesParams := []types.SqlParameter{
		{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
	}

	result, err := d.executeStatement(ctx, inlinePoliciesQuery, inlinePoliciesParams)
	if err != nil {
		return fmt.Errorf("failed to get inline policies for stack: %w", err)
	}

	for _, record := range result.Records {
		if len(record) < 3 {
			continue
		}

		id, _ := getStringField(record[0])
		label, _ := getStringField(record[1])
		policyType, _ := getStringField(record[2])

		// Insert tombstone version for inline policy
		version := mksuid.New().String()
		insertQuery := `INSERT INTO policies (id, version, command_id, operation, label, policy_type, stack_id, policy_data) VALUES (:id, :version, :command_id, :operation, :label, :policy_type, :stack_id, :policy_data)`
		insertParams := []types.SqlParameter{
			{Name: aws.String("id"), Value: &types.FieldMemberStringValue{Value: id}},
			{Name: aws.String("version"), Value: &types.FieldMemberStringValue{Value: version}},
			{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
			{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: "delete"}},
			{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
			{Name: aws.String("policy_type"), Value: &types.FieldMemberStringValue{Value: policyType}},
			{Name: aws.String("stack_id"), Value: &types.FieldMemberStringValue{Value: stackID}},
			{Name: aws.String("policy_data"), Value: &types.FieldMemberStringValue{Value: "{}"}},
		}

		_, err = d.executeStatement(ctx, insertQuery, insertParams)
		if err != nil {
			slog.Error("Failed to delete inline policy", "error", err, "label", label)
			continue
		}
		slog.Debug("Deleted inline policy as part of stack deletion", "label", label, "stackID", stackID)
	}

	return nil
}

// deserializePolicyAurora creates a Policy from stored data (Aurora Data API version)
func deserializePolicyAurora(label, policyType, policyDataStr, stackID string) (pkgmodel.Policy, error) {
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

func (d *DatastoreAuroraDataAPI) GetExpiredStacks() ([]datastore.ExpiredStackInfo, error) {
	ctx := context.Background()

	// Get stacks with TTL policies that have expired:
	// - Handles both inline policies (stack_id set) and standalone policies (via stack_policies junction)
	// - Calculate expiration as stack.valid_from + policy.ttl_seconds
	// - Exclude stacks with active forma commands
	// - Only consider latest non-deleted versions of both stacks and policies
	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
		),
		-- Inline policies: stack_id is set directly on the policy
		inline_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       p.policy_data->>'OnDependents' as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND s.valid_from + ((p.policy_data->>'TTLSeconds')::int * interval '1 second') < now()
		),
		-- Standalone policies: attached via stack_policies junction table
		standalone_expired AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       p.policy_data->>'OnDependents' as on_dependents,
			       s.valid_from
			FROM latest_stacks s
			JOIN stack_policies sp ON sp.stack_id = s.id
			JOIN latest_policies p ON p.id = sp.policy_id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'ttl'
			AND (p.stack_id IS NULL OR p.stack_id = '')  -- standalone policies have NULL or empty stack_id
			AND s.valid_from + ((p.policy_data->>'TTLSeconds')::int * interval '1 second') < now()
		),
		-- Combine both inline and standalone expired stacks
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
		ORDER BY valid_from
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result []datastore.ExpiredStackInfo
	for _, record := range output.Records {
		if len(record) < 3 {
			continue
		}

		stackLabel, err := getStringField(record[0])
		if err != nil {
			return nil, err
		}
		stackID, err := getStringField(record[1])
		if err != nil {
			return nil, err
		}
		onDependents, _ := getStringField(record[2])
		if onDependents == "" {
			onDependents = "abort" // default
		}

		result = append(result, datastore.ExpiredStackInfo{
			StackLabel:   stackLabel,
			StackID:      stackID,
			OnDependents: onDependents,
		})
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) GetStacksWithAutoReconcilePolicy() ([]datastore.StackReconcileInfo, error) {
	ctx := context.Background()

	query := `
		WITH latest_stacks AS (
			SELECT id, label, valid_from, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM stacks
		),
		latest_policies AS (
			SELECT id, label, stack_id, policy_type, policy_data, operation,
			       ROW_NUMBER() OVER (PARTITION BY id ORDER BY version COLLATE "C" DESC) as rn
			FROM policies
		),
		inline_auto_reconcile AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       (p.policy_data->>'IntervalSeconds')::bigint as interval_seconds
			FROM latest_stacks s
			JOIN latest_policies p ON p.stack_id = s.id
			WHERE s.rn = 1 AND s.operation != 'delete'
			AND p.rn = 1 AND p.operation != 'delete'
			AND p.policy_type = 'auto-reconcile'
		),
		standalone_auto_reconcile AS (
			SELECT s.label as stack_label, s.id as stack_id,
			       (p.policy_data->>'IntervalSeconds')::bigint as interval_seconds
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
		       COALESCE(lr.last_reconcile_at, '1970-01-01 00:00:00'::timestamp) as last_reconcile_at
		FROM all_auto_reconcile ar
		LEFT JOIN last_reconcile lr ON ar.stack_label = lr.stack_label
	`

	output, err := d.executeStatement(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var result []datastore.StackReconcileInfo
	for _, record := range output.Records {
		if len(record) < 4 {
			continue
		}

		stackLabel, err := getStringField(record[0])
		if err != nil {
			return nil, err
		}
		stackID, err := getStringField(record[1])
		if err != nil {
			return nil, err
		}
		intervalSeconds, err := getIntField(record[2])
		if err != nil {
			return nil, err
		}
		lastReconcileAt, _ := getTimestampField(record[3])

		result = append(result, datastore.StackReconcileInfo{
			StackLabel:      stackLabel,
			StackID:         stackID,
			IntervalSeconds: int64(intervalSeconds),
			LastReconcileAt: lastReconcileAt,
		})
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) GetResourcesAtLastReconcile(stackLabel string) ([]datastore.ResourceSnapshot, error) {
	ctx := context.Background()

	// Declared state for auto-reconcile: per-resource DesiredState from the
	// most recent user-source reconcile that touched each resource. Failed
	// reconciles count (so failed updates are retried until they converge);
	// Canceled and InProgress reconciles do not (they aren't accepted user
	// intent).
	//
	// Reading per-resource rather than per-command is the key invariant.
	// The generator only emits resource_updates rows for resources whose
	// state actually changes — unchanged resources produce no row. If we
	// scoped the snapshot to a single reconcile command, a partial reconcile
	// that changed only some resources would yield a desired-state Forma
	// that omits the unchanged ones, and auto-reconcile would implicitly
	// delete them as drift. Taking the most recent user-source reconcile
	// row per ksuid keeps unchanged resources represented by the earlier
	// reconcile that last declared them.
	//
	// Destroy commands also contribute to the baseline. A destroy is the
	// user's latest declaration that the named resources should not exist —
	// its resource_updates rows have operation='delete' and become the
	// latest-per-ksuid touch for any destroyed resource. The outer filter
	// (operation != 'delete') then drops them from the snapshot, yielding
	// the correct empty desired baseline for fully-destroyed stacks (or
	// the correctly trimmed baseline for partial destroys). Destroys land
	// in forma_commands with command='destroy' and config_mode='patch', so
	// the OR branch admits them without further filtering on config_mode.
	//
	// The resource column is stored as TEXT; cast to json once in the CTE
	// so the downstream extractions can use the JSON operators.
	//
	// Delete operations are excluded from the outer SELECT: a deletion the
	// user requested is not part of the desired state going forward.
	query := `
		WITH user_reconcile_updates AS (
			SELECT ru.ksuid, ru.resource::json AS resource_json, ru.operation, fc.timestamp
			FROM resource_updates ru
			INNER JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE (
				(fc.command = 'apply' AND fc.config_mode = 'reconcile')
				OR fc.command = 'destroy'
			)
			AND fc.state IN ('Success', 'Failed')
			AND ru.source = 'user'
			AND ru.stack_label = :stack_label
		),
		latest_per_ksuid AS (
			SELECT ksuid, resource_json, operation,
			       ROW_NUMBER() OVER (
			           PARTITION BY ksuid
			           ORDER BY timestamp DESC,
			                    CASE WHEN operation = 'delete' THEN 1 ELSE 0 END
			       ) as rn
			FROM user_reconcile_updates
		)
		SELECT ksuid,
		       resource_json->>'Type'      as type,
		       resource_json->>'Label'     as label,
		       resource_json->>'Target'    as target,
		       resource_json->'Properties' as properties,
		       resource_json->'Schema'     as schema,
		       resource_json->>'NativeID'  as native_id
		FROM latest_per_ksuid
		WHERE rn = 1 AND operation != 'delete'
	`

	params := []types.SqlParameter{
		{Name: aws.String("stack_label"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return nil, err
	}

	var result []datastore.ResourceSnapshot
	for _, record := range output.Records {
		if len(record) < 7 {
			continue
		}

		ksuid, _ := getStringField(record[0])
		resourceType, _ := getStringField(record[1])
		label, _ := getStringField(record[2])
		target, _ := getStringField(record[3])
		propsData, _ := getRawJSONField(record[4])
		schemaData, _ := getRawJSONField(record[5])
		nativeID, _ := getStringField(record[6])

		snapshot := datastore.ResourceSnapshot{
			KSUID:      ksuid,
			Type:       resourceType,
			Label:      label,
			Target:     target,
			Properties: propsData,
			NativeID:   nativeID,
		}

		if len(schemaData) > 0 {
			if err := json.Unmarshal(schemaData, &snapshot.Schema); err != nil {
				return nil, fmt.Errorf("failed to unmarshal schema for resource %s: %w", label, err)
			}
		}

		result = append(result, snapshot)
	}

	return result, nil
}

func (d *DatastoreAuroraDataAPI) StackHasActiveCommands(stackLabel string) (bool, error) {
	ctx := context.Background()

	query := `
		SELECT EXISTS (
			SELECT 1 FROM resource_updates ru
			JOIN forma_commands fc ON ru.command_id = fc.command_id
			WHERE ru.stack_label = :stack_label
			AND fc.state NOT IN ('Success', 'Failed', 'Canceled')
		)
	`

	params := []types.SqlParameter{
		{Name: aws.String("stack_label"), Value: &types.FieldMemberStringValue{Value: stackLabel}},
	}

	output, err := d.executeStatement(ctx, query, params)
	if err != nil {
		return false, err
	}

	if len(output.Records) == 0 || len(output.Records[0]) == 0 {
		return false, nil
	}

	exists, _ := getBoolField(output.Records[0][0])
	return exists, nil
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

// SetHealthStateForTesting forces health_state for the target's max-version row.
// Used by test hooks that need to set up guard conditions (e.g. 'reaped') that cannot
// be reached through the public Datastore API.
func (d *DatastoreAuroraDataAPI) SetHealthStateForTesting(label, state string) error {
	ctx := context.Background()
	query := `
	UPDATE targets SET health_state = :state
	WHERE label = :label
	  AND version = (SELECT MAX(version) FROM targets WHERE label = :label)
	`
	params := []types.SqlParameter{
		{Name: aws.String("state"), Value: &types.FieldMemberStringValue{Value: state}},
		{Name: aws.String("label"), Value: &types.FieldMemberStringValue{Value: label}},
	}
	_, err := d.executeStatement(ctx, query, params)
	return err
}

// ForceCancelResourceUpdates CAS-terminalizes in-flight resource updates to Canceled in one
// transaction. For InProgress rows it also writes force-cancel progress. Returns the rows
// transitioned (split by prior state) and those already terminal (Skipped). Idempotent.
// On an ambiguous commit error the method returns an error so the caller can retry.
func (d *DatastoreAuroraDataAPI) ForceCancelResourceUpdates(commandID string, inProgress []datastore.ForceCancelRow, notStarted []datastore.ResourceUpdateRef, modifiedTs time.Time) (datastore.ForceCancelResult, error) {
	ctx := context.Background()
	var result datastore.ForceCancelResult

	if len(inProgress) == 0 && len(notStarted) == 0 {
		return result, nil
	}

	txID, err := d.beginTransaction(ctx)
	if err != nil {
		return result, fmt.Errorf("failed to begin transaction: %w", err)
	}

	modifiedTsStr := modifiedTs.UTC().Format(time.RFC3339Nano)

	for _, row := range inProgress {
		ref := datastore.ResourceUpdateRef{KSUID: row.KSUID, Operation: row.Operation}
		query := `
		UPDATE resource_updates
		SET state = 'Canceled', modified_ts = :modified_ts::timestamp,
		    progress_result = :progress_result, most_recent_progress = :most_recent_progress
		WHERE command_id = :command_id AND ksuid = :ksuid AND operation = :operation AND state = 'InProgress'
		`
		params := []types.SqlParameter{
			{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTsStr}},
			{Name: aws.String("progress_result"), Value: &types.FieldMemberStringValue{Value: string(row.ProgressJSON)}},
			{Name: aws.String("most_recent_progress"), Value: &types.FieldMemberStringValue{Value: string(row.MostRecentProgressJSON)}},
			{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
			{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: row.KSUID}},
			{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(row.Operation)}},
		}
		out, execErr := d.executeStatementInTransaction(ctx, txID, query, params)
		if execErr != nil {
			_ = d.rollbackTransaction(ctx, txID)
			return result, fmt.Errorf("failed to force-cancel InProgress row %s: %w", row.KSUID, execErr)
		}
		if out.NumberOfRecordsUpdated > 0 {
			result.CanceledInProgress = append(result.CanceledInProgress, ref)
		} else {
			result.Skipped = append(result.Skipped, ref)
		}
	}

	for _, ref := range notStarted {
		query := `
		UPDATE resource_updates
		SET state = 'Canceled', modified_ts = :modified_ts::timestamp
		WHERE command_id = :command_id AND ksuid = :ksuid AND operation = :operation AND state = 'NotStarted'
		`
		params := []types.SqlParameter{
			{Name: aws.String("modified_ts"), Value: &types.FieldMemberStringValue{Value: modifiedTsStr}},
			{Name: aws.String("command_id"), Value: &types.FieldMemberStringValue{Value: commandID}},
			{Name: aws.String("ksuid"), Value: &types.FieldMemberStringValue{Value: ref.KSUID}},
			{Name: aws.String("operation"), Value: &types.FieldMemberStringValue{Value: string(ref.Operation)}},
		}
		out, execErr := d.executeStatementInTransaction(ctx, txID, query, params)
		if execErr != nil {
			_ = d.rollbackTransaction(ctx, txID)
			return result, fmt.Errorf("failed to force-cancel NotStarted row %s: %w", ref.KSUID, execErr)
		}
		if out.NumberOfRecordsUpdated > 0 {
			result.CanceledNotStarted = append(result.CanceledNotStarted, ref)
		} else {
			result.Skipped = append(result.Skipped, ref)
		}
	}

	if err := d.commitTransaction(ctx, txID); err != nil {
		// Ambiguous commit: return error so caller can retry.
		return result, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return result, nil
}
