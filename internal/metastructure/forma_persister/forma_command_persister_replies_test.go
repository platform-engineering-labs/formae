// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package forma_persister

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	dssqlite "github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// A progress update for a command the datastore does not hold is a
// request-scoped failure: the caller must get an answer and the persister
// must keep serving. Terminating instead kills every request queued in the
// mailbox.
func TestFormaCommandPersister_UnknownCommand_RepliesAndStaysAlive(t *testing.T) {
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	result := persister.Call(sender, messages.UpdateResourceProgress{
		CommandID:   "no-such-command",
		ResourceURI: pkgmodel.NewFormaeURI(util.NewID(), ""),
		Operation:   resource_update.OperationCreate,
		Progress: plugin.TrackedProgress{
			ProgressResult: resource.ProgressResult{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusSuccess,
			},
		},
	})

	require.NoError(t, result.Error, "an unknown command must be answered, not terminate the persister")
	failed, ok := result.Response.(CommandPersistResult)
	require.True(t, ok, "the caller must receive a typed reply, got %T", result.Response)
	require.NotEmpty(t, failed.Error)

	// The same actor instance must keep serving: storing and loading a real
	// command still works.
	cmd := newFormaCommandWithCreateResourceUpdate()
	stored := persister.Call(sender, StoreNewFormaCommand{Command: *cmd})
	require.NoError(t, stored.Error, "the persister must still serve after a failed request")
	storedRes, ok := stored.Response.(CommandPersistResult)
	require.True(t, ok, "expected a typed reply, got %T", stored.Response)
	assert.Empty(t, storedRes.Error, "a valid request after a failure must succeed")
}

func newTestDatastoreForReplies() (datastore.Datastore, error) {
	return dssqlite.NewDatastoreSQLite(context.Background(), &pkgmodel.DatastoreConfig{
		DatastoreType: pkgmodel.SqliteDatastore,
		Sqlite:        pkgmodel.SqliteConfig{FilePath: ":memory:"},
	}, "test-agent-id")
}

// commandStoreFailingOnce wraps a real datastore and fails StoreFormaCommand
// while failures remain, the shape of a transient write failure.
type commandStoreFailingOnce struct {
	datastore.Datastore
	failures int
}

func (s *commandStoreFailingOnce) StoreFormaCommand(command *forma_command.FormaCommand, commandID string) error {
	if s.failures > 0 {
		s.failures--
		return errors.New("transient store failure")
	}
	return s.Datastore.StoreFormaCommand(command, commandID)
}

// A completion whose command write fails leaves the cached command mutated
// (terminal) while the datastore still holds the old state. Surviving the
// failure must not strand that divergence: the next request touching the
// command has to flush the cached state to the datastore before it is
// acknowledged, or the stored command stays non-terminal forever, locking its
// stack until an agent restart.
func TestFormaCommandPersister_FailedFinalWrite_FlushedOnNextTouch(t *testing.T) {
	real, err := newTestDatastoreForReplies()
	require.NoError(t, err)
	store := &commandStoreFailingOnce{Datastore: real}

	persister, sender, err := newFormaCommandPersisterWithDatastore(t, store)
	require.NoError(t, err)

	cmd := newFormaCommandWithCreateResourceUpdate()
	stored := persister.Call(sender, StoreNewFormaCommand{Command: *cmd})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	// The command's only resource completes, but the final command write fails.
	store.failures = 2
	completion := messages.MarkResourceUpdateAsComplete{
		CommandID:          cmd.ID,
		ResourceURI:        cmd.ResourceUpdates[0].DesiredState.URI(),
		Operation:          cmd.ResourceUpdates[0].Operation,
		FinalState:         resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow(),
		ResourceProperties: cmd.ResourceUpdates[0].DesiredState.Properties,
		Version:            "v1",
	}
	result := persister.Call(sender, completion)
	require.NoError(t, result.Error, "the failed write must be answered, not terminate the persister")
	completionRes, ok := result.Response.(CommandPersistResult)
	require.True(t, ok, "expected a typed reply, got %T", result.Response)
	require.NotEmpty(t, completionRes.Error, "the failed write must be reported to the caller")

	// The datastore still holds the pre-completion state.
	stale, err := real.GetFormaCommandByCommandID(cmd.ID)
	require.NoError(t, err)
	require.False(t, stale.IsInFinalState(), "the failed write must not have reached the datastore")

	// While the datastore is still failing, a touch must not be acknowledged
	// from the undurable cache: the flush failure fails the request.
	blocked := persister.Call(sender, LoadFormaCommand{CommandID: cmd.ID})
	require.NoError(t, blocked.Error)
	blockedRes, ok := blocked.Response.(LoadFormaCommandResult)
	require.True(t, ok, "expected a typed reply, got %T", blocked.Response)
	require.NotEmpty(t, blockedRes.Error, "a touch while the flush still fails must be answered with the failure")

	// Once the datastore recovers, the next touch flushes the cached state.
	load := persister.Call(sender, LoadFormaCommand{CommandID: cmd.ID})
	require.NoError(t, load.Error)
	loadRes, ok := load.Response.(LoadFormaCommandResult)
	require.True(t, ok, "expected a typed reply, got %T", load.Response)
	require.Empty(t, loadRes.Error, "the load itself must succeed")

	flushed, err := real.GetFormaCommandByCommandID(cmd.ID)
	require.NoError(t, err)
	assert.True(t, flushed.IsInFinalState(),
		"the cached terminal state must reach the datastore on the next touch, not wait for a restart")
}

// deleteFailingOnce wraps a real datastore and fails the next
// DeleteFormaCommand while armed.
type deleteFailingOnce struct {
	datastore.Datastore
	armed bool
}

func (s *deleteFailingOnce) DeleteFormaCommand(command *forma_command.FormaCommand, commandID string) error {
	if s.armed {
		s.armed = false
		return errors.New("transient delete failure")
	}
	return s.Datastore.DeleteFormaCommand(command, commandID)
}

// An empty sync command is deleted at finalization. When that delete fails
// transiently, the flush on the next touch must preserve the finalization
// semantics and delete the command, not store a command that should not
// exist.
func TestFormaCommandPersister_FailedSyncDelete_DeletedOnNextTouch(t *testing.T) {
	real, err := newTestDatastoreForReplies()
	require.NoError(t, err)
	store := &deleteFailingOnce{Datastore: real}

	persister, sender, err := newFormaCommandPersisterWithDatastore(t, store)
	require.NoError(t, err)

	cmd := newSyncFormaCommand()
	stored := persister.Call(sender, StoreNewFormaCommand{Command: *cmd})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	// The sync command's only resource completes with no version, which makes
	// the command an empty sync command due for deletion; the delete fails.
	store.armed = true
	result := persister.Call(sender, messages.MarkResourceUpdateAsComplete{
		CommandID:          cmd.ID,
		ResourceURI:        cmd.ResourceUpdates[0].DesiredState.URI(),
		Operation:          cmd.ResourceUpdates[0].Operation,
		FinalState:         resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow(),
		ResourceProperties: cmd.ResourceUpdates[0].DesiredState.Properties,
		Version:            "",
	})
	require.NoError(t, result.Error)
	deleteRes, ok := result.Response.(CommandPersistResult)
	require.True(t, ok, "expected a typed reply, got %T", result.Response)
	require.NotEmpty(t, deleteRes.Error, "the failed delete must be reported")

	// The next touch must delete the command, not store it.
	load := persister.Call(sender, LoadFormaCommand{CommandID: cmd.ID})
	require.NoError(t, load.Error)
	loadRes, ok := load.Response.(LoadFormaCommandResult)
	require.True(t, ok, "expected a typed reply, got %T", load.Response)
	assert.Contains(t, loadRes.Error, "not found")

	_, err = real.GetFormaCommandByCommandID(cmd.ID)
	require.Error(t, err, "the sync command must be deleted from the datastore, not stored")
}

// An unknown request type is a protocol bug, not a request-scoped outcome:
// the error return keeps the meaning ergo assigns to it and terminates the
// actor, so supervision surfaces the defect instead of a reply masking it.
func TestFormaCommandPersister_UnknownRequestType_Terminates(t *testing.T) {
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	type notARequest struct{}
	result := persister.Call(sender, notARequest{})
	require.Error(t, result.Error, "an unknown request type must terminate the actor")
}

// progressWriteFailingOnce wraps a real datastore and fails the next
// command-meta progress write while failures remain.
type progressWriteFailingOnce struct {
	datastore.Datastore
	failures int
}

func (s *progressWriteFailingOnce) UpdateFormaCommandProgress(commandID string, state forma_command.CommandState, modifiedTs time.Time) error {
	if s.failures > 0 {
		s.failures--
		return errors.New("transient progress-write failure")
	}
	return s.Datastore.UpdateFormaCommandProgress(commandID, state, modifiedTs)
}

// A NON-final completion (other resources still pending) whose command-meta
// write fails leaves the cached command state ahead of the datastore. The
// failure must mark the cache dirty so the next touch flushes it; otherwise
// the stored command row stays stale until some later write happens to
// rewrite it.
func TestFormaCommandPersister_FailedNonFinalMetaWrite_FlushedOnNextTouch(t *testing.T) {
	real, err := newTestDatastoreForReplies()
	require.NoError(t, err)
	store := &progressWriteFailingOnce{Datastore: real}

	persister, sender, err := newFormaCommandPersisterWithDatastore(t, store)
	require.NoError(t, err)

	// Two resources, so completing one leaves the command non-final.
	cmd := newFormaCommandWithCreateResourceUpdate()
	second := cmd.ResourceUpdates[0]
	second.DesiredState.Label = "second-resource"
	second.DesiredState.Ksuid = util.NewID()
	cmd.ResourceUpdates = append(cmd.ResourceUpdates, second)

	stored := persister.Call(sender, StoreNewFormaCommand{Command: *cmd})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	store.failures = 1
	result := persister.Call(sender, messages.MarkResourceUpdateAsComplete{
		CommandID:          cmd.ID,
		ResourceURI:        cmd.ResourceUpdates[0].DesiredState.URI(),
		Operation:          cmd.ResourceUpdates[0].Operation,
		FinalState:         resource_update.ResourceUpdateStateSuccess,
		ResourceStartTs:    util.TimeNow(),
		ResourceModifiedTs: util.TimeNow(),
		ResourceProperties: cmd.ResourceUpdates[0].DesiredState.Properties,
		Version:            "v1",
	})
	require.NoError(t, result.Error, "the failed meta write must be answered, not terminate the persister")
	completionRes, ok := result.Response.(CommandPersistResult)
	require.True(t, ok, "expected a typed reply, got %T", result.Response)
	require.NotEmpty(t, completionRes.Error, "the failed meta write must be reported to the caller")

	staleCmd, err := real.GetFormaCommandByCommandID(cmd.ID)
	require.NoError(t, err)
	require.Equal(t, forma_command.CommandStateNotStarted, staleCmd.State,
		"the failed meta write must not have advanced the stored command state")

	// The next touch must flush the cached state to the datastore.
	load := persister.Call(sender, LoadFormaCommand{CommandID: cmd.ID})
	require.NoError(t, load.Error)
	loadRes, ok := load.Response.(LoadFormaCommandResult)
	require.True(t, ok, "expected a typed reply, got %T", load.Response)
	require.Empty(t, loadRes.Error)

	flushedCmd, err := real.GetFormaCommandByCommandID(cmd.ID)
	require.NoError(t, err)
	assert.Equal(t, forma_command.CommandStateInProgress, flushedCmd.State,
		"the cached command state must reach the datastore on the next touch")
}

// A progress update whose read-snapshot hashing fails must leave the cached
// command untouched: the failure is answered, and the retried update (with a
// well-formed snapshot) must land instead of being dropped by the terminality
// guard against a half-applied first attempt.
func TestFormaCommandPersister_FailedProgressHash_LeavesCacheUntouched(t *testing.T) {
	persister, sender, err := newFormaCommandPersisterForTest(t)
	require.NoError(t, err)

	cmd := newFormaCommandWithCreateResourceUpdate()
	cmd.ResourceUpdates[0].DesiredState.Schema = pkgmodel.Schema{
		Fields: []string{"Secret"},
		Hints:  map[string]pkgmodel.FieldHint{"Secret": {Opaque: true}},
	}
	stored := persister.Call(sender, StoreNewFormaCommand{Command: *cmd})
	require.NoError(t, stored.Error)
	require.True(t, stored.Response.(CommandPersistResult).OK)

	update := func(props json.RawMessage) messages.UpdateResourceProgress {
		return messages.UpdateResourceProgress{
			CommandID:          cmd.ID,
			ResourceURI:        cmd.ResourceUpdates[0].DesiredState.URI(),
			Operation:          cmd.ResourceUpdates[0].Operation,
			ResourceState:      resource_update.ResourceUpdateStateSuccess,
			ResourceStartTs:    util.TimeNow(),
			ResourceModifiedTs: util.TimeNow(),
			Progress: plugin.TrackedProgress{
				ProgressResult: resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					ResourceProperties: props,
				},
			},
		}
	}

	// A snapshot the hasher cannot process fails the request.
	broken := persister.Call(sender, update(json.RawMessage(`{"Secret":`)))
	require.NoError(t, broken.Error, "a hashing failure must be answered, not terminate the persister")
	brokenRes, ok := broken.Response.(CommandPersistResult)
	require.True(t, ok, "expected a typed reply, got %T", broken.Response)
	require.NotEmpty(t, brokenRes.Error, "the hashing failure must be reported")

	// The retry with a well-formed snapshot must land, not be dropped by the
	// terminality guard against state the failed attempt half-applied.
	retry := persister.Call(sender, update(json.RawMessage(`{"Secret":"s3cret"}`)))
	require.NoError(t, retry.Error)
	retryRes, ok := retry.Response.(CommandPersistResult)
	require.True(t, ok, "expected a typed reply, got %T", retry.Response)
	require.Empty(t, retryRes.Error, "the retried progress update must succeed")

	load := persister.Call(sender, LoadFormaCommand{CommandID: cmd.ID})
	require.NoError(t, load.Error)
	loaded := load.Response.(LoadFormaCommandResult).Command
	require.NotNil(t, loaded)
	assert.Equal(t, resource_update.ResourceUpdateStateSuccess, loaded.ResourceUpdates[0].State,
		"the retried progress must be recorded")
	assert.NotEmpty(t, loaded.ResourceUpdates[0].MostRecentProgressResult.ResourceProperties,
		"the retried snapshot must be recorded")
}
