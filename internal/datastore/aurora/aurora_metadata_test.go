// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

const (
	fakeDataAPIRowLimit    = 64 * 1024
	fakeDataAPIResultLimit = 1024 * 1024
)

type fakeCommandMetadata struct {
	message string
	inputs  json.RawMessage
	stacks  json.RawMessage
	setup   json.RawMessage
}

func (m fakeCommandMetadata) encoded() string {
	b, _ := json.Marshal(struct {
		Message string          `json:"Message"`
		Inputs  json.RawMessage `json:"Inputs"`
		Stacks  json.RawMessage `json:"Stacks"`
		Setup   json.RawMessage `json:"Setup"`
	}{m.message, m.inputs, m.stacks, m.setup})
	return string(b)
}

type responseLimitClient struct {
	commands  map[string]fakeCommandMetadata
	base      [][]types.Field
	mutateAt  int
	chunks    int
	malformed bool
	truncate  bool
}

func (c *responseLimitClient) ExecuteStatement(_ context.Context, in *rdsdata.ExecuteStatementInput, _ ...func(*rdsdata.Options)) (*rdsdata.ExecuteStatementOutput, error) {
	sql := *in.Sql
	if strings.Contains(sql, "WITH metadata AS") {
		if err := assertMetadataReadContract(sql, in.Parameters); err != nil {
			return nil, err
		}
		c.chunks++
		if c.malformed {
			return c.checked(&rdsdata.ExecuteStatementOutput{Records: [][]types.Field{{&types.FieldMemberStringValue{Value: "bad"}}}})
		}
		if c.mutateAt > 0 && c.chunks >= c.mutateAt {
			return c.checked(&rdsdata.ExecuteStatementOutput{})
		}
		id := stringParam(in.Parameters, "command_id")
		value := c.commands[id].encoded()
		digest := sha256Hex(value)
		if stringParam(in.Parameters, "metadata_digest") != digest {
			return c.checked(&rdsdata.ExecuteStatementOutput{})
		}
		offset := intParam(in.Parameters, "offset") - 1
		chars := []rune(value)
		end := offset + int(formaCommandMetadataChunkChars)
		if end > len(chars) {
			end = len(chars)
		}
		chunk := ""
		if offset >= 0 && offset < len(chars) {
			chunk = string(chars[offset:end])
		}
		if c.truncate && c.chunks > 1 {
			chunk = ""
		}
		return c.checked(&rdsdata.ExecuteStatementOutput{Records: [][]types.Field{{
			&types.FieldMemberLongValue{Value: int64(len(chars))},
			&types.FieldMemberStringValue{Value: chunk},
			&types.FieldMemberStringValue{Value: digest},
		}}})
	}
	if strings.Contains(sql, "FROM forma_commands") {
		if err := assertBaseProjectionContract(sql, in.Parameters); err != nil {
			return nil, err
		}
		for _, record := range c.base {
			if len(record) != 18 {
				return nil, fmt.Errorf("base command response is not digest-only: %d fields", len(record))
			}
			digest, err := getStringField(record[17])
			if err != nil || len(digest) != sha256.Size*2 {
				return nil, fmt.Errorf("base command response has invalid metadata digest")
			}
		}
		return c.checked(&rdsdata.ExecuteStatementOutput{Records: c.base})
	}
	// Command loaders subsequently ask for resource updates. Their absence is a
	// valid small result and keeps this fake focused on command metadata reads.
	return c.checked(&rdsdata.ExecuteStatementOutput{})
}

func (c *responseLimitClient) BeginTransaction(context.Context, *rdsdata.BeginTransactionInput, ...func(*rdsdata.Options)) (*rdsdata.BeginTransactionOutput, error) {
	return nil, fmt.Errorf("unexpected transaction")
}
func (c *responseLimitClient) CommitTransaction(context.Context, *rdsdata.CommitTransactionInput, ...func(*rdsdata.Options)) (*rdsdata.CommitTransactionOutput, error) {
	return nil, fmt.Errorf("unexpected transaction")
}
func (c *responseLimitClient) RollbackTransaction(context.Context, *rdsdata.RollbackTransactionInput, ...func(*rdsdata.Options)) (*rdsdata.RollbackTransactionOutput, error) {
	return nil, fmt.Errorf("unexpected transaction")
}

func (c *responseLimitClient) checked(out *rdsdata.ExecuteStatementOutput) (*rdsdata.ExecuteStatementOutput, error) {
	total := 0
	for _, record := range out.Records {
		row := len(`[{`) + len(`}]`) // records/row containers, deliberately conservative.
		for _, field := range record {
			switch v := field.(type) {
			case *types.FieldMemberStringValue:
				encoded, _ := json.Marshal(v.Value)
				row += len(`{"stringValue":}`) + len(encoded)
			case *types.FieldMemberLongValue:
				row += len(`{"longValue":9223372036854775807}`)
			}
			row += 1 // field-array delimiter / conservative container allowance
		}
		if row > fakeDataAPIRowLimit {
			return nil, fmt.Errorf("simulated Data API row limit: %d", row)
		}
		total += row
	}
	if total > fakeDataAPIResultLimit {
		return nil, fmt.Errorf("simulated Data API result limit: %d", total)
	}
	return out, nil
}

func assertBaseProjectionContract(sql string, params []types.SqlParameter) error {
	if len(params) > 8 { // dynamic loader filters are bounded and unrelated to metadata.
		return fmt.Errorf("unexpected base parameter width: %d", len(params))
	}
	const baseColumns = `command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, policy_updates, modified_ts, source, subject, subject_name, `
	start := strings.Index(sql, "SELECT ")
	end := strings.Index(sql, "FROM forma_commands")
	if start < 0 || end < 0 || start >= end || normalizeSQL(sql[start+len("SELECT "):end]) != normalizeSQL(baseColumns+formaCommandMetadataDigestSQL) {
		return fmt.Errorf("base command projection does not use digest-only metadata")
	}
	return nil
}

func assertMetadataReadContract(sql string, params []types.SqlParameter) error {
	start := strings.LastIndex(sql, "SELECT ")
	end := strings.LastIndex(sql, "FROM metadata")
	expected := `char_length(value), substring(value FROM :offset::int FOR 4096), ` + formaCommandMetadataHydrationDigestSQL
	if start < 0 || end < 0 || start >= end || normalizeSQL(sql[start+len("SELECT "):end]) != normalizeSQL(expected) {
		return fmt.Errorf("metadata query is not the fixed bounded chunk projection")
	}
	if len(params) != 3 || stringParam(params, "command_id") == "" || stringParam(params, "metadata_digest") == "" || intParam(params, "offset") < 1 {
		return fmt.Errorf("metadata query has invalid bound parameters")
	}
	return nil
}

func normalizeSQL(sql string) string { return strings.Join(strings.Fields(sql), " ") }

func TestResponseLimitClientRejectsOversizedResponses(t *testing.T) {
	c := &responseLimitClient{}
	_, err := c.checked(&rdsdata.ExecuteStatementOutput{Records: [][]types.Field{{&types.FieldMemberStringValue{Value: strings.Repeat("\\\"😀", fakeDataAPIRowLimit)}}}})
	require.ErrorContains(t, err, "row limit")
	records := make([][]types.Field, 200)
	for i := range records {
		records[i] = []types.Field{&types.FieldMemberStringValue{Value: strings.Repeat("\\\"😀", 2000)}}
	}
	_, err = c.checked(&rdsdata.ExecuteStatementOutput{Records: records})
	require.ErrorContains(t, err, "result limit")
}

func TestResponseLimitClientRejectsUnboundedProjectionMutations(t *testing.T) {
	const baseColumns = `command_id, timestamp, command, state, client_id,
		description_text, description_confirm, config_mode, config_force, config_simulate,
		target_updates, stack_updates, policy_updates, modified_ts, source, subject, subject_name, `
	err := assertBaseProjectionContract("SELECT "+baseColumns+formaCommandMetadataDigestSQL+", message FROM forma_commands", nil)
	require.ErrorContains(t, err, "digest-only")
	metadataSQL := `WITH metadata AS (SELECT 'x'::text AS value)
		SELECT char_length(value), substring(value FROM :offset::int FOR 4096), ` + formaCommandMetadataHydrationDigestSQL + `, value
		FROM metadata WHERE ` + formaCommandMetadataHydrationDigestSQL + ` = :metadata_digest`
	params := []types.SqlParameter{
		{Name: ptr("command_id"), Value: &types.FieldMemberStringValue{Value: "one"}},
		{Name: ptr("offset"), Value: &types.FieldMemberLongValue{Value: 1}},
		{Name: ptr("metadata_digest"), Value: &types.FieldMemberStringValue{Value: strings.Repeat("0", sha256.Size*2)}},
	}
	err = assertMetadataReadContract(metadataSQL, params)
	require.ErrorContains(t, err, "fixed bounded")
}

func ptr(value string) *string { return &value }

func stringParam(params []types.SqlParameter, name string) string {
	for _, p := range params {
		if p.Name != nil && *p.Name == name {
			return p.Value.(*types.FieldMemberStringValue).Value
		}
	}
	return ""
}
func intParam(params []types.SqlParameter, name string) int {
	for _, p := range params {
		if p.Name != nil && *p.Name == name {
			return int(p.Value.(*types.FieldMemberLongValue).Value)
		}
	}
	return 0
}
func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fakeBaseRecord(id string, m fakeCommandMetadata) []types.Field {
	return []types.Field{
		&types.FieldMemberStringValue{Value: id}, &types.FieldMemberStringValue{Value: "2026-01-01T00:00:00Z"},
		&types.FieldMemberStringValue{Value: "apply"}, &types.FieldMemberStringValue{Value: "Success"}, &types.FieldMemberStringValue{Value: "client"},
		&types.FieldMemberStringValue{}, &types.FieldMemberBooleanValue{}, &types.FieldMemberStringValue{}, &types.FieldMemberBooleanValue{}, &types.FieldMemberBooleanValue{},
		&types.FieldMemberStringValue{Value: "[]"}, &types.FieldMemberStringValue{Value: "[]"}, &types.FieldMemberStringValue{Value: "[]"}, &types.FieldMemberStringValue{},
		&types.FieldMemberStringValue{Value: "user"}, &types.FieldMemberStringValue{}, &types.FieldMemberStringValue{}, &types.FieldMemberStringValue{Value: sha256Hex(m.encoded())},
	}
}

func TestBoundedCommandMetadataHydrationAcrossAllLoaders(t *testing.T) {
	large := fakeCommandMetadata{message: strings.Repeat("message-", 12000), inputs: json.RawMessage(`{"public":"` + strings.Repeat("x", 70000) + `"}`), stacks: json.RawMessage(`[{"ID":"stack-id","Label":"` + strings.Repeat("member-", 12000) + `"}]`)}
	large.setup = json.RawMessage(`{"Version":1,"Committed":true,"Generators":[{"GeneratorID":"generator-one","StackID":"stack-id","Generator":{"Type":"password","Label":"password","Length":24,"ExcludeCharacters":"` + strings.Repeat("x", 140000) + `"},"Operation":"create","State":"Success","Version":"version-one"}]}`)
	client := &responseLimitClient{commands: map[string]fakeCommandMetadata{"one": large}, base: [][]types.Field{fakeBaseRecord("one", large)}}
	d := &DatastoreAuroraDataAPI{client: client}
	assertLoaded := func(t *testing.T, commands []*forma_command.FormaCommand, err error) {
		require.NoError(t, err)
		require.Len(t, commands, 1)
		require.Equal(t, large.message, commands[0].Message)
		require.Len(t, commands[0].GeneratorUpdates, 1)
		require.Equal(t, "generator-one", commands[0].GeneratorUpdates[0].Generator.GetID())
		require.Equal(t, "stack-id", commands[0].GeneratorUpdates[0].Generator.GetStackID())
		require.True(t, commands[0].Setup.Committed)
		require.JSONEq(t, string(large.inputs), string(commands[0].InputProperties))
		require.JSONEq(t, string(large.stacks), mustJSON(t, commands[0].Stacks))
	}
	t.Run("all", func(t *testing.T) { commands, err := d.LoadFormaCommands(); assertLoaded(t, commands, err) })
	t.Run("incomplete", func(t *testing.T) { commands, err := d.LoadIncompleteFormaCommands(); assertLoaded(t, commands, err) })
	t.Run("query", func(t *testing.T) {
		commands, err := d.QueryFormaCommands(&datastore.StatusQuery{})
		assertLoaded(t, commands, err)
	})
	t.Run("by-id", func(t *testing.T) {
		command, err := d.GetFormaCommandByCommandID("one")
		require.NoError(t, err)
		require.Equal(t, large.message, command.Message)
	})
	t.Run("latest", func(t *testing.T) {
		command, err := d.GetMostRecentFormaCommandByClientID("client")
		require.NoError(t, err)
		require.Equal(t, large.message, command.Message)
	})
}

func TestBoundedCommandMetadataHydrationAvoidsAggregateResultLimit(t *testing.T) {
	client := &responseLimitClient{commands: make(map[string]fakeCommandMetadata)}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("command-%03d", i)
		m := fakeCommandMetadata{inputs: json.RawMessage(`{"public":"` + strings.Repeat("x", 6000) + `"}`)}
		client.commands[id] = m
		client.base = append(client.base, fakeBaseRecord(id, m))
	}
	d := &DatastoreAuroraDataAPI{client: client}
	commands, err := d.QueryFormaCommands(&datastore.StatusQuery{N: 200})
	require.NoError(t, err)
	require.Len(t, commands, 200)
	require.Greater(t, client.chunks, 200, "each complete history is separately bounded")
	for _, command := range commands {
		require.Greater(t, len(command.InputProperties), 6000)
	}
}

func TestBoundedCommandMetadataRejectsConcurrentReplacement(t *testing.T) {
	m := fakeCommandMetadata{inputs: json.RawMessage(`{"public":"` + strings.Repeat("x", 9000) + `"}`)}
	client := &responseLimitClient{commands: map[string]fakeCommandMetadata{"one": m}, mutateAt: 2}
	d := &DatastoreAuroraDataAPI{client: client}
	cmd := &forma_command.FormaCommand{ID: "one"}
	err := d.hydrateFormaCommandMetadata(context.Background(), cmd, sha256Hex(m.encoded()))
	require.ErrorContains(t, err, "changed or is missing")
}

func TestBoundedCommandMetadataPreservesUnicodeAndRejectsInvalidChunks(t *testing.T) {
	m := fakeCommandMetadata{message: "quoted \\\" newline\\n" + strings.Repeat("😀", 1300), inputs: json.RawMessage(`{"false":false,"zero":0,"empty":"","unicode":"😀\\\"\\\\\\n"}`), stacks: json.RawMessage(`[{"ID":"zero","Label":"😀"}]`)}
	for len([]rune(m.encoded()))%int(formaCommandMetadataChunkChars) != 0 {
		m.message += "x"
	}
	t.Run("exact Unicode boundary", func(t *testing.T) {
		client := &responseLimitClient{commands: map[string]fakeCommandMetadata{"one": m}}
		cmd := &forma_command.FormaCommand{ID: "one"}
		require.NoError(t, (&DatastoreAuroraDataAPI{client: client}).hydrateFormaCommandMetadata(context.Background(), cmd, sha256Hex(m.encoded())))
		require.Equal(t, m.message, cmd.Message)
		require.JSONEq(t, string(m.inputs), string(cmd.InputProperties))
		require.Equal(t, m.stacks, json.RawMessage(mustJSON(t, cmd.Stacks)))
	})
	truncated := m
	truncated.message += "x"
	for name, client := range map[string]*responseLimitClient{
		"malformed response": {commands: map[string]fakeCommandMetadata{"one": m}, malformed: true},
		"truncated chunk":    {commands: map[string]fakeCommandMetadata{"one": truncated}, truncate: true},
	} {
		t.Run(name, func(t *testing.T) {
			value := client.commands["one"]
			err := (&DatastoreAuroraDataAPI{client: client}).hydrateFormaCommandMetadata(context.Background(), &forma_command.FormaCommand{ID: "one"}, sha256Hex(value.encoded()))
			require.Error(t, err)
		})
	}
}

// This uses the local Data API service to prove persistence followed by each
// loader still works for a command whose new metadata would exceed a returned
// Data API row. The fake above supplies the production response-limit check.
func TestAuroraLargeCommandMetadataStoreAndRead(t *testing.T) {
	if os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN") == "" {
		t.Skip("local Data API configuration required")
	}
	cfg := &pkgmodel.DatastoreConfig{DatastoreType: pkgmodel.AuroraDataAPIDatastore, AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{
		ClusterARN: os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN"), SecretARN: os.Getenv("FORMAE_TEST_AURORA_SECRET_ARN"), Database: os.Getenv("FORMAE_TEST_AURORA_DATABASE"), Region: os.Getenv("FORMAE_TEST_AURORA_REGION"), Endpoint: os.Getenv("FORMAE_TEST_AURORA_ENDPOINT"),
	}}
	ds, err := NewDatastoreAuroraDataAPI(context.Background(), cfg, "test")
	require.NoError(t, err)
	d := ds.(*DatastoreAuroraDataAPI)
	require.NoError(t, d.CleanUp())
	t.Cleanup(func() { _ = d.CleanUp() })
	cmd := &forma_command.FormaCommand{
		ID: "large-command-metadata", Command: pkgmodel.CommandApply, State: forma_command.CommandStateInProgress, ClientID: "large-client", Source: forma_command.SourceUser,
		Message: strings.Repeat("message-", 10000), InputProperties: json.RawMessage(`{"public":"` + strings.Repeat("x", 70000) + `"}`),
		Stacks: []forma_command.CommandStack{{ID: "large-stack", Label: strings.Repeat("membership-", 8000)}},
	}
	require.NoError(t, d.StoreFormaCommand(cmd, cmd.ID))
	nilInputs := &forma_command.FormaCommand{ID: "nil-command-metadata", Command: pkgmodel.CommandApply, State: forma_command.CommandStateInProgress, ClientID: "nil-client", Source: forma_command.SourceUser, Message: "nil input", InputProperties: nil, Stacks: nil}
	emptyInputs := &forma_command.FormaCommand{ID: "empty-command-metadata", Command: pkgmodel.CommandApply, State: forma_command.CommandStateInProgress, ClientID: "empty-client", Source: forma_command.SourceUser, Message: "empty input", InputProperties: json.RawMessage(`{}`), Stacks: nil}
	scalarInputs := &forma_command.FormaCommand{ID: "scalar-command-metadata", Command: pkgmodel.CommandApply, State: forma_command.CommandStateInProgress, ClientID: "scalar-client", Source: forma_command.SourceUser, Message: "scalar inputs", InputProperties: json.RawMessage(`{"false":false,"zero":0,"empty":""}`), Stacks: nil}
	require.NoError(t, d.StoreFormaCommand(nilInputs, nilInputs.ID))
	require.NoError(t, d.StoreFormaCommand(emptyInputs, emptyInputs.ID))
	require.NoError(t, d.StoreFormaCommand(scalarInputs, scalarInputs.ID))
	expected := map[string]*forma_command.FormaCommand{cmd.ID: cmd, nilInputs.ID: nilInputs, emptyInputs.ID: emptyInputs, scalarInputs.ID: scalarInputs}
	assertMetadata := func(t *testing.T, actual *forma_command.FormaCommand) {
		t.Helper()
		want, ok := expected[actual.ID]
		require.True(t, ok, "unexpected command %s", actual.ID)
		require.Equal(t, want.Message, actual.Message)
		if want.InputProperties == nil {
			require.Nil(t, actual.InputProperties, "SQL NULL input must remain nil")
		} else {
			require.JSONEq(t, string(want.InputProperties), string(actual.InputProperties), "{} and scalar inputs must round trip")
		}
		require.Equal(t, want.Stacks, actual.Stacks, "nil and large membership must round trip")
	}
	assertAllMetadata := func(t *testing.T, got []*forma_command.FormaCommand) {
		t.Helper()
		require.Len(t, got, len(expected))
		for _, actual := range got {
			assertMetadata(t, actual)
		}
	}
	loaded, err := d.LoadFormaCommands()
	require.NoError(t, err)
	assertAllMetadata(t, loaded)
	incomplete, err := d.LoadIncompleteFormaCommands()
	require.NoError(t, err)
	assertAllMetadata(t, incomplete)
	for _, want := range expected {
		byID, err := d.GetFormaCommandByCommandID(want.ID)
		require.NoError(t, err)
		assertMetadata(t, byID)
		latest, err := d.GetMostRecentFormaCommandByClientID(want.ClientID)
		require.NoError(t, err)
		assertMetadata(t, latest)
	}
	queried, err := d.QueryFormaCommands(&datastore.StatusQuery{N: 4})
	require.NoError(t, err)
	assertAllMetadata(t, queried)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}
