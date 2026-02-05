// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_persister

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

type FormaCommandPersister struct {
	act.Actor

	datastore datastore.Datastore

	// activeCommands caches in-flight FormaCommands to avoid repeated JSON unmarshaling.
	// Commands are evicted when they reach a final state (Success, Failed, Canceled).
	activeCommands map[string]*cachedCommand
}

// cachedCommand holds an in-memory FormaCommand with optimized lookup structures.
type cachedCommand struct {
	command            *forma_command.FormaCommand
	ksuidOpToIndex     map[string]int // O(1) lookup: "ksuid:operation" -> ResourceUpdates index
	pendingCompletions int            // Number of MarkResourceUpdateAsComplete messages expected
}

// resourceUpdateKey creates a composite key for looking up a ResourceUpdate by ksuid and operation.
// Replace operations are stored as two separate entries (delete + create) with the same ksuid,
// so we need both ksuid and operation to uniquely identify an entry.
func resourceUpdateKey(ksuid string, operation types.OperationType) string {
	return ksuid + ":" + string(operation)
}

// buildResourceUpdateIndex creates a lookup map for O(1) access to ResourceUpdates.
// Uses composite key "ksuid:operation" since Replace operations store two entries
// (delete + create) with the same ksuid.
func buildResourceUpdateIndex(cmd *forma_command.FormaCommand) map[string]int {
	ksuidOpToIndex := make(map[string]int, len(cmd.ResourceUpdates))
	for i, ru := range cmd.ResourceUpdates {
		key := resourceUpdateKey(ru.DesiredState.Ksuid, types.OperationType(ru.Operation))
		ksuidOpToIndex[key] = i
	}
	return ksuidOpToIndex
}

// countNonFinalResources returns the number of resource updates not yet in final state.
// Used when loading a command from DB to initialize the pending completions counter.
func countNonFinalResources(cmd *forma_command.FormaCommand) int {
	count := 0
	for _, ru := range cmd.ResourceUpdates {
		if !isResourceInFinalState(ru.State) {
			count++
		}
	}
	return count
}

// isResourceInFinalState checks if a resource update is in a final state.
func isResourceInFinalState(state types.ResourceUpdateState) bool {
	return state == types.ResourceUpdateStateSuccess ||
		state == types.ResourceUpdateStateFailed ||
		state == types.ResourceUpdateStateRejected ||
		state == types.ResourceUpdateStateCanceled
}

// findResourceUpdateIndex returns the index of the ResourceUpdate with the given ksuid and operation.
// Returns -1 if not found.
func (c *cachedCommand) findResourceUpdateIndex(ksuid string, operation types.OperationType) int {
	key := resourceUpdateKey(ksuid, operation)
	if idx, ok := c.ksuidOpToIndex[key]; ok {
		return idx
	}
	return -1
}

func NewFormaCommandPersister() gen.ProcessBehavior {
	return &FormaCommandPersister{}
}

func (f *FormaCommandPersister) Init(args ...any) error {
	ds, ok := f.Env("Datastore")
	if !ok {
		f.Log().Error("FormaCommandPersister: missing 'Datastore' environment variable")
		return fmt.Errorf("formaCommandPersister: missing 'Datastore' environment variable")
	}
	f.datastore = ds.(datastore.Datastore)
	f.activeCommands = make(map[string]*cachedCommand)

	f.Log().Debug("FormaCommandPersister initialized: datastoreType=%s", fmt.Sprintf("%T", f.datastore))

	return nil
}

// getOrLoadCommand retrieves a command from cache or loads it from the database.
// The command is cached for subsequent accesses until it reaches a final state.
func (f *FormaCommandPersister) getOrLoadCommand(commandID string) (*cachedCommand, error) {
	// Check cache first
	if cached, ok := f.activeCommands[commandID]; ok {
		return cached, nil
	}

	// Load from DB
	cmd, err := f.datastore.GetFormaCommandByCommandID(commandID)
	if err != nil {
		return nil, err
	}

	// Build cache entry
	cached := &cachedCommand{
		command:            cmd,
		ksuidOpToIndex:     buildResourceUpdateIndex(cmd),
		pendingCompletions: countNonFinalResources(cmd),
	}
	f.activeCommands[commandID] = cached

	return cached, nil
}

// persistCommand writes the command to the database.
// For simple progress updates, use this directly.
func (f *FormaCommandPersister) persistCommand(cached *cachedCommand) error {
	cmd := cached.command

	if err := f.datastore.StoreFormaCommand(cmd, cmd.ID); err != nil {
		f.Log().Error("Failed to store command", "commandID", cmd.ID, "error", err)
		return fmt.Errorf("failed to store command: %w", err)
	}

	return nil
}

// finalizeAndPersist handles finalization logic after a command completes:
// hashes sensitive data, deletes empty sync commands, and evicts from cache.
// Use this for markResourceUpdateAsComplete and bulkUpdateResourceState.
func (f *FormaCommandPersister) finalizeAndPersist(cached *cachedCommand) error {
	cmd := cached.command

	if _, err := f.hashSensitiveDataIfComplete(cmd); err != nil {
		f.Log().Error("Failed to hash sensitive data", "commandID", cmd.ID, "error", err)
		return fmt.Errorf("failed to hash sensitive data: %w", err)
	}

	shouldDelete := shouldDeleteSyncCommand(cmd, cached)
	if shouldDelete {
		if err := f.datastore.DeleteFormaCommand(cmd, cmd.ID); err != nil {
			f.Log().Error("Failed to delete sync command", "commandID", cmd.ID, "error", err)
			return fmt.Errorf("failed to delete sync command: %w", err)
		}
		delete(f.activeCommands, cmd.ID)
		return nil
	}

	if err := f.datastore.StoreFormaCommand(cmd, cmd.ID); err != nil {
		f.Log().Error("Failed to store command", "commandID", cmd.ID, "error", err)
		return fmt.Errorf("failed to store command: %w", err)
	}

	// Evict from cache if command is complete.
	// For sync commands, we must wait for all completions before evicting because
	// sync commands don't store ResourceUpdates in DB (they rely on in-memory cache).
	// If we evict early, late completions will load from DB and find 0 resources.
	if cmd.IsInFinalState() {
		if cmd.Command == pkgmodel.CommandSync && cached.pendingCompletions > 0 {
			// Don't evict yet - sync command still waiting for completions
			return nil
		}
		delete(f.activeCommands, cmd.ID)
	}

	return nil
}

type StoreNewFormaCommand struct {
	Command forma_command.FormaCommand
}

type LoadFormaCommand struct {
	CommandID string
}

// ResourceUpdateRef identifies a specific ResourceUpdate by URI and operation.
// This is needed because Replace operations create two entries (delete + create)
// with the same KSUID, so URI alone is not sufficient.
type ResourceUpdateRef struct {
	URI       pkgmodel.FormaeURI
	Operation types.OperationType
}

type MarkResourcesAsRejected struct {
	CommandID          string
	Resources          []ResourceUpdateRef
	ResourceModifiedTs time.Time
}

type MarkResourcesAsFailed struct {
	CommandID          string
	Resources          []ResourceUpdateRef
	ResourceModifiedTs time.Time
}

type MarkResourcesAsCanceled struct {
	CommandID string
	Resources []ResourceUpdateRef
}

func (f *FormaCommandPersister) HandleCall(from gen.PID, ref gen.Ref, message any) (any, error) {
	switch msg := message.(type) {
	case StoreNewFormaCommand:
		return f.storeNewFormaCommand(&msg.Command)
	case LoadFormaCommand:
		return f.loadFormaCommand(msg.CommandID)
	case messages.UpdateResourceProgress:
		return f.updateCommandFromProgress(&msg)
	case messages.UpdateTargetStates:
		return f.updateTargetStates(&msg)
	case messages.UpdateStackStates:
		return f.updateStackStates(&msg)
	case MarkResourcesAsRejected:
		return f.markResourcesAsRejected(&msg)
	case MarkResourcesAsFailed:
		return f.markResourcesAsFailed(&msg)
	case MarkResourcesAsCanceled:
		return f.markResourcesAsCanceled(&msg)
	case messages.MarkResourceUpdateAsComplete:
		return f.markResourceUpdateAsComplete(&msg)
	default:
		return nil, fmt.Errorf("unhandled message type: %T", msg)
	}
}

func (f *FormaCommandPersister) storeNewFormaCommand(command *forma_command.FormaCommand) (bool, error) {
	f.Log().Debug("Storing new Forma command", "commandID", command.ID, "commandType", command.Command)

	// Store the command metadata and ResourceUpdates (StoreFormaCommand handles both)
	err := f.datastore.StoreFormaCommand(command, command.ID)
	if err != nil {
		f.Log().Error("Failed to store new Forma command", "error", err)
		return false, fmt.Errorf("failed to store new Forma command: %w", err)
	}

	// Cache the command to avoid immediate database reload on next message
	f.activeCommands[command.ID] = &cachedCommand{
		command:            command,
		ksuidOpToIndex:     buildResourceUpdateIndex(command),
		pendingCompletions: len(command.ResourceUpdates),
	}

	f.Log().Debug("Stored and cached new Forma command", "commandID", command.ID)

	return true, nil
}

func (f *FormaCommandPersister) loadFormaCommand(commandID string) (*forma_command.FormaCommand, error) {
	cached, err := f.getOrLoadCommand(commandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command", "commandID", commandID, "error", err)
		return nil, fmt.Errorf("failed to load Forma command: %w", err)
	}
	return cached.command, nil
}

func (f *FormaCommandPersister) updateCommandFromProgress(progress *messages.UpdateResourceProgress) (bool, error) {
	cached, err := f.getOrLoadCommand(progress.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for progress update", "commandID", progress.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for progress update: %w", err)
	}

	command := cached.command

	// Don't update if command is already canceled
	if command.State == forma_command.CommandStateCanceled {
		f.Log().Debug("Ignoring progress update for canceled command", "commandID", progress.CommandID)
		return true, nil
	}

	// Find matching ResourceUpdate by KSUID and operation
	idx := cached.findResourceUpdateIndex(progress.ResourceURI.KSUID(), progress.Operation)
	if idx != -1 {
		res := &command.ResourceUpdates[idx]
		res.State = progress.ResourceState
		res.StartTs = progress.ResourceStartTs
		res.ModifiedTs = progress.ResourceModifiedTs

		// NOTE: Do NOT decrement pendingCompletions here!
		// pendingCompletions tracks expected MarkResourceUpdateAsComplete messages, not state transitions.
		// Progress updates can set state to Success, but completion messages arrive separately.

		// progress.Progress is already a TrackedProgress from the PluginOperator
		tracked := progress.Progress
		tracked.StartTs = progress.ResourceStartTs
		tracked.ModifiedTs = progress.ResourceModifiedTs

		index := slices.IndexFunc(res.ProgressResult, func(p plugin.TrackedProgress) bool {
			return p.Operation == progress.Progress.Operation
		})
		if index == -1 {
			res.ProgressResult = append(res.ProgressResult, tracked)
		} else {
			res.ProgressResult = slices.Replace(res.ProgressResult, index, index+1, tracked)
		}
		res.MostRecentProgressResult = tracked

		if progress.ResourceProperties != nil {
			res.DesiredState.Properties = progress.ResourceProperties
		}

		if progress.ResourceReadOnlyProperties != nil {
			res.DesiredState.ReadOnlyProperties = progress.ResourceReadOnlyProperties
		}

		if progress.Version != "" {
			res.Version = progress.Version
		}

		// Update the normalized resource_updates table
		// Skip for sync commands: resource updates are not stored upfront, progress tracked in-memory only
		if command.Command != pkgmodel.CommandSync {
			if err := f.datastore.UpdateResourceUpdateProgress(
				progress.CommandID,
				progress.ResourceURI.KSUID(),
				progress.Operation,
				progress.ResourceState,
				progress.ResourceModifiedTs,
				progress.Progress,
			); err != nil {
				f.Log().Error("Failed to update resource update progress in normalized table", "commandID", progress.CommandID, "error", err)
				// Continue with command meta update even if normalized update fails
			}
		}
	}

	// The first resource update reporting progress sets the start timestamp for the command
	if command.StartTs.IsZero() {
		command.StartTs = progress.ResourceStartTs
	}
	command.ModifiedTs = progress.ResourceModifiedTs

	command.State = overallCommandState(command)

	// Only update command-level metadata (state, modified_ts)
	// ResourceUpdates are already persisted via UpdateResourceUpdateProgress above
	if err := f.datastore.UpdateFormaCommandProgress(command.ID, command.State, command.ModifiedTs); err != nil {
		f.Log().Error("Failed to update Forma command meta from resource progress", "commandID", progress.CommandID, "error", err)
		return false, fmt.Errorf("failed to update Forma command meta from resource progress: %w", err)
	}

	return true, nil
}

func (f *FormaCommandPersister) updateTargetStates(msg *messages.UpdateTargetStates) (bool, error) {
	f.Log().Debug("Updating Forma command with target states", "commandID", msg.CommandID, "targetCount", len(msg.TargetUpdates))

	cached, err := f.getOrLoadCommand(msg.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for target state update", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for target state update: %w", err)
	}

	command := cached.command
	command.TargetUpdates = msg.TargetUpdates
	command.State = overallCommandState(command)

	if err := f.persistCommand(cached); err != nil {
		f.Log().Error("Failed to update Forma command with target states", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to update Forma command with target states: %w", err)
	}

	f.Log().Debug("Successfully updated Forma command with target states", "commandID", msg.CommandID)
	return true, nil
}

func (f *FormaCommandPersister) updateStackStates(msg *messages.UpdateStackStates) (bool, error) {
	f.Log().Debug("Updating Forma command with stack states", "commandID", msg.CommandID, "stackCount", len(msg.StackUpdates))

	cached, err := f.getOrLoadCommand(msg.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for stack state update", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for stack state update: %w", err)
	}

	command := cached.command
	command.StackUpdates = msg.StackUpdates
	command.State = overallCommandState(command)

	if err := f.persistCommand(cached); err != nil {
		f.Log().Error("Failed to update Forma command with stack states", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to update Forma command with stack states: %w", err)
	}

	f.Log().Debug("Successfully updated Forma command with stack states", "commandID", msg.CommandID)
	return true, nil
}

func (f *FormaCommandPersister) markResourcesAsRejected(msg *MarkResourcesAsRejected) (bool, error) {
	return f.bulkUpdateResourceState(msg.CommandID, msg.Resources, types.ResourceUpdateStateRejected, msg.ResourceModifiedTs)
}

func (f *FormaCommandPersister) markResourcesAsFailed(msg *MarkResourcesAsFailed) (bool, error) {
	return f.bulkUpdateResourceState(msg.CommandID, msg.Resources, types.ResourceUpdateStateFailed, msg.ResourceModifiedTs)
}

func (f *FormaCommandPersister) markResourcesAsCanceled(msg *MarkResourcesAsCanceled) (bool, error) {
	return f.bulkUpdateResourceState(msg.CommandID, msg.Resources, types.ResourceUpdateStateCanceled, util.TimeNow())
}

func (f *FormaCommandPersister) markResourceUpdateAsComplete(msg *messages.MarkResourceUpdateAsComplete) (bool, error) {
	cached, err := f.getOrLoadCommand(msg.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for complete update", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for complete update: %w", err)
	}

	cmd := cached.command

	// O(1) lookup by KSUID and operation
	idx := cached.findResourceUpdateIndex(msg.ResourceURI.KSUID(), msg.Operation)
	if idx != -1 {
		res := &cmd.ResourceUpdates[idx]
		res.State = msg.FinalState
		res.StartTs = msg.ResourceStartTs
		res.ModifiedTs = msg.ResourceModifiedTs

		if msg.ResourceProperties != nil {
			res.DesiredState.Properties = msg.ResourceProperties
		}
		if msg.ResourceReadOnlyProperties != nil {
			res.DesiredState.ReadOnlyProperties = msg.ResourceReadOnlyProperties
		}
		if msg.Version != "" {
			res.Version = msg.Version
		}

		// Persist the resource update to the normalized table
		// For sync commands: resource updates were not stored upfront, so INSERT only if change detected (Version set)
		// For non-sync commands: resource updates exist in DB, so UPDATE the state
		if cmd.Command == pkgmodel.CommandSync {
			// Sync commands only persist resource updates that detected changes
			if msg.Version != "" {
				if err := f.datastore.BulkStoreResourceUpdates(msg.CommandID, []resource_update.ResourceUpdate{*res}); err != nil {
					f.Log().Error("Failed to insert sync resource update with change", "commandID", msg.CommandID, "error", err)
					// Continue with command meta update even if insert fails
				}
			}
			// If no version (no change), nothing to persist - that's the optimization
		} else {
			// Non-sync commands: update the existing resource update state
			if err := f.datastore.UpdateResourceUpdateState(
				msg.CommandID,
				msg.ResourceURI.KSUID(),
				msg.Operation,
				msg.FinalState,
				msg.ResourceModifiedTs,
			); err != nil {
				f.Log().Error("Failed to update resource update state in normalized table", "commandID", msg.CommandID, "error", err)
				// Continue with command meta update even if normalized update fails
			}
		}

		// Always decrement pending completions counter when we receive a completion message.
		// This counter tracks the number of expected MarkResourceUpdateAsComplete messages,
		// NOT the number of resources in non-final state (which can be set by UpdateResourceProgress).
		if cached.pendingCompletions > 0 {
			cached.pendingCompletions--
		}

		cmd.ModifiedTs = msg.ResourceModifiedTs

		// Calculate new command state
		cmd.State = overallCommandState(cmd)

		// If command is in final state, do full finalization (hash sensitive data, potentially delete, etc.)
		// Otherwise just update command meta for performance
		if cmd.IsInFinalState() {
			if err := f.finalizeAndPersist(cached); err != nil {
				return false, err
			}
		} else {
			// Only update command-level metadata (state, modified_ts)
			// ResourceUpdate is already persisted via UpdateResourceUpdateState above
			if err := f.datastore.UpdateFormaCommandProgress(cmd.ID, cmd.State, cmd.ModifiedTs); err != nil {
				f.Log().Error("Failed to update Forma command meta", "commandID", msg.CommandID, "error", err)
				return false, fmt.Errorf("failed to update Forma command meta: %w", err)
			}
		}
	} else {
		// Resource not found in this command - this indicates a bug or unexpected race condition.
		// Log as error but return success to avoid actor crash.
		f.Log().Error("Received completion for resource not in command",
			"commandID", msg.CommandID,
			"resource", msg.ResourceURI.KSUID(),
			"operation", msg.Operation)
	}

	return true, nil
}

func (f *FormaCommandPersister) bulkUpdateResourceState(
	commandID string,
	resources []ResourceUpdateRef,
	state types.ResourceUpdateState,
	modifiedTs time.Time,
) (bool, error) {
	cached, err := f.getOrLoadCommand(commandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for bulk update", "commandID", commandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for bulk update: %w", err)
	}

	command := cached.command

	// O(1) lookup by KSUID and operation for each resource
	for _, ref := range resources {
		idx := cached.findResourceUpdateIndex(ref.URI.KSUID(), ref.Operation)
		if idx != -1 {
			res := &command.ResourceUpdates[idx]

			// If transitioning to final state, decrement counter
			if !isResourceInFinalState(res.State) && isResourceInFinalState(state) {
				if cached.pendingCompletions > 0 {
					cached.pendingCompletions--
				}
			}

			res.State = state
			res.ModifiedTs = modifiedTs
		}
	}

	// Update the normalized resource_updates table in batch
	if len(resources) > 0 {
		datastoreRefs := make([]datastore.ResourceUpdateRef, len(resources))
		for i, ref := range resources {
			datastoreRefs[i] = datastore.ResourceUpdateRef{
				KSUID:     ref.URI.KSUID(),
				Operation: ref.Operation,
			}
		}
		if err := f.datastore.BatchUpdateResourceUpdateState(commandID, datastoreRefs, state, modifiedTs); err != nil {
			f.Log().Error("Failed to batch update resource states in normalized table", "commandID", commandID, "error", err)
			// Continue with command meta update even if normalized update fails
		}
	}

	command.ModifiedTs = modifiedTs
	command.State = overallCommandState(command)

	// If command is in final state, do full finalization (hash sensitive data, potentially delete, etc.)
	// Otherwise just update command meta for performance
	if command.IsInFinalState() {
		if err := f.finalizeAndPersist(cached); err != nil {
			return false, err
		}
	} else {
		// Only update command-level metadata (state, modified_ts)
		// ResourceUpdates are already persisted via BatchUpdateResourceUpdateState above
		if err := f.datastore.UpdateFormaCommandProgress(command.ID, command.State, command.ModifiedTs); err != nil {
			f.Log().Error("Failed to update Forma command meta", "commandID", commandID, "error", err)
			return false, fmt.Errorf("failed to update Forma command meta: %w", err)
		}
	}

	return true, nil
}

func overallCommandState(command *forma_command.FormaCommand) forma_command.CommandState {
	var states []types.ResourceUpdateState
	for _, res := range command.ResourceUpdates {
		states = append(states, res.State)
	}

	// Check if any resources are not in a final state
	hasNonFinalState := slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s != types.ResourceUpdateStateSuccess &&
			s != types.ResourceUpdateStateFailed &&
			s != types.ResourceUpdateStateRejected &&
			s != types.ResourceUpdateStateCanceled
	})

	if hasNonFinalState {
		return forma_command.CommandStateInProgress
	}

	// All resources are in final state, determine which final state
	hasCanceled := slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s == types.ResourceUpdateStateCanceled
	})

	// Check if any resources were canceled
	if hasCanceled {
		return forma_command.CommandStateCanceled
	}

	hasFailedOrRejected := slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s == types.ResourceUpdateStateFailed || s == types.ResourceUpdateStateRejected
	})

	// Check if any resources failed or were rejected
	if hasFailedOrRejected {
		return forma_command.CommandStateFailed
	}

	return forma_command.CommandStateSuccess
}

// shouldDeleteSyncCommand determines if a sync command should be deleted because it has no resource versions.
// This helps keep the database clean by not retaining sync commands that resulted in no changes.
// The cached parameter is used to ensure all completion messages have been processed before deletion.
func shouldDeleteSyncCommand(command *forma_command.FormaCommand, cached *cachedCommand) bool {
	isSync := command.Command == pkgmodel.CommandSync
	isFinal := command.IsInFinalState()
	hasVersions := command.HasResourceVersions()
	allCompleted := cached.pendingCompletions == 0
	return isSync && isFinal && !hasVersions && allCompleted
}

func hasOpaqueValues(props json.RawMessage) bool {
	return bytes.Contains(props, []byte(`"$visibility"`)) &&
		bytes.Contains(props, []byte(`"Opaque"`))
}

// hashSensitiveDataIfComplete checks if the command is in a final state and hashes sensitive data if so.
// This should be called after updating resource states to ensure opaque values are hashed when the command completes.
func (f *FormaCommandPersister) hashSensitiveDataIfComplete(command *forma_command.FormaCommand) (bool, error) {
	// Only hash if the command is in a final state
	if !command.IsInFinalState() {
		return false, nil
	}

	t := transformations.NewPersistValueTransformer()
	hashedCount := 0

	// Hash opaque vals in ResourceUpdates array (Resources are stored in resource_updates table, not forma.Resources)
	for i, resourceUpdate := range command.ResourceUpdates {
		if hasOpaqueValues(resourceUpdate.DesiredState.Properties) {
			transformed, err := t.ApplyToResource(&resourceUpdate.DesiredState)
			if err != nil {
				f.Log().Error("Failed to hash resource update during final cleanup",
					"commandID", command.ID,
					"resourceLabel", resourceUpdate.DesiredState.Label,
					"error", err)
				return false, fmt.Errorf("failed to hash resource update %s during final cleanup: %w", resourceUpdate.DesiredState.Label, err)
			}
			command.ResourceUpdates[i].DesiredState = *transformed
			hashedCount++
		}
	}

	if hashedCount > 0 {
		f.Log().Debug("Hashed sensitive data for completed command",
			"commandID", command.ID,
			"state", command.State,
			"hashedResourceCount", hashedCount)
	}

	return hashedCount > 0, nil
}
