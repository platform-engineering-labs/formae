// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_persister

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourcePersister is an actor responsible for persisting resource updates to the datastore.
// It handles storing resources, targets, and stacks based on successful resource operations.
type ResourcePersister struct {
	act.Actor

	datastore               datastore.Datastore
	persistValueTransformer transformations.ResourceTransformer
}

func NewResourcePersister() gen.ProcessBehavior {
	return &ResourcePersister{
		persistValueTransformer: transformations.NewPersistValueTransformer(),
	}
}

func (rp *ResourcePersister) Init(args ...any) error {
	ds, ok := rp.Env("Datastore")
	if !ok {
		rp.Log().Error("ResourcePersister: missing 'Datastore' environment variable")
		return fmt.Errorf("resource persister: missing 'Datastore' environment variable")
	}
	rp.datastore = ds.(datastore.Datastore)

	rp.persistValueTransformer = transformations.NewPersistValueTransformer()

	return nil
}

func (rp *ResourcePersister) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case resource_update.PersistResourceUpdate:
		hash, err := rp.storeResourceUpdate(req.CommandID, req.ResourceOperation, req.PluginOperation, &req.ResourceUpdate)
		return hash, err
	case messages.LoadResource:
		return rp.loadResource(req.ResourceURI)
	default:
		rp.Log().Error("ResourcePersister: unknown request type", "type", fmt.Sprintf("%T", request))
		return nil, fmt.Errorf("resource persister: unknown request type %T", request)
	}
}

func (rp *ResourcePersister) storeResourceUpdate(commandID string, resourceOperation resource_update.OperationType, pluginOperation pkgresource.Operation, resourceUpdate *resource_update.ResourceUpdate) (string, error) {
	ok, relevantProgress := resourceUpdate.FindProgress(pluginOperation)
	if !ok {
		panic(fmt.Sprintf("Progress for operation %s not found in resource update %s", pluginOperation, resourceUpdate.Resource.Label))
	}

	// This can cause a validation error when the delete op is in fact valid.
	// Subsequently no delete is persisted and the system doesn't work as expected.
	// To avoid this, we skip the validation when the operation is a delete.
	if resourceOperation != resource_update.OperationDelete {
		if err := validateRequiredFields(resourceUpdate.Resource); err != nil {
			slog.Error("Validation of required fields failed", "error", err)
			return "", nil
		}
	}

	resourceUpdate.Operation = delegateCommandFromOperation(resourceOperation, pluginOperation, relevantProgress)

	forma := &forma_command.FormaCommand{
		ID:      commandID,
		Forma:   rp.formaFromResourceUpate(pluginOperation, resourceUpdate),
		Command: formaCommandFromOperation(pluginOperation),
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				Resource:                 resourceUpdate.Resource,
				ResourceTarget:           resourceUpdate.ResourceTarget,
				Operation:                delegateCommandFromOperation(resourceOperation, pluginOperation, relevantProgress),
				State:                    resource_update.ResourceUpdateStateSuccess,
				StartTs:                  relevantProgress.StartTs,
				ModifiedTs:               relevantProgress.ModifiedTs,
				MostRecentProgressResult: *relevantProgress,
				GroupID:                  resourceUpdate.GroupID,
			},
		},
	}
	if pluginOperation == pkgresource.OperationRead && resourceOperation == resource_update.OperationUpdate {
		// We need to overwrite
		forma.ResourceUpdates[0].Resource = resourceUpdate.ExistingResource
	}

	err := rp.storeStacks(forma)
	if err != nil {
		slog.Error("Failed to store stacks for resource update",
			"error", err,
			"resource", resourceUpdate.Resource,
			"operation", pluginOperation)
		return "", fmt.Errorf("failed to store stacks for resource update %v: %w", resourceUpdate.Resource, err)
	}
	hash := forma.ResourceUpdates[0].Version

	return hash, nil
}

func validateRequiredFields(resource pkgmodel.Resource) error {
	var missingFields []string
	for field, hint := range resource.Schema.Hints {
		if hint.Required {
			if strings.Contains(field, ".") {
				parts := strings.Split(field, ".")
				shouldSkip := false
				for i := 0; i < len(parts)-1; i++ {
					parentField := strings.Join(parts[:i+1], ".")
					parentValue, parentFound := resource.GetProperty(parentField)
					if !parentFound || parentValue == "" {
						shouldSkip = true
						break

					}
				}

				if shouldSkip {
					continue
				}
			}
			value, found := resource.GetProperty(field)
			if !found || value == "" {
				missingFields = append(missingFields, field)
			}
		}
	}
	if len(missingFields) > 0 {
		return fmt.Errorf("resource %s of type %s is missing required fields: %v", resource.Label, resource.Type, missingFields)
	}

	return nil
}

func (rp *ResourcePersister) storeStacks(formaCommand *forma_command.FormaCommand) error {
	for _, stack := range formaCommand.Forma.SplitByStack() {
		for i := range formaCommand.ResourceUpdates {
			rc := &formaCommand.ResourceUpdates[i]
			if rc.Resource.Stack == stack.SingleStackLabel() && rc.State == resource_update.ResourceUpdateStateSuccess && rc.Version == "" {
				err := rp.storeTarget(formaCommand)
				if err != nil {
					slog.Error("Failed to store target",
						"error", err,
						"stackLabel", stack.SingleStackLabel(),
						"resourceLabel", rc.Resource.Label)
					return fmt.Errorf("failed to store target %s: %w", rc.Resource.Label, err)
				}
				slog.Debug("Resource command successful, persisting...",
					"stackLabel", rc.Resource.Stack,
					"resourceLabel", rc.Resource.Label,
					"command", rc.Operation)

				if len(stack.Stacks) == 0 {
					slog.Error("Stack has no Stacks slice elements",
						"stackLabel", stack.SingleStackLabel(),
						"resourceLabel", rc.Resource.Label)
					return fmt.Errorf("stack %s has no Stacks elements", stack.SingleStackLabel())
				}

				hash, err := rp.processResourceUpdate(formaCommand.ID, stack.Stacks[0], *rc)
				if err != nil {
					slog.Error("Failed to persist resource command",
						"error", err,
						"resourceLabel", rc.Resource.Label,
						"stackLabel", rc.Resource.Stack)
					return fmt.Errorf("failed to persist resource %s in stack %s: %w", rc.Resource.Label, rc.Resource.Stack, err)
				}
				if hash != "" {
					rc.Version = hash
					slog.Debug("Resource command persisted",
						"resourceLabel", rc.Resource.Label,
						"stackLabel", rc.Resource.Stack,
						"hash", hash)
				} else {
					slog.Debug("No persist needed for resource command",
						"resourceLabel", rc.Resource.Label,
						"stackLabel", rc.Resource.Stack,
						"command", rc.Operation)
				}
			}
		}
	}
	return nil
}

func (rp *ResourcePersister) loadResource(resourceURI pkgmodel.FormaeURI) (messages.LoadResourceResult, error) {
	res, err := rp.datastore.LoadResourceById(resourceURI.KSUID())
	if err != nil {
		return messages.LoadResourceResult{}, fmt.Errorf("failed to load resource %s: %w", resourceURI, err)
	}

	if res == nil {
		return messages.LoadResourceResult{}, fmt.Errorf("resource %s not found", resourceURI.KSUID())
	}

	currentTarget, err := rp.datastore.LoadTarget(res.Target)
	if err != nil || currentTarget == nil {
		slog.Error("Error loading target",
			"targetLabel", res.Target,
			"error", err)
		return messages.LoadResourceResult{}, fmt.Errorf("failed to load target %s: %w", res.Target, err)
	}

	return messages.LoadResourceResult{
		Resource: *res,
		Target:   *currentTarget,
	}, nil
}

func (rp *ResourcePersister) formaFromResourceUpate(operation pkgresource.Operation, resourceUpdate *resource_update.ResourceUpdate) pkgmodel.Forma {
	return pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{
				Label: resourceUpdate.StackLabel,
			},
		},
		Resources: []pkgmodel.Resource{resourceUpdate.Resource},
		Targets:   []pkgmodel.Target{resourceUpdate.ResourceTarget},
	}
}

// Moving forward, every read operation that has a diff will be persisted to the datastore. To not change the existing code,
// we force this behavior by making every read operation a sync command.
func formaCommandFromOperation(operation pkgresource.Operation) pkgmodel.Command {
	switch operation {
	case pkgresource.OperationRead:
		return pkgmodel.CommandSync
	default:
		return pkgmodel.CommandApply
	}
}

func delegateCommandFromOperation(resourceOperation resource_update.OperationType, pluginOperation pkgresource.Operation, progress *pkgresource.ProgressResult) resource_update.OperationType {
	switch pluginOperation {
	case pkgresource.OperationCreate:
		return resource_update.OperationCreate
	case pkgresource.OperationUpdate:
		return resource_update.OperationUpdate
	case pkgresource.OperationDelete:
		return resource_update.OperationDelete
	case pkgresource.OperationRead:
		if progress.ErrorCode == pkgresource.OperationErrorCodeNotFound {
			return resource_update.OperationDelete
		}
		return resource_update.OperationRead
	default:
		panic(fmt.Sprintf("Unknown resource operation: %s", pluginOperation))
	}
}

func (rp *ResourcePersister) processResourceUpdate(commandID string, stack pkgmodel.Stack, rc resource_update.ResourceUpdate) (string, error) {
	stackLabel := rc.Resource.Stack

	var resourceVersion string
	var storeResourceErr error

	// Create secret-safe version of the resource for storage
	secretSafeResource, err := rp.persistValueTransformer.ApplyToResource(&rc.Resource)
	if err != nil {
		slog.Error("Failed to transform resource to secret-safe format",
			"stackLabel", stackLabel,
			"resourceLabel", rc.Resource.Label,
			"error", err)
		return "", fmt.Errorf("failed to transform resource %s for secret-safe storage: %w", rc.Resource.Label, err)
	}

	switch rc.Operation {
	case resource_update.OperationCreate:
		secretSafeResource.PatchDocument = nil
		resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)
	case resource_update.OperationUpdate:
		secretSafeResource.PatchDocument = nil
		resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)
	case resource_update.OperationDelete:
		r, err := rp.datastore.LoadResource(rc.Resource.URI())
		if err == nil && r != nil {
			slog.Debug("Resource exists, deleting",
				"stackLabel", stackLabel,
				"resourceLabel", rc.Resource.Label)
		} else {
			slog.Debug("Resource does not exist, no removal needed",
				"stackLabel", stackLabel,
				"resourceLabel", rc.Resource.Label)
			return "", nil
		}

		resourceVersion, storeResourceErr = rp.datastore.DeleteResource(&rc.Resource, commandID)
	case resource_update.OperationRead:
		if rc.Operation == resource_update.OperationRead {
			currentResource, err := rp.datastore.LoadResource(rc.Resource.URI())
			if err != nil {
				slog.Error("Failed to load current resource for comparison",
					"resourceLabel", rc.Resource.Label,
					"error", err)
				return "", fmt.Errorf("failed to load current resource %s for comparison: %w", rc.Resource.Label, err)
			}

			if currentResource == nil {
				slog.Debug("Resource not found, creating new one",
					"resourceLabel", rc.Resource.Label,
					"stackLabel", stackLabel)

				resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)

				return resourceVersion, storeResourceErr
			}

			// Only persist if Properties or ReadOnlyProperties have changed
			if !util.JsonEqualRaw(currentResource.Properties, rc.Resource.Properties) ||
				!util.JsonEqualRaw(currentResource.ReadOnlyProperties, rc.Resource.ReadOnlyProperties) {

				currentResource.Properties = secretSafeResource.Properties
				currentResource.ReadOnlyProperties = secretSafeResource.ReadOnlyProperties

				slog.Debug("Resource properties changed, persisting update",
					"resourceLabel", rc.Resource.Label,
					"stackLabel", stackLabel)

				resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)
				if storeResourceErr != nil {
					return "", fmt.Errorf("failed to persist updated resource %s: %w", rc.Resource.Label, storeResourceErr)
				}

				return resourceVersion, storeResourceErr
			} else {
				slog.Debug("No changes in Properties or ReadOnlyProperties, skipping persist",
					"resourceLabel", rc.Resource.Label,
					"stackLabel", stackLabel)
				return "", nil
			}
		} else {
			slog.Debug("Read command, no persist required", "stackLabel", stackLabel, "resourceLabel", rc.Resource.Label)
			return "", nil
		}

	default:
		slog.Error("Unknown resource command type", "command", rc.Operation, "stackLabel", stackLabel, "resourceLabel", rc.Resource.Label)
		return "", fmt.Errorf("unknown resource command type '%s' for resource %s", rc.Operation, rc.Resource.Label)
	}

	if storeResourceErr != nil {
		slog.Error("Failed to persist/remove stack for resource command",
			"error", storeResourceErr,
			"stackLabel", stackLabel,
			"resourceLabel", rc.Resource.Label,
			"command", rc.Operation,
		)
		return "", fmt.Errorf("persist operation failed for resource %s: %w", rc.Resource.Label, storeResourceErr)
	}

	slog.Debug("Successfully persisted resource operation",
		"stackLabel", stackLabel,
		"resourceLabel", rc.Resource.Label,
		"command", rc.Operation,
		"hash", resourceVersion,
	)

	return resourceVersion, nil
}

func (rp *ResourcePersister) storeTarget(formaCommand *forma_command.FormaCommand) error {
	// First check if there are any successful resource commands for each target
	targetHasSuccessfulCommands := make(map[string]bool)

	for _, rc := range formaCommand.ResourceUpdates {

		t := rc.Resource.Target
		if rc.State == resource_update.ResourceUpdateStateSuccess && rc.Resource.Target != "" {
			targetHasSuccessfulCommands[t] = true
		}
	}

	for _, target := range formaCommand.Forma.Targets {
		// Skip targets without successful resource commands
		if !targetHasSuccessfulCommands[target.Label] {
			slog.Debug("Skipping target without successful resource commands",
				"targetLabel", target.Label)
			continue
		}

		slog.Debug("Processing target with successful commands",
			"targetLabel", target.Label,
			"namespace", target.Namespace)

		currentTarget, err := rp.datastore.LoadTarget(target.Label)
		if err != nil {
			slog.Error("Error loading target",
				"targetLabel", target.Label,
				"error", err)
			return fmt.Errorf("failed to load target %s: %w", target.Label, err)
		}

		// If target doesn't exist, persist it
		if currentTarget == nil {
			slog.Debug("Target not found, creating new one",
				"targetLabel", target.Label)

			targetVersion, err := rp.datastore.StoreTarget(&target)
			if err != nil {
				slog.Error("Failed to persist new target",
					"targetLabel", target.Label,
					"error", err)
				return fmt.Errorf("failed to persist new target %s: %w", target.Label, err)
			}

			slog.Debug("Successfully persisted new target",
				"targetLabel", target.Label,
				"hash", targetVersion)
			continue
		}

		// Compare existing target with new one
		currentConfig, _ := json.Marshal(currentTarget.Config)
		newConfig, _ := json.Marshal(target.Config)

		if currentTarget.Label != target.Label ||
			currentTarget.Namespace != target.Namespace ||
			string(currentConfig) != string(newConfig) {

			slog.Debug("Target has changed, updating",
				"targetLabel", target.Label)

			targetVersion, err := rp.datastore.StoreTarget(&target)
			if err != nil {
				slog.Error("Failed to persist target update",
					"targetLabel", target.Label,
					"error", err)
				return fmt.Errorf("failed to persist target update %s: %w", target.Label, err)
			}

			slog.Debug("Successfully persisted target update",
				"targetLabel", target.Label,
				"hash", targetVersion)
		} else {
			slog.Debug("Target unchanged, skipping persist",
				"targetLabel", target.Label)
		}
	}

	return nil
}
