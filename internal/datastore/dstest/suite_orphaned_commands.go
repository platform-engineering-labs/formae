// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package dstest

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
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

		ids, err := ds.ConflictingCommandIDsForStacks([]string{blocked})
		require.NoError(t, err)
		want := []string{legacySync.ID, legacyState.ID}
		sort.Strings(want)
		require.Equal(t, want, ids, "every command that blocks admission must be listed, and no other")

		modified := func(id string) (time.Time, time.Time) {
			c, err := ds.GetFormaCommandByCommandID(id)
			require.NoError(t, err)
			require.Len(t, c.ResourceUpdates, 1)
			return c.ModifiedTs, c.ResourceUpdates[0].ModifiedTs
		}
		runningModified, runningUpdateModified := modified(running.ID)

		sweptAfter := time.Now().Add(-time.Second)
		count, err := ds.FailOrphanedCommands()
		require.NoError(t, err)
		require.Equal(t, 3, count)

		// The status API reports modified_ts as the end time, so the swept rows
		// carry the time of the sweep, one value for all of them, not the time
		// the abandoned command last made progress.
		recoveredAt, _ := modified(legacySync.ID)
		require.True(t, recoveredAt.After(sweptAfter), "swept at %s, modified_ts %s", sweptAfter, recoveredAt)
		for _, c := range []*forma_command.FormaCommand{legacySync, legacyState, interruptedSync} {
			commandState, updateState := state(c.ID)
			require.Equal(t, forma_command.CommandStateFailed, commandState)
			require.Equal(t, resource_update.ResourceUpdateStateFailed, updateState)
			commandModified, updateModified := modified(c.ID)
			require.True(t, recoveredAt.Equal(commandModified), "command modified_ts %s, want %s", commandModified, recoveredAt)
			require.True(t, recoveredAt.Equal(updateModified), "resource update modified_ts %s, want %s", updateModified, recoveredAt)
		}
		commandState, updateState := state(running.ID)
		require.Equal(t, forma_command.CommandStateInProgress, commandState, "a resumable command belongs to startup recovery")
		require.Equal(t, resource_update.ResourceUpdateStateInProgress, updateState)
		commandModified, updateModified := modified(running.ID)
		require.True(t, runningModified.Equal(commandModified) && runningUpdateModified.Equal(updateModified), "a resumable command must not be restamped")
		commandState, _ = state(finished.ID)
		require.Equal(t, forma_command.CommandStateSuccess, commandState)

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

		// One query per chunk of labels: blockers found by different chunks
		// are all named. The fillers sort between the two stacks that have one.
		spread := "orphan-spread-" + util.NewID()
		spreadCommand := store(spread, pkgmodel.CommandApply, forma_command.CommandStateInProgress, forma_command.SourceUser)
		labels := []string{resumable, spread}
		for i := range 400 {
			labels = append(labels, fmt.Sprintf("orphan-rf-%d", i))
		}
		want = []string{running.ID, spreadCommand.ID}
		sort.Strings(want)
		ids, err = ds.ConflictingCommandIDsForStacks(labels)
		require.NoError(t, err)
		require.Equal(t, want, ids)

		// A rejection names a bounded number of commands however many block.
		crowded := "orphan-crowded-" + util.NewID()
		blocking := map[string]bool{running.ID: true}
		for range datastore.MaxListedConflictingCommands + 3 {
			blocking[store(crowded, pkgmodel.CommandApply, forma_command.CommandStateInProgress, forma_command.SourceUser).ID] = true
		}
		ids, err = ds.ConflictingCommandIDsForStacks([]string{crowded, resumable})
		require.NoError(t, err)
		require.Len(t, ids, datastore.MaxListedConflictingCommands)
		require.True(t, sort.StringsAreSorted(ids))
		for _, id := range ids {
			require.True(t, blocking[id], "%s does not block these stacks", id)
		}
	})
}
