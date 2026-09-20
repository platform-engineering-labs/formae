// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_persister

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/require"
)

func TestGuardedPersisterRejectsStaleAndReplaysWithoutRecaching(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	defer ds.Close()
	guards, err := ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard})
	require.NoError(t, err)
	admission := datastore.CommandAdmission{Guards: guards, PrincipalScope: "test-subject", IdempotencyKey: "test-key", RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{"review":"original"}`)}
	_, err = ds.CreateStack(&pkgmodel.Stack{ID: util.NewID(), Label: "new"}, "writer")
	require.NoError(t, err)
	operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	command := newFormaCommandWithCreateResourceUpdate()
	result := operator.Call(sender, StoreNewFormaCommand{Command: *command, Admission: &admission})
	require.NoError(t, result.Error)
	require.Contains(t, result.Response.(CommandPersistResult).Error, datastore.ErrStaleAdmission.Error())
	_, callErr := messages.UnwrapCall(result.Response, nil)
	require.ErrorIs(t, callErr, datastore.ErrStaleAdmission)
	stored, err := ds.GetFormaCommandByCommandID(command.ID)
	require.Error(t, err)
	require.Nil(t, stored)
	admission.Guards, err = ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard})
	require.NoError(t, err)
	result = operator.Call(sender, StoreNewFormaCommand{Command: *command, Admission: &admission})
	require.NoError(t, result.Error)
	accepted := result.Response.(CommandPersistResult)
	require.Empty(t, accepted.Error)
	require.NotNil(t, accepted.Admission)
	require.False(t, accepted.Admission.Replayed)
	require.Equal(t, command.ID, accepted.Admission.CommandID)
	restarted, sender2, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	retry := *command
	retry.ID = util.NewID()
	result = restarted.Call(sender2, StoreNewFormaCommand{Command: retry, Admission: &admission})
	require.NoError(t, result.Error)
	replay := result.Response.(CommandPersistResult)
	require.Empty(t, replay.Error)
	require.True(t, replay.Admission.Replayed)
	require.Equal(t, command.ID, replay.Admission.CommandID)
	require.Empty(t, restarted.Behavior().(*FormaCommandPersister).activeCommands)
	commands, err := ds.LoadFormaCommands()
	require.NoError(t, err)
	require.Len(t, commands, 1)
}

func TestGuardedPersisterCachesCommittedMetadata(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	defer ds.Close()
	operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	command := newFormaCommandWithCreateResourceUpdate()
	id := util.NewID()
	label := "committed"
	command.Stacks = []forma_command.CommandStack{{ID: id, Label: label}}
	command.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: id, Label: label}, Operation: stack_update.StackOperationCreate}}
	guards, err := ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
	require.NoError(t, err)
	admission := datastore.CommandAdmission{Guards: guards, PrincipalScope: "scope", IdempotencyKey: "setup", RequestDigest: strings.Repeat("b", 64), Receipt: []byte(`{"ok":true}`)}
	result := operator.Call(sender, StoreNewFormaCommand{Command: *command, Admission: &admission})
	require.NoError(t, result.Error)
	require.Empty(t, result.Response.(CommandPersistResult).Error)
	cached := operator.Behavior().(*FormaCommandPersister).activeCommands[command.ID]
	require.NotNil(t, cached)
	require.Equal(t, stack_update.StackUpdateStateSuccess, cached.command.StackUpdates[0].State)
	require.True(t, cached.command.Setup.Committed)
	stale := operator.Call(sender, messages.UpdateStackStates{CommandID: command.ID, StackUpdates: command.StackUpdates})
	require.NoError(t, stale.Error)
	require.Equal(t, stack_update.StackUpdateStateSuccess, cached.command.StackUpdates[0].State)

	require.NotEqual(t, forma_command.CommandStateSuccess, cached.command.State, "provider work must remain pending")
	result = operator.Call(sender, MarkResourcesAsFailed{CommandID: command.ID, Resources: []ResourceUpdateRef{{URI: command.ResourceUpdates[0].URI(), Operation: command.ResourceUpdates[0].Operation}}, ResourceModifiedTs: util.TimeNow(), FailureReason: "provider failed after setup"})
	require.NoError(t, result.Error)
	failed, err := ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateFailed, failed.State)
	require.True(t, failed.Setup.Committed)
	require.Equal(t, stack_update.StackUpdateStateSuccess, failed.StackUpdates[0].State)
	// Metadata-only admission has no executor completion message to await.
	command = newFormaCommandWithCreateResourceUpdate()
	command.ID = util.NewID()
	command.ResourceUpdates = nil
	command.Stacks = []forma_command.CommandStack{{ID: util.NewID(), Label: "metadata-only"}}
	command.StackUpdates = []stack_update.StackUpdate{{Stack: pkgmodel.Stack{ID: command.Stacks[0].ID, Label: "metadata-only"}, Operation: stack_update.StackOperationCreate}}
	admission.IdempotencyKey = "only"
	admission.Guards, err = ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard, datastore.AdmissionPolicyGuard, datastore.AdmissionGeneratorGuard})
	require.NoError(t, err)
	result = operator.Call(sender, StoreNewFormaCommand{Command: *command, Admission: &admission})
	require.NoError(t, result.Error)
	require.Empty(t, result.Response.(CommandPersistResult).Error)
	require.Nil(t, operator.Behavior().(*FormaCommandPersister).activeCommands[command.ID])
	only, err := ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateSuccess, only.State)

}

// Once the persister replies to a guarded admission, later mailbox turns must
// not mutate the caller's snapshot, and caller mutations must not reach back
// into the authoritative cached command. Load replies have the same ownership
// boundary. The snapshot includes nested execution data and setup-only draw
// identity that ordinary FormaCommand JSON omits.
func TestGuardedPersisterRepliesOwnDetachedCommandSnapshots(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	defer ds.Close()

	persister, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	command := newFormaCommandWithCreateResourceUpdate()
	command.Setup = &forma_command.SetupBoundary{Version: 1}
	command.ResourceUpdates[0].ResourceTarget.Config = json.RawMessage(`{"endpoint":"original"}`)
	command.ResourceUpdates[0].ResourceTarget.ExecutionIncarnation = "planned-incarnation"
	command.TargetUpdates = []target_update.TargetUpdate{{
		Target:    pkgmodel.Target{Label: "command-target", Namespace: "test", Config: json.RawMessage(`{"region":"original"}`)},
		Operation: target_update.TargetOperationCreate,
		State:     target_update.TargetUpdateStateNotStarted,
	}}
	draw := &pkgmodel.PasswordGenerator{Label: "credential", Stack: "test-stack", Length: 24, Lowercase: true}
	draw.SetID("generator-id")
	draw.SetStackID("generator-stack-id")
	command.DrawGeneratorUpdates = []generator_update.GeneratorUpdate{generator_update.NewDrawGeneratorUpdate(draw, "test-stack")}
	command.DrawIntentKnown = true

	guards, err := ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{
		datastore.AdmissionStackMappingGuard,
		datastore.AdmissionPolicyGuard,
		datastore.AdmissionGeneratorGuard,
	})
	require.NoError(t, err)
	admission := datastore.CommandAdmission{Guards: guards, PrincipalScope: "snapshot-subject", IdempotencyKey: "snapshot-key", RequestDigest: strings.Repeat("c", 64), Receipt: []byte(`{"ok":true}`)}
	result := persister.Call(sender, StoreNewFormaCommand{Command: *command, Admission: &admission})
	require.NoError(t, result.Error)
	stored := result.Response.(CommandPersistResult)
	require.Empty(t, stored.Error)
	require.NotNil(t, stored.Admission)
	reply := stored.Admission.Command
	require.NotNil(t, reply)
	require.True(t, reply.Setup.Committed)
	require.Equal(t, "generator-id", reply.DrawGeneratorUpdates[0].Generator.GetID())
	require.Equal(t, "generator-stack-id", reply.DrawGeneratorUpdates[0].Generator.GetStackID())

	progress := persister.Call(sender, messages.UpdateResourceProgress{
		CommandID: command.ID, ResourceURI: command.ResourceUpdates[0].URI(), Operation: resource_update.OperationCreate,
		ResourceState: resource_update.ResourceUpdateStateInProgress, ResourceStartTs: time.Now(), ResourceModifiedTs: time.Now(),
		ResourceProperties: json.RawMessage(`{"foo":"progressed"}`), Version: "v2",
		Progress: plugin.TrackedProgress{ProgressResult: resource.ProgressResult{Operation: resource.OperationCreate, OperationStatus: resource.OperationStatusInProgress}},
	})
	require.NoError(t, progress.Error)
	require.Empty(t, progress.Response.(CommandPersistResult).Error)
	targets := persister.Call(sender, target_update.UpdateTargetStates{CommandID: command.ID, TargetUpdates: []target_update.TargetUpdate{{
		Target:    pkgmodel.Target{Label: "command-target", Namespace: "test", Config: json.RawMessage(`{"region":"progressed"}`)},
		Operation: target_update.TargetOperationCreate, State: target_update.TargetUpdateStateInProgress,
	}}})
	require.NoError(t, targets.Error)
	require.Empty(t, targets.Response.(CommandPersistResult).Error)

	require.Equal(t, forma_command.CommandStateNotStarted, reply.State)
	require.JSONEq(t, `{"foo":"bar"}`, string(reply.ResourceUpdates[0].DesiredState.Properties))
	require.Equal(t, "", reply.ResourceUpdates[0].Version)
	require.JSONEq(t, `{"endpoint":"original"}`, string(reply.ResourceUpdates[0].ResourceTarget.Config))
	require.Equal(t, "planned-incarnation", reply.ResourceUpdates[0].ResourceTarget.ExecutionIncarnation)
	require.JSONEq(t, `{"region":"original"}`, string(reply.TargetUpdates[0].Target.Config))
	require.Equal(t, target_update.TargetUpdateStateNotStarted, reply.TargetUpdates[0].State)
	require.True(t, reply.Setup.Committed)
	require.True(t, reply.DrawIntentKnown)
	require.Equal(t, "generator-id", reply.DrawGeneratorUpdates[0].Generator.GetID())
	require.Equal(t, "generator-stack-id", reply.DrawGeneratorUpdates[0].Generator.GetStackID())

	// A consumer is free to mutate its reply without changing the persister's
	// authoritative command.
	reply.ResourceUpdates[0].DesiredState.Properties[0] = '['
	reply.ResourceUpdates[0].ResourceTarget.Config[0] = '['
	reply.TargetUpdates[0].Target.Config[0] = '['
	reply.Setup.Committed = false
	reply.DrawGeneratorUpdates[0].Generator.SetID("caller-corruption")

	loadedResult := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, loadedResult.Error)
	loaded := loadedResult.Response.(LoadFormaCommandResult).Command
	require.Equal(t, forma_command.CommandStateInProgress, loaded.State)
	require.JSONEq(t, `{"foo":"progressed"}`, string(loaded.ResourceUpdates[0].DesiredState.Properties))
	require.Equal(t, "v2", loaded.ResourceUpdates[0].Version)
	require.JSONEq(t, `{"endpoint":"original"}`, string(loaded.ResourceUpdates[0].ResourceTarget.Config))
	require.Equal(t, "planned-incarnation", loaded.ResourceUpdates[0].ResourceTarget.ExecutionIncarnation)
	require.JSONEq(t, `{"region":"progressed"}`, string(loaded.TargetUpdates[0].Target.Config))
	require.True(t, loaded.Setup.Committed)
	require.Equal(t, "generator-id", loaded.DrawGeneratorUpdates[0].Generator.GetID())
	require.Equal(t, "generator-stack-id", loaded.DrawGeneratorUpdates[0].Generator.GetStackID())

	loaded.ResourceUpdates[0].DesiredState.Properties[0] = '['
	loaded.Setup.Committed = false
	loaded.DrawGeneratorUpdates[0].Generator.SetStackID("load-corruption")
	reloadedResult := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, reloadedResult.Error)
	reloaded := reloadedResult.Response.(LoadFormaCommandResult).Command
	require.JSONEq(t, `{"foo":"progressed"}`, string(reloaded.ResourceUpdates[0].DesiredState.Properties))
	require.True(t, reloaded.Setup.Committed)
	require.Equal(t, "generator-stack-id", reloaded.DrawGeneratorUpdates[0].Generator.GetStackID())
}

func TestOverallStateIncludesIncompleteAndFailedMetadata(t *testing.T) {
	c := &forma_command.FormaCommand{StackUpdates: []stack_update.StackUpdate{{State: stack_update.StackUpdateStateNotStarted}}}
	require.NotEqual(t, forma_command.CommandStateSuccess, overallCommandState(c))
	c.StackUpdates[0].State = stack_update.StackUpdateStateFailed
	require.Equal(t, forma_command.CommandStateFailed, overallCommandState(c))
}
