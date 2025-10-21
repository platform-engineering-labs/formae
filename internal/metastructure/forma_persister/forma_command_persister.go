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
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

type FormaCommandPersister struct {
	act.Actor

	datastore datastore.Datastore
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

	return nil
}

type StoreNewFormaCommand struct {
	Command forma_command.FormaCommand
}

type LoadFormaCommand struct {
	CommandID string
}

type MarkResourcesAsRejected struct {
	CommandID          string
	ResourceUris       []pkgmodel.FormaeURI
	ResourceModifiedTs time.Time
}

type MarkResourcesAsFailed struct {
	CommandID          string
	ResourceUris       []pkgmodel.FormaeURI
	ResourceModifiedTs time.Time
}

// BulkUpdateResourceState is a common structure for rejecting and failing resources.
type BulkUpdateResourceState struct {
	CommandID          string
	ResourceState      types.ResourceUpdateState
	ResourceModifiedTs time.Time
}

type BulkUpdateResourceStateByKsuid struct {
	CommandID          string
	ResourceUris       []pkgmodel.FormaeURI
	ResourceState      types.ResourceUpdateState
	ResourceModifiedTs time.Time
}

type MarkFormaCommandAsComplete struct {
	CommandID string
}

func (f *FormaCommandPersister) HandleCall(from gen.PID, ref gen.Ref, message any) (any, error) {
	switch msg := message.(type) {
	case StoreNewFormaCommand:
		return f.storeNewFormaCommand(msg.Command)
	case LoadFormaCommand:
		return f.loadFormaCommand(msg.CommandID)
	case messages.UpdateResourceProgress:
		return f.updateCommandFromProgress(&msg)
	case MarkResourcesAsRejected:
		return f.markResourcesAsRejected(&msg)
	case MarkResourcesAsFailed:
		return f.markResourcesAsFailed(&msg)
	case messages.MarkResourceUpdateAsComplete:
		return f.markResourceUpdateAsComplete(&msg)
	case MarkFormaCommandAsComplete:
		return f.markFormaCommandAsComplete(&msg)
	default:
		return nil, fmt.Errorf("unhandled message type: %T", msg)
	}
}

func (f *FormaCommandPersister) storeNewFormaCommand(command forma_command.FormaCommand) (bool, error) {
	err := f.datastore.StoreFormaCommand(&command, command.ID)
	if err != nil {
		f.Log().Error("Failed to store new Forma command", "error", err)
		return false, fmt.Errorf("failed to store new Forma command: %w", err)
	}
	return true, nil
}

func (f *FormaCommandPersister) loadFormaCommand(commandID string) (*forma_command.FormaCommand, error) {
	command, err := f.datastore.GetFormaCommandByCommandID(commandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command", "commandID", commandID, "error", err)
		return nil, fmt.Errorf("failed to load Forma command: %w", err)
	}
	return command, nil
}

func (f *FormaCommandPersister) updateCommandFromProgress(progress *messages.UpdateResourceProgress) (bool, error) {
	f.Log().Debug("Updating Forma command from resource progress", "commandID", progress.CommandID, "resourceURI", progress.ResourceURI)
	command, err := f.datastore.GetFormaCommandByCommandID(progress.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for update", "commandID", progress.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for update: %w", err)
	}

	for i, res := range command.ResourceUpdates {
		if res.Resource.Ksuid == progress.ResourceURI.KSUID() && shouldUpdateResourceUpdate(res, progress.Progress) {
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

			command.ResourceUpdates[i] = res
			break
		}
	}

	// The first resource update reporting progress sets the start timestamp for the command
	if command.StartTs.IsZero() {
		command.StartTs = progress.ResourceStartTs
	}
	command.ModifiedTs = progress.ResourceModifiedTs
	command.State = overallCommandState(command)

	err = f.datastore.StoreFormaCommand(command, command.ID)
	if err != nil {
		f.Log().Error("Failed to update Forma command from resource progress", "commandID", progress.CommandID, "error", err)
		return false, fmt.Errorf("failed to update Forma command from resource progress: %w", err)
	}

	return true, nil
}

func (f *FormaCommandPersister) markResourcesAsRejected(msg *MarkResourcesAsRejected) (bool, error) {
	return f.bulkUpdateResourceState(&BulkUpdateResourceStateByKsuid{
		CommandID:          msg.CommandID,
		ResourceUris:       msg.ResourceUris,
		ResourceState:      types.ResourceUpdateStateRejected,
		ResourceModifiedTs: msg.ResourceModifiedTs,
	})
}

func (f *FormaCommandPersister) markResourcesAsFailed(msg *MarkResourcesAsFailed) (bool, error) {
	return f.bulkUpdateResourceState(&BulkUpdateResourceStateByKsuid{
		CommandID:          msg.CommandID,
		ResourceUris:       msg.ResourceUris,
		ResourceState:      types.ResourceUpdateStateFailed,
		ResourceModifiedTs: msg.ResourceModifiedTs,
	})
}

func (f *FormaCommandPersister) markResourceUpdateAsComplete(msg *messages.MarkResourceUpdateAsComplete) (bool, error) {
	f.Log().Debug("Marking command resource as complete", "commandID", msg.CommandID, "resourceURI", msg.ResourceURI)
	cmd, err := f.datastore.GetFormaCommandByCommandID(msg.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for complete update", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for complete update: %w", err)
	}

	for i, res := range cmd.ResourceUpdates {
		if res.Resource.Ksuid != msg.ResourceURI.KSUID() {
			continue
		}

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

		cmd.ResourceUpdates[i] = res
		break
	}

	cmd.ModifiedTs = msg.ResourceModifiedTs
	cmd.State = overallCommandState(cmd)

	err = f.datastore.StoreFormaCommand(cmd, cmd.ID)
	if err != nil {
		f.Log().Error("Failed to mark command resource as complete", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to mark command resource as complete: %w", err)
	}

	f.Log().Debug("Successfully marked command resource as complete", "commandID", msg.CommandID)
	return true, nil
}

func (f *FormaCommandPersister) bulkUpdateResourceState(bulkUpdate *BulkUpdateResourceStateByKsuid) (bool, error) {
	f.Log().Debug("Bulk updating resource states by KSUID", "commandID", bulkUpdate.CommandID, "resourceCount", len(bulkUpdate.ResourceUris), "state", bulkUpdate.ResourceState)

	command, err := f.datastore.GetFormaCommandByCommandID(bulkUpdate.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for bulk update", "commandID", bulkUpdate.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for bulk update: %w", err)
	}

	// Create a set of KSUIDs for fast lookup
	ksuidSet := make(map[string]struct{})
	for _, uri := range bulkUpdate.ResourceUris {
		ksuidSet[uri.KSUID()] = struct{}{}
	}

	// Match resources by their KSUID instead of URI
	for i, res := range command.ResourceUpdates {
		if _, exists := ksuidSet[res.Resource.Ksuid]; exists {
			res.State = bulkUpdate.ResourceState
			res.ModifiedTs = bulkUpdate.ResourceModifiedTs
			command.ResourceUpdates[i] = res
			f.Log().Debug("Marked resource as failed due to cascading failure", "resourceKsuid", res.Resource.Ksuid, "state", bulkUpdate.ResourceState)
		}
	}

	command.ModifiedTs = bulkUpdate.ResourceModifiedTs
	command.State = overallCommandState(command)

	err = f.datastore.StoreFormaCommand(command, command.ID)
	if err != nil {
		f.Log().Error("Failed to bulk update resource states", "commandID", bulkUpdate.CommandID, "error", err)
		return false, fmt.Errorf("failed to bulk update resource states: %w", err)
	}

	f.Log().Debug("Successfully bulk updated resource states by KSUID", "commandID", bulkUpdate.CommandID, "resourceCount", len(bulkUpdate.ResourceUris))
	return true, nil
}

// markFormaCommandAsComplete hashes all resources to ensure no sensitive data remains
func (f *FormaCommandPersister) markFormaCommandAsComplete(msg *MarkFormaCommandAsComplete) (bool, error) {
	f.Log().Debug("Hashing all forma resources for command completion", "commandID", msg.CommandID)

	command, err := f.datastore.GetFormaCommandByCommandID(msg.CommandID)
	if err != nil {
		f.Log().Error("Failed to load Forma command for final hashing", "commandID", msg.CommandID, "error", err)
		return false, fmt.Errorf("failed to load Forma command for final hashing: %w", err)
	}

	t := transformations.NewPersistValueTransformer()
	hashedCount := 0

	// Hash opaque vals in Forma.Resources array
	for i, resource := range command.Forma.Resources {
		if hasOpaqueValues(resource.Properties) {
			transformed, err := t.ApplyToResource(&resource)
			if err != nil {
				f.Log().Error("Failed to hash forma resource during final cleanup",
					"commandID", msg.CommandID,
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
					"commandID", msg.CommandID,
					"resourceLabel", resourceUpdate.Resource.Label,
					"error", err)
				return false, fmt.Errorf("failed to hash resource update %s during final cleanup: %w", resourceUpdate.Resource.Label, err)
			}
			command.ResourceUpdates[i].Resource = *transformed
			hashedCount++
		}
	}

	if hashedCount > 0 {
		err = f.datastore.StoreFormaCommand(command, command.ID)
		if err != nil {
			f.Log().Error("Failed to store forma command after final hashing", "commandID", msg.CommandID, "error", err)
			return false, fmt.Errorf("failed to store forma command after final hashing: %w", err)
		}

		f.Log().Debug("Successfully hashed all forma resources for command completion",
			"commandID", msg.CommandID,
			"hashedResourceCount", hashedCount)
	}

	return true, nil
}

func overallCommandState(command *forma_command.FormaCommand) forma_command.CommandState {
	var states []types.ResourceUpdateState
	for _, res := range command.ResourceUpdates {
		states = append(states, res.State)
	}
	if slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s != types.ResourceUpdateStateSuccess && s != types.ResourceUpdateStateFailed && s != types.ResourceUpdateStateRejected
	}) {
		return forma_command.CommandStateInProgress
	} else if slices.ContainsFunc(states, func(s types.ResourceUpdateState) bool {
		return s == types.ResourceUpdateStateFailed || s == types.ResourceUpdateStateRejected
	}) {
		return forma_command.CommandStateFailed
	}

	return forma_command.CommandStateSuccess
}

func hasOpaqueValues(props json.RawMessage) bool {
	return bytes.Contains(props, []byte(`"$visibility"`)) &&
		bytes.Contains(props, []byte(`"Opaque"`))
}

// shouldUpdateResourceUpdate determines if a ResourceUpdate should be updated based on the progress
func shouldUpdateResourceUpdate(resourceUpdate resource_update.ResourceUpdate, progress resource.ProgressResult) bool {
	switch progress.Operation {
	case resource.OperationCreate:
		return resourceUpdate.Operation == resource_update.OperationCreate || resourceUpdate.Operation == resource_update.OperationReplace
	case resource.OperationDelete:
		return resourceUpdate.Operation == resource_update.OperationDelete || resourceUpdate.Operation == resource_update.OperationReplace
	case resource.OperationUpdate:
		return resourceUpdate.Operation == resource_update.OperationUpdate || resourceUpdate.Operation == resource_update.OperationReplace
	case resource.OperationRead:
		// Read operations can apply to Delete, Update, Replace, or Read ResourceUpdates
		return resourceUpdate.Operation == resource_update.OperationDelete ||
			resourceUpdate.Operation == resource_update.OperationUpdate ||
			resourceUpdate.Operation == resource_update.OperationRead ||
			resourceUpdate.Operation == resource_update.OperationReplace
	default:
		// Default to allowing the update if we can't determine
		return true
	}
}
