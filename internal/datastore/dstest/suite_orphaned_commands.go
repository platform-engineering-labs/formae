// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"sort"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

// RunOrphanedCommandsTest covers the commands that block admission but that
// startup recovery never loads: every one of them must be named by the
// conflict listing, and finalized by FailOrphanedCommands.
func RunOrphanedCommandsTest(t *testing.T, newDS func(t *testing.T) TestDatastore) {
	t.Run("OrphanedCommandsTest", func(t *testing.T) {
		td := newDS(t)
		ds := td.Datastore
		defer td.CleanUpFn() //nolint:errcheck

		blocked, resumable := "orphan-blocked-"+util.NewID(), "orphan-resumable-"+util.NewID()
		store := func(stack string, command pkgmodel.Command, state forma_command.CommandState, source forma_command.Source) *forma_command.FormaCommand {
			c := lifecycleCommand(stack, util.NewID(), `{"value":"a"}`, state, -time.Minute)
			c.Command, c.Source = command, source
			c.ResourceUpdates[0].State = resource_update.ResourceUpdateStateInProgress
			require.NoError(t, ds.StoreFormaCommand(c, c.ID))
			if command == pkgmodel.CommandSync {
				// A sync only persists the resource updates that saw a change.
				require.NoError(t, ds.BulkStoreResourceUpdates(c.ID, c.ResourceUpdates))
			}
			return c
		}
		// A sync written before commands recorded their source.
		legacySync := store(blocked, pkgmodel.CommandSync, forma_command.CommandStateInProgress, "")
		// A state this build no longer writes.
		legacyState := store(blocked, pkgmodel.CommandApply, forma_command.CommandStatePending, forma_command.SourceUser)
		interruptedSync := store(blocked, pkgmodel.CommandSync, forma_command.CommandStateInProgress, forma_command.SourceSynchronizer)
		running := store(resumable, pkgmodel.CommandApply, forma_command.CommandStateInProgress, forma_command.SourceUser)
		finished := store(blocked, pkgmodel.CommandApply, forma_command.CommandStateSuccess, forma_command.SourceUser)

		state := func(id string) (forma_command.CommandState, resource_update.ResourceUpdateState) {
			c, err := ds.GetFormaCommandByCommandID(id)
			require.NoError(t, err)
			require.Len(t, c.ResourceUpdates, 1)
			return c.State, c.ResourceUpdates[0].State
		}

		conflict, err := ds.HasConflictingCommandForStacks([]string{blocked})
		require.NoError(t, err)
		require.True(t, conflict)
		ids, err := ds.ConflictingCommandIDsForStacks([]string{blocked})
		require.NoError(t, err)
		want := []string{legacySync.ID, legacyState.ID}
		sort.Strings(want)
		require.Equal(t, want, ids, "every command that blocks admission must be listed, and no other")

		count, err := ds.FailOrphanedCommands()
		require.NoError(t, err)
		require.Equal(t, 3, count)

		for _, c := range []*forma_command.FormaCommand{legacySync, legacyState, interruptedSync} {
			commandState, updateState := state(c.ID)
			require.Equal(t, forma_command.CommandStateFailed, commandState)
			require.Equal(t, resource_update.ResourceUpdateStateFailed, updateState)
		}
		commandState, updateState := state(running.ID)
		require.Equal(t, forma_command.CommandStateInProgress, commandState, "a resumable command belongs to startup recovery")
		require.Equal(t, resource_update.ResourceUpdateStateInProgress, updateState)
		commandState, _ = state(finished.ID)
		require.Equal(t, forma_command.CommandStateSuccess, commandState)

		conflict, err = ds.HasConflictingCommandForStacks([]string{blocked})
		require.NoError(t, err)
		require.False(t, conflict)
		ids, err = ds.ConflictingCommandIDsForStacks([]string{blocked})
		require.NoError(t, err)
		require.Empty(t, ids)
		ids, err = ds.ConflictingCommandIDsForStacks([]string{resumable})
		require.NoError(t, err)
		require.Equal(t, []string{running.ID}, ids)

		// What still blocks admission is exactly what recovery will resume.
		incomplete, err := ds.LoadIncompleteFormaCommands()
		require.NoError(t, err)
		require.Len(t, incomplete, 1)
		require.Equal(t, running.ID, incomplete[0].ID)

		count, err = ds.FailOrphanedCommands()
		require.NoError(t, err)
		require.Zero(t, count)
	})
}
