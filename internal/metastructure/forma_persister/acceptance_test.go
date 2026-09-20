// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_persister

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"

	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestAcceptanceCommandCompletesWithoutExecutionAndSurvivesRestart(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	command := newFormaCommandWithCreateResourceUpdate()
	command.ResourceUpdates[0].Operation = resource_update.OperationAccept
	command.ResourceUpdates[0].State = types.ResourceUpdateStateSuccess
	_, err = ds.StoreResource(&command.ResourceUpdates[0].DesiredState, "observation")
	require.NoError(t, err)
	observed, err := ds.LoadResource(command.ResourceUpdates[0].DesiredState.URI())
	require.NoError(t, err)
	command.ResourceUpdates[0].Version = observed.Version
	before, err := ds.LoadAllResourceVersions()
	require.NoError(t, err)
	operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	result := operator.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, result.Error)
	require.True(t, result.Response.(CommandPersistResult).OK)
	require.Empty(t, operator.Behavior().(*FormaCommandPersister).activeCommands, "pure acceptance must not retain an active command")
	persisted, err := ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateSuccess, persisted.State)
	require.Equal(t, types.ResourceUpdateStateSuccess, persisted.ResourceUpdates[0].State)
	require.Equal(t, observed.Version, persisted.ResourceUpdates[0].Version)
	inventory, err := ds.LoadAllResourceVersions()
	require.NoError(t, err)
	require.Equal(t, before, inventory, "logical acceptance must never insert or rewrite inventory versions")
	restarted, sender2, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	loaded := restarted.Call(sender2, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, loaded.Error)
	require.Equal(t, forma_command.CommandStateSuccess, loaded.Response.(LoadFormaCommandResult).Command.State)
	require.Empty(t, restarted.Behavior().(*FormaCommandPersister).activeCommands, "loading a finished acceptance must not recache it")
}

func TestAcceptanceMixedCommandWaitsOnlyForProviderCompletion(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	command := newFormaCommandWithCreateResourceUpdate()
	provider := command.ResourceUpdates[0]
	accept := provider
	accept.DesiredState.Ksuid = util.NewID()
	accept.DesiredState.Label = "accepted"
	accept.Operation = resource_update.OperationAccept
	accept.State = types.ResourceUpdateStateSuccess
	accept.Version = "reviewed"
	command.ResourceUpdates = append(command.ResourceUpdates, accept)
	operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	result := operator.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, result.Error)
	require.True(t, result.Response.(CommandPersistResult).OK)
	loaded, err := ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.False(t, loaded.IsInFinalState())
	completion := messages.MarkResourceUpdateAsComplete{CommandID: command.ID, ResourceURI: provider.DesiredState.URI(), Operation: provider.Operation, FinalState: types.ResourceUpdateStateSuccess, ResourceModifiedTs: util.TimeNow(), Version: "written"}
	result = operator.Call(sender, completion)
	require.NoError(t, result.Error)
	require.True(t, result.Response.(CommandPersistResult).OK)
	loaded, err = ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateSuccess, loaded.State)
	require.Empty(t, operator.Behavior().(*FormaCommandPersister).activeCommands)
	// After eviction a duplicate completion is a no-op based on persisted
	// terminal rows; acceptance must not keep the stale active cache alive.
	result = operator.Call(sender, completion)
	require.NoError(t, result.Error)
	require.True(t, result.Response.(CommandPersistResult).OK, "%+v", result.Response)
}

func TestAllProducerSourcesResolveAffectedAndEmptyStackIdentitiesAtAdmission(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	for _, label := range []string{"empty", "resource", "cascade"} {
		_, err = ds.CreateStack(&pkgmodel.Stack{ID: "real-" + label, Label: label}, "initial")
		require.NoError(t, err)
	}
	operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	for _, source := range []forma_command.Source{forma_command.SourceUser, forma_command.SourceSynchronizer, forma_command.SourceDiscovery, forma_command.SourceAutoReconciler, forma_command.SourceStackExpirer, forma_command.SourceGeneratorRotator} {
		command := newFormaCommandWithCreateResourceUpdate()
		command.Source = source
		command.Stacks = []forma_command.CommandStack{{ID: "forged", Label: "empty"}}
		command.ResourceUpdates[0].StackLabel = "cascade"
		command.ResourceUpdates[0].DesiredState.Stack = "resource"
		result := operator.Call(sender, StoreNewFormaCommand{Command: *command})
		require.NoError(t, result.Error)
		require.True(t, result.Response.(CommandPersistResult).OK)
		persisted, err := ds.GetFormaCommandByCommandID(command.ID)
		require.NoError(t, err)
		require.Equal(t, source, persisted.Source)
		require.ElementsMatch(t, []forma_command.CommandStack{{ID: "real-empty", Label: "empty"}, {ID: "real-resource", Label: "resource"}, {ID: "real-cascade", Label: "cascade"}}, persisted.Stacks)
		// Each producer case is independent. Finish this synthetic command so
		// command-level exclusion does not make the next matrix row overlap it.
		require.NoError(t, ds.UpdateFormaCommandProgress(command.ID, forma_command.CommandStateSuccess, time.Now().UTC()))
	}
}

func TestDiscoveryVirtualStackDoesNotFabricateMembership(t *testing.T) {
	operator, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)
	command := newFormaCommandWithCreateResourceUpdate()
	command.Source = forma_command.SourceDiscovery
	command.ResourceUpdates[0].StackLabel = "unmanaged"
	command.ResourceUpdates[0].DesiredState.Stack = "unmanaged"
	command.Stacks = []forma_command.CommandStack{{ID: "forged", Label: "unmanaged"}}
	result := operator.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, result.Error)
	require.True(t, result.Response.(CommandPersistResult).OK)
	ds := operator.Behavior().(*FormaCommandPersister).datastore
	persisted, err := ds.GetFormaCommandByCommandID(command.ID)
	require.NoError(t, err)
	require.Empty(t, persisted.Stacks, "virtual inventory scope has no managed stack incarnation")
	require.Equal(t, []string{"unmanaged"}, persisted.GetStackLabels())
}

// Removing command-level exclusion from the persister admits a second mutator
// after the first command's last RU is terminal but its command is not.
func TestUnguardedPersisterRejectsNonterminalCommandMembership(t *testing.T) {
	ds, err := dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{Sqlite: pkgmodel.SqliteConfig{FilePath: ":memory:"}}, "test")
	require.NoError(t, err)
	defer ds.Close()
	for _, label := range []string{"test-stack", "free"} {
		_, err = ds.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
	}

	active := newFormaCommandWithCreateResourceUpdate()
	active.State = forma_command.CommandStateInProgress
	active.ResourceUpdates[0].State = types.ResourceUpdateStateSuccess
	active.ModifiedTs = time.Now().UTC()
	require.NoError(t, ds.StoreFormaCommand(active, active.ID))

	operator, sender, err := newFormaCommandPersisterWithDatastore(t, ds)
	require.NoError(t, err)
	conflicting := newFormaCommandWithCreateResourceUpdate()
	conflicting.ID = util.NewID()
	conflicting.Command = pkgmodel.CommandDestroy
	conflicting.Source = forma_command.SourceStackExpirer
	result := operator.Call(sender, StoreNewFormaCommand{Command: *conflicting})
	require.NoError(t, result.Error)
	require.ErrorIs(t, result.Response.(CommandPersistResult).CallFailure(), datastore.ErrCommandConflict)
	stored, err := ds.GetFormaCommandByCommandID(conflicting.ID)
	require.Error(t, err)
	require.Nil(t, stored)

	guarded := newFormaCommandWithCreateResourceUpdate()
	guarded.ID = util.NewID()
	guarded.Command = pkgmodel.CommandDestroy
	guarded.Source = forma_command.SourceStackExpirer
	require.NoError(t, guarded.ResolveStackIdentities(ds))
	guards, err := ds.(datastore.CommandAdmitter).ReadAdmissionRevisions([]string{datastore.AdmissionStackMappingGuard})
	require.NoError(t, err)
	admission := &datastore.CommandAdmission{Guards: guards, PrincipalScope: "stack-expirer", IdempotencyKey: guarded.ID, RequestDigest: strings.Repeat("a", 64), Receipt: []byte(`{"producer":"test"}`)}
	result = operator.Call(sender, StoreNewFormaCommand{Command: *guarded, Admission: admission})
	require.NoError(t, result.Error)
	require.ErrorIs(t, result.Response.(CommandPersistResult).CallFailure(), datastore.ErrCommandConflict)
	require.NoError(t, ds.UpdateFormaCommandProgress(active.ID, forma_command.CommandStateSuccess, time.Now().UTC()))

	metadataOnly := &forma_command.FormaCommand{
		ID: util.NewID(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser,
		State: forma_command.CommandStateInProgress, StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(),
		Stacks: []forma_command.CommandStack{{ID: "real-test-stack", Label: "test-stack"}},
	}
	require.NoError(t, ds.StoreFormaCommand(metadataOnly, metadataOnly.ID))
	metadataConflict := newFormaCommandWithCreateResourceUpdate()
	metadataConflict.ID = util.NewID()
	result = operator.Call(sender, StoreNewFormaCommand{Command: *metadataConflict})
	require.NoError(t, result.Error)
	require.ErrorIs(t, result.Response.(CommandPersistResult).CallFailure(), datastore.ErrCommandConflict,
		"metadata-only membership remains protected without a resource update")

	independent := newFormaCommandWithCreateResourceUpdate()
	independent.ID = util.NewID()
	independent.ResourceUpdates[0].StackLabel = "free"
	independent.ResourceUpdates[0].DesiredState.Stack = "free"
	result = operator.Call(sender, StoreNewFormaCommand{Command: *independent})
	require.NoError(t, result.Error)
	require.NoError(t, result.Response.(CommandPersistResult).CallFailure())

	readOnly := newFormaCommandWithCreateResourceUpdate()
	readOnly.ID = util.NewID()
	readOnly.Command = pkgmodel.CommandSync
	readOnly.Source = forma_command.SourceSynchronizer
	result = operator.Call(sender, StoreNewFormaCommand{Command: *readOnly})
	require.NoError(t, result.Error)
	require.NoError(t, result.Response.(CommandPersistResult).CallFailure())
}
