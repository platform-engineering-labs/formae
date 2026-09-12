// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_persister

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/aurora"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// Acceptance plus metadata completes inside admission, with no executor left to
// sanitize declarations. Provider work must still retain its input for resume.
func TestAcceptanceAdmissionSecretLifecycle(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres", "mssql", "aurora"} {
		t.Run(backend, func(t *testing.T) {
			ds := newAcceptanceSecretDatastore(t, backend)
			for _, tc := range []struct {
				name     string
				metadata string
				provider bool
				legacy   bool
			}{
				{name: "pure_acceptance"},
				{name: "ttl_update", metadata: "ttl"},
				{name: "stack_update", metadata: "stack"},
				{name: "generator_update", metadata: "generator"},
				{name: "ttl_with_provider", metadata: "ttl", provider: true},
				{name: "legacy_pending_ttl", metadata: "ttl", legacy: true},
				{name: "legacy_pending_stack", metadata: "stack", legacy: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
					require.NoError(t, err)
					sid, pid, gid := util.NewID(), util.NewID(), util.NewID()
					label := "accept-secret-" + sid
					admission := func(id string) *datastore.CommandAdmission {
						guards, err := ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
						require.NoError(t, err)
						return &datastore.CommandAdmission{Guards: guards, PrincipalScope: label, IdempotencyKey: id, RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{"ok":true}`)}
					}
					seed := newFormaCommandWithCreateResourceUpdate()
					seed.ID, seed.Command = util.NewID(), pkgmodel.CommandApply
					seed.ResourceUpdates = nil
					seed.Stacks = []forma_command.CommandStack{{ID: sid, Label: label}}
					seed.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: label}, Operation: stack_update.StackOperationCreate}}
					seed.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: pid, StackID: sid, StackLabel: label, Policy: &pkgmodel.TTLPolicy{Label: label, TTLSeconds: 3600, OnDependents: "abort"}, Operation: policy_update.PolicyOperationCreate}}
					seed.GeneratorUpdates = []generator_update.GeneratorUpdate{{Generator: &pkgmodel.PasswordGenerator{ID: gid, StackID: sid, Stack: label, Label: "password", Length: 24}, StackLabel: label, Operation: generator_update.GeneratorOperationCreate}}
					result := operator.Call(sender, StoreNewFormaCommand{Command: *seed, Admission: admission(seed.ID)})
					require.NoError(t, result.Error)
					require.True(t, result.Response.(CommandPersistResult).OK, "%+v", result.Response)

					command := newFormaCommandWithCreateResourceUpdate()
					command.ID, command.Command = util.NewID(), pkgmodel.CommandApply
					command.Stacks = seed.Stacks
					ru := &command.ResourceUpdates[0]
					ru.StackLabel, ru.DesiredState.Stack = label, label
					ru.Operation = resource_update.OperationAccept
					ru.DesiredState.Schema = pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}}}
					ru.DesiredState.Properties = json.RawMessage(`{"SecretString":"literal-accept-secret","Replicas":3}`)
					sensitive := true
					command.InputProperties = pkgmodel.SnapshotInputProperties(map[string]pkgmodel.Prop{"password": {Value: "input-secret", Sensitive: &sensitive}})
					switch tc.metadata {
					case "ttl":
						command.PolicyUpdates = []policy_update.PolicyUpdate{{PolicyID: pid, StackID: sid, StackLabel: label, Policy: &pkgmodel.TTLPolicy{Label: label, TTLSeconds: 7200, OnDependents: "abort"}, Operation: policy_update.PolicyOperationUpdate}}
					case "stack":
						command.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: sid, Label: label, Description: "updated"}, Operation: stack_update.StackOperationUpdate}}
					case "generator":
						command.GeneratorUpdates = []generator_update.GeneratorUpdate{{Generator: &pkgmodel.PasswordGenerator{ID: gid, StackID: sid, Stack: label, Label: "password", Length: 32}, StackLabel: label, Operation: generator_update.GeneratorOperationUpdate}}
					}
					if tc.provider {
						provider := *ru
						provider.DesiredState.Ksuid, provider.DesiredState.Label = util.NewID(), "provider"
						provider.Operation = resource_update.OperationCreate
						provider.DesiredState.Properties = json.RawMessage(`{"SecretString":"literal-provider-secret"}`)
						command.ResourceUpdates = append(command.ResourceUpdates, provider)
					}
					request := StoreNewFormaCommand{Command: *command}
					if !tc.legacy {
						request.Admission = admission(command.ID)
					}
					result = operator.Call(sender, request)
					require.NoError(t, result.Error)
					require.True(t, result.Response.(CommandPersistResult).OK, "%+v", result.Response)
					stored, err := ds.GetFormaCommandByCommandID(command.ID)
					require.NoError(t, err)
					require.JSONEq(t, `{"password":{"redacted":true}}`, string(stored.InputProperties))
					assertHashed := func(c *forma_command.FormaCommand) {
						t.Helper()
						for _, update := range c.ResourceUpdates {
							var props map[string]json.RawMessage
							require.NoError(t, json.Unmarshal(update.DesiredState.Properties, &props))
							secret := "literal-accept-secret"
							if update.Operation == resource_update.OperationCreate {
								secret = "literal-provider-secret"
							}
							digest := sha256.Sum256([]byte(secret))
							require.JSONEq(t, `{"$value":"`+hex.EncodeToString(digest[:])+`","$visibility":"Opaque","$strategy":"Update","$hashed":true}`, string(props["SecretString"]))
							require.NotContains(t, string(update.DesiredState.Properties), secret)
						}
					}
					if !tc.provider && !tc.legacy {
						require.Equal(t, forma_command.CommandStateSuccess, stored.State)
						assertHashed(stored)
						require.Empty(t, operator.Behavior().(*FormaCommandPersister).activeCommands)
						return
					}
					require.False(t, stored.IsInFinalState())
					// Both the durable resume payload and the live execution payload retain
					// literal inputs until outstanding work reports completion.
					restarted, sender2, err := newFormaCommandPersisterWithDatastore(t, ds)
					require.NoError(t, err)
					resumed := restarted.Call(sender2, LoadFormaCommand{CommandID: command.ID})
					require.NoError(t, resumed.Error)
					for _, c := range []*forma_command.FormaCommand{stored, resumed.Response.(LoadFormaCommandResult).Command, operator.Behavior().(*FormaCommandPersister).activeCommands[command.ID].command} {
						for _, update := range c.ResourceUpdates {
							require.Contains(t, string(update.DesiredState.Properties), "literal-")
							require.NotContains(t, string(update.DesiredState.Properties), "$hashed")
						}
					}
					if tc.legacy {
						if tc.metadata == "stack" {
							completed := command.StackUpdates[0]
							completed.State = stack_update.StackUpdateStateSuccess
							result = restarted.Call(sender2, messages.UpdateStackStates{CommandID: command.ID, StackUpdates: []stack_update.StackUpdate{completed}})
						} else {
							completed := command.PolicyUpdates[0]
							completed.State = policy_update.PolicyUpdateStateSuccess
							result = restarted.Call(sender2, messages.UpdatePolicyStates{CommandID: command.ID, PolicyUpdates: []policy_update.PolicyUpdate{completed}})
						}
					} else {
						provider := command.ResourceUpdates[1]
						result = restarted.Call(sender2, messages.MarkResourceUpdateAsComplete{CommandID: command.ID, ResourceURI: provider.URI(), Operation: provider.Operation, FinalState: types.ResourceUpdateStateSuccess, ResourceModifiedTs: util.TimeNow(), Version: "written"})
					}
					require.NoError(t, result.Error)
					require.True(t, result.Response.(CommandPersistResult).OK, "%+v", result.Response)
					stored, err = ds.GetFormaCommandByCommandID(command.ID)
					require.NoError(t, err)
					require.Equal(t, forma_command.CommandStateSuccess, stored.State)
					assertHashed(stored)
				})
			}
		})
	}
}

// External backends follow the datastore suite's local service configuration.
// PG/MSSQL databases belong to this test. Aurora uses unique row identities and
// deliberately does not clear its shared emulator database.
func newAcceptanceSecretDatastore(t *testing.T, backend string) datastore.Datastore {
	t.Helper()
	ctx := context.Background()
	var ds datastore.Datastore
	var err error
	switch backend {
	case "sqlite":
		ds, err = dssqlite.NewDatastoreSQLite(ctx, &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	case "postgres":
		admin, e := pgx.Connect(ctx, "postgres://postgres:admin@localhost:5432/postgres")
		if e != nil {
			t.Skipf("local Postgres unavailable: %v", e)
		}
		db := "accept_secret_" + strings.ToLower(util.NewID())
		t.Cleanup(func() {
			_, e := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{db}.Sanitize())
			require.NoError(t, e)
			require.NoError(t, admin.Close(ctx))
		})
		ds, err = postgres.NewDatastorePostgresEnsureDatabase(ctx, &pkgmodel.DatastoreConfig{Postgres: pkgmodel.PostgresConfig{Host: "localhost", Port: 5432, User: "postgres", Password: "admin", Database: db}}, "test")
	case "mssql":
		admin, e := sql.Open("sqlserver", "sqlserver://sa:Formae_Test_1234!@localhost:1433?encrypt=disable&database=master")
		require.NoError(t, e)
		if e = admin.PingContext(ctx); e != nil {
			_ = admin.Close()
			t.Skipf("local MSSQL unavailable: %v", e)
		}
		db := "accept_secret_" + strings.ToLower(util.NewID())
		_, e = admin.ExecContext(ctx, "CREATE DATABASE ["+db+"]")
		require.NoError(t, e)
		t.Cleanup(func() {
			_, e := admin.ExecContext(ctx, "DROP DATABASE ["+db+"]")
			require.NoError(t, e)
			require.NoError(t, admin.Close())
		})
		ds, err = mssql.NewDatastoreMSSQL(ctx, &pkgmodel.DatastoreConfig{MSSQL: pkgmodel.MSSQLConfig{Host: "localhost", Port: 1433, User: "sa", Password: "Formae_Test_1234!", Database: db, AuthMode: pkgmodel.MSSQLAuthSQL, ConnectionParams: "encrypt=disable"}}, "test")
	case "aurora":
		cluster, secret := os.Getenv("FORMAE_TEST_AURORA_CLUSTER_ARN"), os.Getenv("FORMAE_TEST_AURORA_SECRET_ARN")
		if cluster == "" || secret == "" {
			t.Skip("Aurora test environment not configured")
		}
		db := os.Getenv("FORMAE_TEST_AURORA_DATABASE")
		if db == "" {
			db = "formae"
		}
		ds, err = aurora.NewDatastoreAuroraDataAPI(ctx, &pkgmodel.DatastoreConfig{AuroraDataAPI: pkgmodel.AuroraDataAPIConfig{ClusterARN: cluster, SecretARN: secret, Database: db, Region: os.Getenv("FORMAE_TEST_AURORA_REGION"), Endpoint: os.Getenv("FORMAE_TEST_AURORA_ENDPOINT")}}, "test")
	}
	require.NoError(t, err)
	t.Cleanup(func() { ds.Close() })
	return ds
}
