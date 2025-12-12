// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_persister

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
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
	command        *forma_command.FormaCommand
	ksuidOpToIndex map[string]int // O(1) lookup: "ksuid:operation" -> ResourceUpdates index
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
		key := resourceUpdateKey(ru.Resource.Ksuid, types.OperationType(ru.Operation))
		ksuidOpToIndex[key] = i
	}
	return ksuidOpToIndex
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
	cfg, ok := f.Env("DatastoreConfig")
	if !ok {
		f.Log().Error("FormaCommandPersister: missing 'Config' environment variable")
		return fmt.Errorf("formaCommandPersister: missing 'Config' environment variable")
	}
	config := cfg.(pkgmodel.DatastoreConfig)

	_agentID, ok := f.Env("AgentID")
	if !ok {
		f.Log().Error("FormaCommandPersister: missing 'AgentID' environment variable")
		return fmt.Errorf("formaCommandPersister: missing 'AgentID' environment variable")
	}
	agentID := _agentID.(string)

	_ctx, ok := f.Env("Context")
	if !ok {
		f.Log().Error("FormaCommandPersister: missing 'Context' environment variable")
		return fmt.Errorf("formaCommandPersister: missing 'Context' environment variable")
	}
	ctx := _ctx.(context.Context)

	// Initialize the datastore
	var ds datastore.Datastore
	var err error
	if config.DatastoreType == pkgmodel.PostgresDatastore {
		ds, err = datastore.NewDatastorePostgres(ctx, &config, agentID)
	} else {
		ds, err = datastore.NewDatastoreSQLite(ctx, &config, agentID)
	}

	if err != nil {
		f.Log().Error("FormaCommandPersister: failed to initialize datastore", "error", err)
		return fmt.Errorf("formaCommandPersister: failed to initialize datastore: %w", err)
	}
	f.datastore = ds
	f.activeCommands = make(map[string]*cachedCommand)

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
		command:        cmd,
		ksuidOpToIndex: buildResourceUpdateIndex(cmd),
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

	if shouldDeleteSyncCommand(cmd) {
		f.Log().Debug("Deleting sync command with no resource versions", "commandID", cmd.ID)
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

	// Evict from cache if command is complete
	if cmd.IsInFinalState() {
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
	err := f.datastore.StoreFormaCommand(command, command.ID)
	if err != nil {
		f.Log().Error("Failed to store new Forma command", "error", err)
		return false, fmt.Errorf("failed to store new Forma command: %w", err)
	}

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
	f.Log().Debug("Updating Forma command from resource progress", "commandID", progress.CommandID, "resourceURI", progress.ResourceURI)

	cached, err := f.getOrLoadCommand(progress.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for update", "commandID", progress.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for update: %w", err)
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

		index := slices.IndexFunc(res.ProgressResult, func(p resource.ProgressResult) bool {
			return p.Operation == progress.Progress.Operation
		})
		if index == -1 {
			res.ProgressResult = append(res.ProgressResult, progress.Progress)
		} else {
			res.ProgressResult = slices.Replace(res.ProgressResult, index, index+1, progress.Progress)
		}
		res.MostRecentProgressResult = progress.Progress

		if progress.ResourceProperties != nil {
			res.Resource.Properties = progress.ResourceProperties
		}

		if progress.ResourceReadOnlyProperties != nil {
			res.Resource.ReadOnlyProperties = progress.ResourceReadOnlyProperties
		}

		if progress.Version != "" {
			res.Version = progress.Version
		}
	}

	// The first resource update reporting progress sets the start timestamp for the command
	if command.StartTs.IsZero() {
		command.StartTs = progress.ResourceStartTs
	}
	command.ModifiedTs = progress.ResourceModifiedTs
	command.State = overallCommandState(command)

	if err := f.persistCommand(cached); err != nil {
		f.Log().Error("Failed to update Forma command from resource progress", "commandID", progress.CommandID, "error", err)
		return false, fmt.Errorf("failed to update Forma command from resource progress: %w", err)
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
	f.Log().Debug("Marking command resource as complete", "commandID", msg.CommandID, "resourceURI", msg.ResourceURI)

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
			res.Resource.Properties = msg.ResourceProperties
		}
		if msg.ResourceReadOnlyProperties != nil {
			res.Resource.ReadOnlyProperties = msg.ResourceReadOnlyProperties
		}
		if msg.Version != "" {
			res.Version = msg.Version
		}
	}

	cmd.ModifiedTs = msg.ResourceModifiedTs
	cmd.State = overallCommandState(cmd)

	if err := f.finalizeAndPersist(cached); err != nil {
		return false, err
	}

	f.Log().Debug("Successfully marked command resource as complete", "commandID", msg.CommandID)
	return true, nil
}

func (f *FormaCommandPersister) bulkUpdateResourceState(
	commandID string,
	resources []ResourceUpdateRef,
	state types.ResourceUpdateState,
	modifiedTs time.Time,
) (bool, error) {
	f.Log().Debug("Bulk updating resource states", "commandID", commandID, "count", len(resources), "state", state)

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
			res.State = state
			res.ModifiedTs = modifiedTs
		}
	}

	command.ModifiedTs = modifiedTs
	command.State = overallCommandState(command)

	if err := f.finalizeAndPersist(cached); err != nil {
		return false, err
	}

	f.Log().Debug("Successfully bulk updated resource states", "commandID", commandID)
	return true, nil
}

func overallCommandState(command *forma_command.FormaCommand) forma_command.CommandState {
	var states []types.ResourceUpdateState
	for _, res := range command.ResourceUpdates {
		states = append(states, res.State)
	}

	// Check if any resources are not in a final state
	if slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s != types.ResourceUpdateStateSuccess &&
			s != types.ResourceUpdateStateFailed &&
			s != types.ResourceUpdateStateRejected &&
			s != types.ResourceUpdateStateCanceled
	}) {
		return forma_command.CommandStateInProgress
	}

	// Check if any resources were canceled
	if slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s == types.ResourceUpdateStateCanceled
	}) {
		return forma_command.CommandStateCanceled
	}

	// Check if any resources failed or were rejected
	if slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s == types.ResourceUpdateStateFailed || s == types.ResourceUpdateStateRejected
	}) {
		return forma_command.CommandStateFailed
	}

	return forma_command.CommandStateSuccess
}

// shouldDeleteSyncCommand determines if a sync command should be deleted because it has no resource versions.
// This helps keep the database clean by not retaining sync commands that resulted in no changes.
func shouldDeleteSyncCommand(command *forma_command.FormaCommand) bool {
	return command.Command == pkgmodel.CommandSync &&
		command.IsInFinalState() &&
		!command.HasResourceVersions()
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

	// Hash opaque vals in Forma.Resources array
	for i, resource := range command.Forma.Resources {
		if hasOpaqueValues(resource.Properties) {
			transformed, err := t.ApplyToResource(&resource)
			if err != nil {
				f.Log().Error("Failed to hash forma resource during final cleanup",
					"commandID", command.ID,
					"resourceLabel", resource.Label,
					"error", err)
				return false, fmt.Errorf("failed to hash forma resource %s during final cleanup: %w", resource.Label, err)
			}
			command.Forma.Resources[i] = *transformed
			hashedCount++
		}
	}

	// Hash opaque vals in ResourceUpdates array
	for i, resourceUpdate := range command.ResourceUpdates {
		if hasOpaqueValues(resourceUpdate.Resource.Properties) {
			transformed, err := t.ApplyToResource(&resourceUpdate.Resource)
			if err != nil {
				f.Log().Error("Failed to hash resource update during final cleanup",
					"commandID", command.ID,
					"resourceLabel", resourceUpdate.Resource.Label,
					"error", err)
				return false, fmt.Errorf("failed to hash resource update %s during final cleanup: %w", resourceUpdate.Resource.Label, err)
			}
			command.ResourceUpdates[i].Resource = *transformed
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
