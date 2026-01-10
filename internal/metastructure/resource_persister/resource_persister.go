// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_persister

import (
	"fmt"
	"log/slog"
	"strings"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/target_update"
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
	discoveryEnabled        bool
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

	// Read discovery config to check if discovery is enabled
	dcfg, ok := rp.Env("DiscoveryConfig")
	if !ok {
		rp.Log().Error("ResourcePersister: missing 'DiscoveryConfig' environment variable")
		return fmt.Errorf("resource persister: missing 'DiscoveryConfig' environment variable")
	}
	discoveryConfig := dcfg.(pkgmodel.DiscoveryConfig)
	rp.discoveryEnabled = discoveryConfig.Enabled

	rp.persistValueTransformer = transformations.NewPersistValueTransformer()

	return nil
}

func (rp *ResourcePersister) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case resource_update.PersistResourceUpdate:
		hash, err := rp.storeResourceUpdate(req.CommandID, req.ResourceOperation, req.PluginOperation, &req.ResourceUpdate)
		return hash, err
	case target_update.PersistTargetUpdates:
		versions, err := rp.persistTargetUpdates(req.TargetUpdates, req.CommandID)
		if err != nil {
			return nil, err
		}
		return versions, nil
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
		panic(fmt.Sprintf("Progress for operation %s not found in resource update %s", pluginOperation, resourceUpdate.DesiredState.Label))
	}

	resourceUpdate.Operation = resourceOperationFromPluginOperation(resourceOperation, pluginOperation, relevantProgress)

	// This can cause a validation error when the delete op is in fact valid.
	// Subsequently no delete is persisted and the system doesn't work as expected.
	// To avoid this, we skip the validation when the operation is a delete.
	if resourceUpdate.Operation != resource_update.OperationDelete {
		if err := validateRequiredFields(resourceUpdate.DesiredState); err != nil {
			slog.Debug("Validation of required fields failed", "error", err)
			return "", nil
		}
	}

	forma := &forma_command.FormaCommand{
		ID:      commandID,
		Command: formaCommandFromOperation(pluginOperation),
		ResourceUpdates: []resource_update.ResourceUpdate{
			{
				DesiredState:             resourceUpdate.DesiredState,
				ResourceTarget:           resourceUpdate.ResourceTarget,
				Operation:                resourceOperationFromPluginOperation(resourceOperation, pluginOperation, relevantProgress),
				State:                    resource_update.ResourceUpdateStateSuccess,
				StartTs:                  relevantProgress.StartTs,
				ModifiedTs:               relevantProgress.ModifiedTs,
				MostRecentProgressResult: *relevantProgress,
				GroupID:                  resourceUpdate.GroupID,
				StackLabel:               resourceUpdate.StackLabel,
			},
		},
	}
	if pluginOperation == pkgresource.OperationRead && resourceOperation == resource_update.OperationUpdate {
		// We need to overwrite
		forma.ResourceUpdates[0].DesiredState = resourceUpdate.PriorState
	}

	err := rp.storeStacks(forma)
	if err != nil {
		slog.Error("Failed to store stacks for resource update",
			"error", err,
			"resource", resourceUpdate.DesiredState,
			"operation", pluginOperation)
		return "", fmt.Errorf("failed to store stacks for resource update %v: %w", resourceUpdate.DesiredState, err)
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
	// Iterate ResourceUpdates directly - no need to use Forma.Resources/SplitByStack()
	// since all information needed is in the ResourceUpdate itself
	for i := range formaCommand.ResourceUpdates {
		rc := &formaCommand.ResourceUpdates[i]
		// Only process if the resource's stack matches the expected stack label.
		// This is important for Update operations where the ExistingResource (with old stack)
		// may be substituted - we should skip persisting if the stacks don't match,
		// as this indicates a stack change is in progress and we shouldn't persist the
		// old stack's resource state.
		if rc.DesiredState.Stack != rc.StackLabel {
			slog.Debug("Skipping resource persist - stack mismatch (stack change in progress)",
				"resourceStack", rc.DesiredState.Stack,
				"stackLabel", rc.StackLabel,
				"resourceLabel", rc.DesiredState.Label)
			continue
		}
		if rc.State == resource_update.ResourceUpdateStateSuccess && rc.Version == "" {
			slog.Debug("Resource command successful, persisting...",
				"stackLabel", rc.DesiredState.Stack,
				"resourceLabel", rc.DesiredState.Label,
				"command", rc.Operation)

			hash, err := rp.processResourceUpdate(formaCommand.ID, *rc)
			if err != nil {
				slog.Error("Failed to persist resource command",
					"error", err,
					"resourceLabel", rc.DesiredState.Label,
					"stackLabel", rc.DesiredState.Stack)
				return fmt.Errorf("failed to persist resource %s in stack %s: %w", rc.DesiredState.Label, rc.DesiredState.Stack, err)
			}
			if hash != "" {
				rc.Version = hash
				slog.Debug("Resource command persisted",
					"resourceLabel", rc.DesiredState.Label,
					"stackLabel", rc.DesiredState.Stack,
					"hash", hash)
			} else {
				slog.Debug("No persist needed for resource command",
					"resourceLabel", rc.DesiredState.Label,
					"stackLabel", rc.DesiredState.Stack,
					"command", rc.Operation)
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

func resourceOperationFromPluginOperation(resourceOperation resource_update.OperationType, pluginOperation pkgresource.Operation, progress *pkgresource.ProgressResult) resource_update.OperationType {
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

func (rp *ResourcePersister) processResourceUpdate(commandID string, rc resource_update.ResourceUpdate) (string, error) {
	stackLabel := rc.DesiredState.Stack

	var resourceVersion string
	var storeResourceErr error

	// Create secret-safe version of the resource for storage
	secretSafeResource, err := rp.persistValueTransformer.ApplyToResource(&rc.DesiredState)
	if err != nil {
		slog.Error("Failed to transform resource to secret-safe format",
			"stackLabel", stackLabel,
			"resourceLabel", rc.DesiredState.Label,
			"error", err)
		return "", fmt.Errorf("failed to transform resource %s for secret-safe storage: %w", rc.DesiredState.Label, err)
	}

	switch rc.Operation {
	case resource_update.OperationCreate:
		secretSafeResource.PatchDocument = nil
		resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)
	case resource_update.OperationUpdate:
		secretSafeResource.PatchDocument = nil
		resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)
	case resource_update.OperationDelete:
		r, err := rp.datastore.LoadResource(rc.DesiredState.URI())
		if err == nil && r != nil {
			slog.Debug("Resource exists, deleting",
				"stackLabel", stackLabel,
				"resourceLabel", rc.DesiredState.Label)
		} else {
			slog.Debug("Resource does not exist, no removal needed",
				"stackLabel", stackLabel,
				"resourceLabel", rc.DesiredState.Label)
			return "", nil
		}

		resourceVersion, storeResourceErr = rp.datastore.DeleteResource(&rc.DesiredState, commandID)
	case resource_update.OperationRead:
		if rc.Operation == resource_update.OperationRead {
			currentResource, err := rp.datastore.LoadResource(rc.DesiredState.URI())
			if err != nil {
				slog.Error("Failed to load current resource for comparison",
					"resourceLabel", rc.DesiredState.Label,
					"error", err)
				return "", fmt.Errorf("failed to load current resource %s for comparison: %w", rc.DesiredState.Label, err)
			}

			if currentResource == nil {
				slog.Debug("Resource not found, creating new one",
					"resourceLabel", rc.DesiredState.Label,
					"stackLabel", stackLabel)

				resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)

				return resourceVersion, storeResourceErr
			}

			// Only persist if Properties or ReadOnlyProperties have changed
			if !util.JsonEqualRaw(currentResource.Properties, rc.DesiredState.Properties) ||
				!util.JsonEqualRaw(currentResource.ReadOnlyProperties, rc.DesiredState.ReadOnlyProperties) {

				// Preserve the current stack and managed state during sync READ operations
				// to prevent stale sync data from overwriting recent stack changes
				secretSafeResource.Stack = currentResource.Stack
				secretSafeResource.Managed = currentResource.Managed
				secretSafeResource.Schema.Discoverable = currentResource.Schema.Discoverable
				secretSafeResource.Schema.Extractable = currentResource.Schema.Extractable

				currentResource.Properties = secretSafeResource.Properties
				currentResource.ReadOnlyProperties = secretSafeResource.ReadOnlyProperties

				slog.Debug("Resource properties changed, persisting update",
					"resourceLabel", rc.DesiredState.Label,
					"stackLabel", stackLabel)

				resourceVersion, storeResourceErr = rp.datastore.StoreResource(secretSafeResource, commandID)
				if storeResourceErr != nil {
					return "", fmt.Errorf("failed to persist updated resource %s: %w", rc.DesiredState.Label, storeResourceErr)
				}

				return resourceVersion, storeResourceErr
			} else {
				slog.Debug("No changes in Properties or ReadOnlyProperties, skipping persist",
					"resourceLabel", rc.DesiredState.Label,
					"stackLabel", stackLabel)
				return "", nil
			}
		} else {
			slog.Debug("Read command, no persist required", "stackLabel", stackLabel, "resourceLabel", rc.DesiredState.Label)
			return "", nil
		}

	default:
		slog.Error("Unknown resource command type", "command", rc.Operation, "stackLabel", stackLabel, "resourceLabel", rc.DesiredState.Label)
		return "", fmt.Errorf("unknown resource command type '%s' for resource %s", rc.Operation, rc.DesiredState.Label)
	}

	if storeResourceErr != nil {
		slog.Error("Failed to persist/remove stack for resource command",
			"error", storeResourceErr,
			"stackLabel", stackLabel,
			"resourceLabel", rc.DesiredState.Label,
			"command", rc.Operation,
		)
		return "", fmt.Errorf("persist operation failed for resource %s: %w", rc.DesiredState.Label, storeResourceErr)
	}

	slog.Debug("Successfully persisted resource operation",
		"stackLabel", stackLabel,
		"resourceLabel", rc.DesiredState.Label,
		"command", rc.Operation,
		"hash", resourceVersion,
	)

	return resourceVersion, nil
}

func (rp *ResourcePersister) persistTargetUpdates(updates []target_update.TargetUpdate, commandID string) ([]string, error) {
	rp.Log().Debug("Starting to persist target updates", "count", len(updates), "commandID", commandID)

	versions := make([]string, 0, len(updates))
	for i := range updates {
		rp.Log().Debug("Persisting target update", "index", i, "label", updates[i].Target.Label)
		if err := rp.persistTargetUpdate(&updates[i]); err != nil {
			rp.Log().Error("Failed to persist target update", "index", i, "label", updates[i].Target.Label, "error", err)
			return nil, fmt.Errorf("failed to persist target update for %s: %w", updates[i].Target.Label, err)
		}
		rp.Log().Debug("Successfully persisted target update", "index", i, "label", updates[i].Target.Label)
		versions = append(versions, updates[i].Version)
	}

	rp.Log().Debug("Finished persisting all target updates", "commandID", commandID)
	return versions, nil
}

func (rp *ResourcePersister) persistTargetUpdate(update *target_update.TargetUpdate) error {
	var version string
	var err error

	switch update.Operation {
	case target_update.TargetOperationCreate:
		version, err = rp.datastore.CreateTarget(&update.Target)
	case target_update.TargetOperationUpdate:
		version, err = rp.datastore.UpdateTarget(&update.Target)
	default:
		err = fmt.Errorf("unknown target operation: %s", update.Operation)
	}

	if err != nil {
		update.State = target_update.TargetUpdateStateFailed
		update.ErrorMessage = err.Error()
		update.ModifiedTs = util.TimeNow()
		slog.Error("Failed to persist target",
			"label", update.Target.Label,
			"operation", update.Operation,
			"error", err)
		return err
	}

	update.Version = version
	update.State = target_update.TargetUpdateStateSuccess
	update.ModifiedTs = util.TimeNow()
	slog.Debug("Successfully persisted target",
		"label", update.Target.Label,
		"operation", update.Operation,
		"version", version)

	// Trigger discovery for freshly added discoverable targets (if discovery is enabled)
	if rp.discoveryEnabled && target_update.ShouldTriggerDiscovery(update) {
		discoveryPID := gen.ProcessID{
			Name: actornames.Discovery,
			Node: rp.Node().Name(),
		}
		if err := rp.Send(discoveryPID, discovery.Discover{Once: true}); err != nil {
			rp.Log().Error("Failed to trigger discovery for newly discoverable target",
				"label", update.Target.Label,
				"error", err)
		} else {
			rp.Log().Debug("Triggered discovery for newly discoverable target",
				"label", update.Target.Label)
		}
	}

	return nil
}
