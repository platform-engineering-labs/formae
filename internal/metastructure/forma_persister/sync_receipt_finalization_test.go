// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_persister

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
)

func twoResourceSyncCommand() *forma_command.FormaCommand {
	command := newSyncFormaCommand()
	command.ID = "two-resource-sync"
	command.Source = forma_command.SourceSynchronizer
	second := command.ResourceUpdates[0]
	second.DesiredState.Ksuid = util.NewID()
	second.DesiredState.Label = "second-resource"
	command.ResourceUpdates = append(command.ResourceUpdates, second)
	return command
}

func completeSyncResource(command *forma_command.FormaCommand, index int, version string, state resource_update.ResourceUpdateState) messages.MarkResourceUpdateAsComplete {
	update := command.ResourceUpdates[index]
	return messages.MarkResourceUpdateAsComplete{
		CommandID:          command.ID,
		ResourceURI:        update.DesiredState.URI(),
		Operation:          update.Operation,
		FinalState:         state,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow(),
		Version:            version,
	}
}

func TestFormaCommandPersister_EmptySyncWaitsForAllCompletionsBeforeDeletion(t *testing.T) {
	command := twoResourceSyncCommand()
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	stored := persister.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	first := persister.Call(sender, completeSyncResource(command, 0, "", resource_update.ResourceUpdateStateSuccess))
	require.NoError(t, first.Error)
	require.True(t, first.Response.(CommandPersistResult).OK)

	loaded := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, loaded.Error)
	response := loaded.Response.(LoadFormaCommandResult)
	require.Empty(t, response.Error)
	require.NotNil(t, response.Command, "the command must remain until every expected completion arrives")
	require.Equal(t, 1, persister.Behavior().(*FormaCommandPersister).activeCommands[command.ID].pendingCompletions)

	second := persister.Call(sender, completeSyncResource(command, 1, "", resource_update.ResourceUpdateStateSuccess))
	require.NoError(t, second.Error)
	require.True(t, second.Response.(CommandPersistResult).OK)

	deleted := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, deleted.Error)
	require.Contains(t, deleted.Response.(LoadFormaCommandResult).Error, "forma command not found")
}

func TestFormaCommandPersister_MixedSyncHistoryRetainsVersionedEvent(t *testing.T) {
	command := twoResourceSyncCommand()
	command.ID = "mixed-sync-history"
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	stored := persister.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	empty := persister.Call(sender, completeSyncResource(command, 0, "", resource_update.ResourceUpdateStateSuccess))
	require.NoError(t, empty.Error)
	require.True(t, empty.Response.(CommandPersistResult).OK)

	versioned := persister.Call(sender, completeSyncResource(command, 1, "physical-version", resource_update.ResourceUpdateStateSuccess))
	require.NoError(t, versioned.Error)
	require.True(t, versioned.Response.(CommandPersistResult).OK)

	loaded := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, loaded.Error)
	response := loaded.Response.(LoadFormaCommandResult)
	require.Empty(t, response.Error)
	require.NotNil(t, response.Command)
	require.Equal(t, forma_command.CommandStateSuccess, response.Command.State)
	require.Len(t, response.Command.ResourceUpdates, 1, "only the real version event belongs in persisted sync history")
	require.Equal(t, "physical-version", response.Command.ResourceUpdates[0].Version)
}

func TestFormaCommandPersister_EmptyFailedSyncKeepsExistingFailurePolicy(t *testing.T) {
	command := newSyncFormaCommand()
	command.ID = "failed-empty-sync"
	command.Source = forma_command.SourceSynchronizer
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	stored := persister.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	failed := persister.Call(sender, completeSyncResource(command, 0, "", resource_update.ResourceUpdateStateFailed))
	require.NoError(t, failed.Error)
	require.True(t, failed.Response.(CommandPersistResult).OK)

	deleted := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, deleted.Error)
	require.Contains(t, deleted.Response.(LoadFormaCommandResult).Error, "forma command not found",
		"failure without persisted detail must keep the existing empty-sync pruning policy")
}

func TestFormaCommandPersister_EmptyDiscoverySyncKeepsExistingPruningPolicy(t *testing.T) {
	command := newSyncFormaCommand()
	command.ID = "empty-discovery-sync"
	command.Source = forma_command.SourceDiscovery
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	stored := persister.Call(sender, StoreNewFormaCommand{Command: *command})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	completed := persister.Call(sender, completeSyncResource(command, 0, "", resource_update.ResourceUpdateStateSuccess))
	require.NoError(t, completed.Error)
	require.True(t, completed.Response.(CommandPersistResult).OK)

	deleted := persister.Call(sender, LoadFormaCommand{CommandID: command.ID})
	require.NoError(t, deleted.Error)
	require.Contains(t, deleted.Response.(LoadFormaCommandResult).Error, "forma command not found",
		"discovery without a version event must keep the existing empty-sync pruning policy")
}
