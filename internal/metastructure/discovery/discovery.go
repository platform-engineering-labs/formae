// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/plugin_operation"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Discovery is the component responsible for automated resource discovery. On a configured interval, discovery will
// scan all non block-listed targets for resources that are marked as discoverable by the resource plugins.
//
// The state transitions are as follows:
//
//				+-----------------------+
//			 -->|         Idle          |---
//			|   +-----------------------+   |
//			|                               |
//			|                               |
//			|                               |
//			|                               |
//			|                               |                 -
//			|                               |
//			|   +-----------------------+   |
//			 ---|      Discovering      |<---
//				+-----------------------+

type Discovery struct {
	statemachine.StateMachine[DiscoveryData]
}

type ResourceDescriptorTreeNode struct {
	ResourceType                         string
	Discoverable                         bool
	NestedResourcesWithMappingProperties map[*ResourceDescriptorTreeNode][]plugin.ListParameter
}

func (n ResourceDescriptorTreeNode) HasNestedResources() bool {
	return len(n.NestedResourcesWithMappingProperties) > 0
}

func NewDiscovery() gen.ProcessBehavior {
	return &Discovery{}
}

// Messages processed by Discovery

type Discover struct {
	Once bool
}

// DiscoverSync is a synchronous version of Discover that returns immediately
// indicating whether discovery started or was already running.
type DiscoverSync struct {
	Once bool
}

// DiscoverSyncResult is the response from DiscoverSync
type DiscoverSyncResult struct {
	Started        bool
	AlreadyRunning bool
}

type ResumeScanning struct{}

const (
	StateIdle        = gen.Atom("Idle")
	StateDiscovering = gen.Atom("Discovering")
)

type DiscoveryData struct {
	pluginManager                 *plugin.Manager
	ds                            datastore.Datastore
	serverCfg                     *pkgmodel.ServerConfig
	discoveryCfg                  *pkgmodel.DiscoveryConfig
	isScheduledDiscovery          bool
	targets                       map[string]pkgmodel.Target
	resourceDescriptorRoots       map[string]ResourceDescriptorTreeNode
	resourceDescriptors           map[string]plugin.ResourceDescriptor
	queuedListOperations          map[string][]ListOperation
	outstandingListOperations     map[ListOperation]struct{}
	outstandingSyncCommands       map[string]ListOperation
	recentlyDiscoveredResourceIDs map[string]struct{}
	timeStarted                   time.Time
	summary                       map[string]int
}

func (d *DiscoveryData) SetTargets(targets []*pkgmodel.Target) {
	d.targets = make(map[string]pkgmodel.Target, len(targets))
	for _, target := range targets {
		d.targets[target.Label] = *target
	}
}

func (d DiscoveryData) HasOutstandingWork() bool {
	return len(d.queuedListOperations) > 0 || len(d.outstandingListOperations) > 0 || len(d.outstandingSyncCommands) > 0
}

type ListOperation struct {
	ResourceType string
	TargetLabel  string
	ListParams   string
}

func (d *Discovery) Init(args ...any) (statemachine.StateMachineSpec[DiscoveryData], error) {
	pluginManager, ok := d.Env("PluginManager")
	if !ok {
		d.Log().Error("Discovery: missing 'PluginManager' environment variable")
		return statemachine.StateMachineSpec[DiscoveryData]{}, fmt.Errorf("discovery: missing 'PluginManager' environment variable")
	}

	dsEnv, ok := d.Env("Datastore")
	if !ok {
		d.Log().Error("Discovery: missing 'Datastore' environment variable")
		return statemachine.StateMachineSpec[DiscoveryData]{}, fmt.Errorf("discovery: missing 'Datastore' environment variable")
	}

	scfg, ok := d.Env("ServerConfig")
	if !ok {
		d.Log().Error("Discovery: missing 'ServerConfig' environment variable")
		return statemachine.StateMachineSpec[DiscoveryData]{}, fmt.Errorf("discovery: missing 'ServerConfig' environment variable")
	}
	serverCfg := scfg.(pkgmodel.ServerConfig)

	dcfg, ok := d.Env("DiscoveryConfig")
	if !ok {
		d.Log().Error("Discovery: missing 'DiscoveryConfig' environment variable")
		return statemachine.StateMachineSpec[DiscoveryData]{}, fmt.Errorf("discovery: missing 'DiscoveryConfig' environment variable")
	}
	discoveryCfg := dcfg.(pkgmodel.DiscoveryConfig)

	// Load discoverable targets from datastore
	ds := dsEnv.(datastore.Datastore)

	// ONE-RELEASE GRACE PERIOD: Auto-migrate config scan_targets to database
	// This provides backward compatibility for users still using scan_targets in config
	if err := migrateScanTargetsToDatabase(ds, &discoveryCfg, d); err != nil {
		d.Log().Error("Failed to migrate scan_targets to database", "error", err)
		// Continue startup even if migration fails - don't block the system
	}

	data := DiscoveryData{
		pluginManager:                 pluginManager.(*plugin.Manager),
		ds:                            ds,
		discoveryCfg:                  &discoveryCfg,
		serverCfg:                     &serverCfg,
		targets:                       make(map[string]pkgmodel.Target), // Will be populated on each discovery run
		resourceDescriptorRoots:       make(map[string]ResourceDescriptorTreeNode),
		resourceDescriptors:           make(map[string]plugin.ResourceDescriptor),
		queuedListOperations:          make(map[string][]ListOperation),
		outstandingListOperations:     make(map[ListOperation]struct{}),
		outstandingSyncCommands:       make(map[string]ListOperation),
		recentlyDiscoveredResourceIDs: make(map[string]struct{}),
		summary:                       make(map[string]int),
	}

	spec := statemachine.NewStateMachineSpec(StateIdle,
		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateMessageHandler(StateIdle, discover),
		statemachine.WithStateMessageHandler(StateDiscovering, discover),
		statemachine.WithStateMessageHandler(StateDiscovering, resumeScanning),
		statemachine.WithStateMessageHandler(StateDiscovering, processListing),
		statemachine.WithStateMessageHandler(StateDiscovering, syncCompleted),

		// Call handlers for synchronous discovery
		statemachine.WithStateCallHandler(StateIdle, discoverSync),
		statemachine.WithStateCallHandler(StateDiscovering, discoverSync),
	)

	if discoveryCfg.Enabled {
		if err := d.Send(d.PID(), Discover{}); err != nil {
			return statemachine.StateMachineSpec[DiscoveryData]{}, fmt.Errorf("failed to send initial discovery message: %s", err)
		}
		d.Log().Info("Resource discovery ready")
	}

	return spec, nil
}

func onStateChange(oldState gen.Atom, newState gen.Atom, data DiscoveryData, proc gen.Process) (gen.Atom, DiscoveryData, error) {
	if oldState == StateDiscovering && newState == StateIdle {
		proc.Log().Debug("Discovery finished. The following resources have been discovered:\n"+renderSummary(data.summary), "duration", time.Since(data.timeStarted))
		if data.isScheduledDiscovery {
			_, err := proc.SendAfter(proc.PID(), Discover{}, data.discoveryCfg.Interval)
			if err != nil {
				proc.Log().Error("Failed to schedule next discovery run", "error", err)
				return newState, data, gen.TerminateReasonPanic
			}
		}
	}
	return newState, data, nil
}

func discover(state gen.Atom, data DiscoveryData, message Discover, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	data.isScheduledDiscovery = !message.Once
	data.timeStarted = time.Now()

	if state == StateDiscovering {
		proc.Log().Debug("Discovery already running, consider configuring a longer interval")
		return state, data, nil, nil
	}
	proc.Log().Debug("Starting resource discovery", "timestamp", data.timeStarted)

	allTargets, err := data.ds.LoadDiscoverableTargets()
	if err != nil {
		proc.Log().Error("Discovery: failed to load targets from datastore", "error", err)
		allTargets = []*pkgmodel.Target{}
	}
	data.SetTargets(allTargets)

	// If there are no discoverable targets, complete discovery immediately
	if len(data.targets) == 0 {
		proc.Log().Debug("No discoverable targets found, completing discovery")

		if data.isScheduledDiscovery {
			_, err := proc.SendAfter(proc.PID(), Discover{}, data.discoveryCfg.Interval)
			if err != nil {
				proc.Log().Error("Failed to schedule next discovery run", "error", err)
				return StateIdle, data, nil, gen.TerminateReasonPanic
			}
			proc.Log().Debug("Scheduled next discovery run", "interval", data.discoveryCfg.Interval)
		}

		return StateIdle, data, nil, nil
	}

	for _, target := range data.targets {
		resourcePlugin, err := data.pluginManager.ResourcePlugin(target.Namespace)
		if err != nil {
			proc.Log().Error("Discovery: no resource plugin for namespace %s: %v", target.Namespace, err)
			continue
		}

		discoverableResources := (*resourcePlugin).SupportedResources()
		supportedResources := make([]plugin.ResourceDescriptor, 0)
		for _, desc := range discoverableResources {
			if len(data.discoveryCfg.ResourceTypesToDiscover) == 0 ||
				slices.Contains(data.discoveryCfg.ResourceTypesToDiscover, desc.Type) {
				supportedResources = append(supportedResources, desc)
				data.resourceDescriptors[desc.Type] = desc
			}
		}

		data.resourceDescriptorRoots = constructParentResourceDescriptors(supportedResources)
		for _, desc := range data.resourceDescriptorRoots {
			data.queuedListOperations[target.Namespace] = append(data.queuedListOperations[target.Namespace],
				ListOperation{ResourceType: desc.ResourceType, TargetLabel: target.Label, ListParams: util.MapToString(make(map[string]string))})
		}
		err = proc.Send(proc.PID(), ResumeScanning{})
		if err != nil {
			proc.Log().Error("Discovery failed to send ResumeScanning message: %v", err)
			return state, data, nil, gen.TerminateReasonPanic
		}
	}

	return StateDiscovering, data, nil, nil
}

func discoverSync(state gen.Atom, data DiscoveryData, message DiscoverSync, proc gen.Process) (gen.Atom, DiscoveryData, *DiscoverSyncResult, []statemachine.Action, error) {
	if state == StateDiscovering {
		return state, data, &DiscoverSyncResult{
			Started:        false,
			AlreadyRunning: true,
		}, nil, nil
	}

	newState, newData, actions, err := discover(state, data, Discover(message), proc)
	if err != nil {
		proc.Log().Error("Discovery failed to start", "error", err)
		return newState, newData, &DiscoverSyncResult{
			Started:        false,
			AlreadyRunning: false,
		}, nil, nil
	}

	return newState, newData, &DiscoverSyncResult{
		Started:        true,
		AlreadyRunning: false,
	}, actions, nil
}

func resumeScanning(state gen.Atom, data DiscoveryData, message ResumeScanning, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	finished := make(map[string]bool)

	// Do as much work as the rate limiter allows for each namespace
	for namespace := range data.queuedListOperations {
		finished[namespace] = true
		remaining := len(data.queuedListOperations[namespace])
		tokens, err := proc.Call(actornames.RateLimiter, changeset.RequestTokens{Namespace: namespace, N: remaining})
		if err != nil {
			proc.Log().Error("Failed to fetch tokens for namespace.", "namespace", namespace, "error", err)
			return state, data, nil, gen.TerminateReasonPanic
		}
		n := tokens.(changeset.TokensGranted).N
		if n < remaining {
			finished[namespace] = false
		}

		for range n {
			next := data.queuedListOperations[namespace][0]
			err := scanTargetForResourceType(data.targets[next.TargetLabel], next.ResourceType, util.StringToMap(next.ListParams), data, proc)
			if err != nil {
				proc.Log().Error("Failed to scan target for resource type %s: %v", next.ResourceType, err)
				return state, data, nil, gen.TerminateReasonPanic
			}
			data.queuedListOperations[namespace] = data.queuedListOperations[namespace][1:]
		}
	}
	for namespace, done := range finished {
		if !done {
			_, err := proc.SendAfter(proc.PID(), ResumeScanning{}, 1*time.Second)
			if err != nil {
				proc.Log().Error("Discovery failed to send ResumeScanning message: %v", err)
				return state, data, nil, gen.TerminateReasonPanic
			}
			return state, data, nil, nil
		}
		delete(data.queuedListOperations, namespace)
	}

	return StateDiscovering, data, nil, nil
}

func scanTargetForResourceType(target pkgmodel.Target, resourceType string, listParameters map[string]string, data DiscoveryData, proc gen.Process) error {
	if !data.resourceDescriptors[resourceType].Discoverable {
		proc.Log().Debug("Skipping non-discoverable resource type %s in target %s", resourceType, target.Label)
		return nil
	}

	// Register the operation as remaining work
	op := ListOperation{
		ResourceType: resourceType,
		TargetLabel:  target.Label,
		ListParams:   util.MapToString(listParameters),
	}
	data.outstandingListOperations[op] = struct{}{}
	if _, exists := data.summary[resourceType]; !exists {
		data.summary[resourceType] = 0
	}

	uri := discoveryURI(resourceType)
	operation := resource.OperationList
	operationID := uuid.New().String()

	// Ensure the plugin operator supervisor is running
	_, err := proc.Call(actornames.PluginOperatorSupervisor, plugin_operation.EnsurePluginOperator{
		ResourceURI: uri,
		Operation:   operation,
		OperationID: operationID,
	})
	if err != nil {
		delete(data.outstandingListOperations, op)
		return fmt.Errorf("failed to ensure PluginOperator for %s: %w", uri, err)
	}

	err = proc.Send(actornames.PluginOperator(uri, string(operation), operationID), plugin_operation.ListResources{
		Namespace:      target.Namespace,
		ResourceType:   resourceType,
		Target:         target,
		ListParameters: listParameters,
	})
	if err != nil {
		delete(data.outstandingListOperations, op)
		return fmt.Errorf("discovery failed to send ListResources for %s: %w", uri, err)
	}
	proc.Log().Debug("Scanning resource type %s in target %s with list properties %v in %f seconds", resourceType, target.Label, listParameters)

	return nil
}

func processListing(state gen.Atom, data DiscoveryData, message plugin_operation.Listing, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	proc.Log().Debug("Processing listing for %s in target %s with list parameters %v", message.ResourceType, message.Target.Label, message.ListParameters)

	op := ListOperation{
		ResourceType: message.ResourceType,
		TargetLabel:  message.Target.Label,
		ListParams:   util.MapToString(message.ListParameters),
	}
	delete(data.outstandingListOperations, op)

	// Remove the operation from remaining work if the plugin reported an error
	if message.Error != nil {
		proc.Log().Error("Failed to list resources for %s in target %s: %v", message.ResourceType, message.Target.Label, message.Error)
		if !data.HasOutstandingWork() {
			return StateIdle, data, nil, nil
		}
		return state, data, nil, nil
	}

	// De-duplicate resources already under management
	var newResources []resource.Resource
	for _, resource := range message.Resources {
		if !resourceExists(resource.NativeID, data) {
			newResources = append(newResources, resource)
			data.recentlyDiscoveredResourceIDs[resource.NativeID] = struct{}{}
			data.summary[message.ResourceType]++
		}
	}

	// If there are no new resources, we can skip the synchronization
	if len(newResources) == 0 {
		proc.Log().Debug("No new resources to synchronize for %s in target %s", message.ResourceType, message.Target.Label)
		if !data.HasOutstandingWork() {
			return StateIdle, data, nil, nil
		}
		return state, data, nil, nil
	}

	// Synchronize the new resources, remove the operation from remaining work on error
	commandID, err := synchronizeResources(op, message.Target.Namespace, message.Target, newResources, data, proc)
	if err != nil {
		proc.Log().Error("Failed to synchronize resources for %s in target %s: %v", message.ResourceType, message.Target.Label, err)
		if !data.HasOutstandingWork() {
			return StateIdle, data, nil, nil
		}
	}

	// Register the asynchronously running sync command
	data.outstandingSyncCommands[commandID] = op

	return StateDiscovering, data, nil, nil
}

func synchronizeResources(op ListOperation, namespace string, target pkgmodel.Target, resources []resource.Resource, data DiscoveryData, proc gen.Process) (string, error) {
	plugin, err := data.pluginManager.ResourcePlugin(namespace)
	if err != nil {
		proc.Log().Error("failed to synchronize resources of type %s in target %s: no resource plugin for namespace %s: %v", op.ResourceType, op.TargetLabel, namespace, err)
		return "", err
	}
	schema, err := (*plugin).SchemaForResourceType(op.ResourceType)
	if err != nil {
		proc.Log().Error("failed to synchronize resources of type %s in target %s: no schema for resource type in plugin for namespace %s: %v", op.ResourceType, op.TargetLabel, namespace, err)
		return "", err
	}

	resourceFilters := (*plugin).GetResourceFilters()

	var resourcesToSynchronize []pkgmodel.Resource
	for _, resource := range resources {
		// We initially set the label to the native ID. Since not every List API reliably returns tags we need to overwrite the label
		// in the resource_updater where we sync (read) the resource.
		resourcesToSynchronize = append(resourcesToSynchronize, pkgmodel.Resource{
			Label:      url.QueryEscape(resource.NativeID),
			Type:       op.ResourceType,
			Stack:      constants.UnmanagedStack,
			Target:     target.Label,
			NativeID:   resource.NativeID,
			Properties: json.RawMessage(resource.Properties),
			Schema:     schema,
			Managed:    false,
			Ksuid:      util.NewID(),
		})
	}

	forma := pkgmodel.Forma{
		Targets:   []pkgmodel.Target{target},
		Resources: resourcesToSynchronize,
	}

	formaCommandConfig := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch}
	existingTargets, err := data.ds.LoadAllTargets()
	if err != nil {
		proc.Log().Error("failed to load existing targets: %v", err)
		return "", fmt.Errorf("failed to load targets: %w", err)
	}

	resourceUpdates, err := resource_update.GenerateResourceUpdates(&forma, pkgmodel.CommandSync, formaCommandConfig.Mode, resource_update.FormaCommandSourceDiscovery, existingTargets, data.ds, resourceFilters)
	if err != nil {
		proc.Log().Error("failed to generate resource updates: %v", err)
		return "", fmt.Errorf("failed to generate resource updates: %w", err)
	}

	syncCommand := forma_command.NewFormaCommand(
		&forma,
		formaCommandConfig,
		pkgmodel.CommandSync,
		resourceUpdates,
		nil, // No target updates on discovery
		"formae",
	)

	// store the forma command
	_, err = proc.Call(actornames.FormaCommandPersister, forma_persister.StoreNewFormaCommand{
		Command: *syncCommand,
	})
	if err != nil {
		proc.Log().Error("failed to store sync command", "error", err)
		return "", err
	}

	// Sync commands (READs) will never contain cycles so we can safely ignore the error here.
	cs, _ := changeset.NewChangesetFromResourceUpdates(syncCommand.ResourceUpdates, syncCommand.ID, pkgmodel.CommandApply)

	proc.Log().Debug("Ensuring ChangesetExecutor for sync command", "commandID", syncCommand.ID)
	_, err = proc.Call(
		gen.ProcessID{Name: actornames.ChangesetSupervisor, Node: proc.Node().Name()},
		changeset.EnsureChangesetExecutor{CommandID: syncCommand.ID},
	)
	if err != nil {
		slog.Error("failed to ensure ChangesetExecutor for sync command", "command", syncCommand.Command, "forma", syncCommand, "error", err)
		return "", err
	}

	proc.Log().Debug("Starting ChangesetExecutor for sync command", "commandID", syncCommand.ID)
	err = proc.Send(
		gen.ProcessID{Name: actornames.ChangesetExecutor(syncCommand.ID), Node: proc.Node().Name()},
		changeset.Start{Changeset: cs},
	)
	if err != nil {
		slog.Error("failed to start ChangesetExecutor for sync command", "command", syncCommand.Command, "forma", syncCommand, "error", err)
		return "", err
	}

	return syncCommand.ID, nil
}

func syncCompleted(state gen.Atom, data DiscoveryData, message changeset.ChangesetCompleted, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	op, exists := data.outstandingSyncCommands[message.CommandID]
	if !exists {
		proc.Log().Error("Discovery received ChangesetCompleted for unknown command ID", "commandID", message.CommandID)
		return state, data, nil, nil
	}
	delete(data.outstandingSyncCommands, message.CommandID)

	if message.State == changeset.ChangeSetStateFinishedWithErrors {
		proc.Log().Error("Discovery failed to synchronize discovered resources", "resourceType", op.ResourceType, "listParams", op.ListParams, "commandID", message.CommandID)
		if !data.HasOutstandingWork() {
			return StateIdle, data, nil, nil
		}
	}

	if data.resourceDescriptorRoots[op.ResourceType].HasNestedResources() {
		// Load all parent resources of this type that were just synchronized
		query := datastore.ResourceQuery{
			Type:   &datastore.QueryItem[string]{Item: op.ResourceType, Constraint: datastore.Required},
			Target: &datastore.QueryItem[string]{Item: op.TargetLabel, Constraint: datastore.Required},
		}
		parentResources, err := data.ds.QueryResources(&query)
		if err != nil {
			proc.Log().Error("Failed to load parent resources for discovery of nested resources", "resourceType", op.ResourceType, "target", op.TargetLabel, "error", err)
			return state, data, nil, gen.TerminateReasonPanic
		}
		proc.Log().Debug("Found %d parent resources of type %s in target %s", len(parentResources), op.ResourceType, op.TargetLabel)
		for _, parentResource := range parentResources {
			for nestedResource, mappingProperties := range data.resourceDescriptorRoots[op.ResourceType].NestedResourcesWithMappingProperties {
				proc.Log().Debug("Scanning for nested resource type %s in target %s with list properties %+v", nestedResource, op.TargetLabel, mappingProperties)
				listParameters := make(map[string]string)
				for _, param := range mappingProperties {
					proc.Log().Debug("Getting mapping property for nested resource discovery", "propertyQuery", param, "resourceType", op.ResourceType, "target", op.TargetLabel, "nativeId", parentResource.NativeID)
					value, found := parentResource.GetProperty(param.ParentProperty)
					if found {
						listParameters[param.ListProperty] = value
					} else {
						proc.Log().Error("Failed to get mapping property for nested resource discovery", "param", param, "resourceType", op.ResourceType, "target", op.TargetLabel, "nativeId", parentResource.NativeID)
					}
				}
				data.queuedListOperations[data.targets[op.TargetLabel].Namespace] = append(data.queuedListOperations[data.targets[op.TargetLabel].Namespace], ListOperation{
					ResourceType: nestedResource.ResourceType,
					TargetLabel:  op.TargetLabel,
					ListParams:   util.MapToString(listParameters),
				})
				err = proc.Send(proc.PID(), ResumeScanning{})
				if err != nil {
					proc.Log().Error("Discovery failed to send ResumeScanning message: %v", err)
					return state, data, nil, gen.TerminateReasonPanic
				}
			}
		}
	}
	if !data.HasOutstandingWork() {
		return StateIdle, data, nil, nil
	}

	return state, data, nil, nil
}

// Some resource types are global and might be discovered in multiple targets. This introduces a race condition. A resource can be discovered, but before the resource has
// been synchronized and created in the database the resource is then discovered again. Therefore we cannot rely on the database for deduplication but we maintain a set of
// recently discovered resources locally in the actor.
func resourceExists(nativeID string, data DiscoveryData) bool {
	_, recentlyDiscovered := data.recentlyDiscoveredResourceIDs[nativeID]
	if recentlyDiscovered {
		return true
	}

	resource, err := data.ds.LoadResourceByNativeID(nativeID)
	if err != nil {
		slog.Error("Failed to load resource by native ID", "nativeID", nativeID, "error", err)
		return false
	}
	return resource != nil
}

func constructParentResourceDescriptors(descriptors []plugin.ResourceDescriptor) map[string]ResourceDescriptorTreeNode {
	parentResources := make(map[string]ResourceDescriptorTreeNode)
	nestedDescriptors := make(map[string]plugin.ResourceDescriptor)
	for _, desc := range descriptors {
		slog.Debug("Processing resource descriptor", "type", desc.Type, "parents", desc.ParentResourceTypesWithMappingProperties, "discoverable", desc.Discoverable)
		if !desc.Discoverable {
			slog.Debug("Skipping non-discoverable resource type", "type", desc.Type)
			continue
		}
		if len(desc.ParentResourceTypesWithMappingProperties) == 0 {
			parentResources[desc.Type] = ResourceDescriptorTreeNode{
				ResourceType:                         desc.Type,
				Discoverable:                         desc.Discoverable,
				NestedResourcesWithMappingProperties: make(map[*ResourceDescriptorTreeNode][]plugin.ListParameter),
			}
		} else {
			nestedDescriptors[desc.Type] = desc
		}
	}
	for _, desc := range nestedDescriptors {
		for parentType, mappingProperties := range desc.ParentResourceTypesWithMappingProperties {
			parent, exists := parentResources[parentType]
			if !exists {
				slog.Error("Parent resource type for nested resource type not found", "parent", parentType, "nested", desc.Type)
				continue
			}
			parent.NestedResourcesWithMappingProperties[&ResourceDescriptorTreeNode{
				ResourceType:                         desc.Type,
				Discoverable:                         desc.Discoverable,
				NestedResourcesWithMappingProperties: make(map[*ResourceDescriptorTreeNode][]plugin.ListParameter),
			}] = mappingProperties
		}
	}
	slog.Debug("Constructed parent resource descriptors", "count", len(parentResources))

	return parentResources
}

func discoveryURI(resourceType string) pkgmodel.FormaeURI {
	parts := strings.Split(resourceType, "::")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	for i := range parts {
		parts[i] = strings.ToLower(parts[i])
	}
	namespace := strings.Join(parts, ".")

	// Generate a discovery-specific KSUID for this resource type
	discoveryKsuid := fmt.Sprintf("discovery-%s-%s", namespace, util.NewID())
	return pkgmodel.NewFormaeURI(discoveryKsuid, "")
}

func renderSummary(summary map[string]int) string {
	var builder strings.Builder

	// Define column headers and widths for formatting.
	const resourceHeader = "Resource Type"
	const countHeader = "Value"
	const resourceWidth = 40
	const countWidth = 5

	// Write the header row.
	headerFormat := fmt.Sprintf("%%-%ds  %%-%ds\n", resourceWidth, countWidth)
	builder.WriteString(fmt.Sprintf(headerFormat, resourceHeader, countHeader))

	// Write a separator line for the header.
	builder.WriteString(strings.Repeat("-", resourceWidth+countWidth+2) + "\n")

	// Iterate over the map and add resource types and counts as a row.
	rowFormat := fmt.Sprintf("%%-%ds  %%-%dd\n", resourceWidth, countWidth)
	for key, value := range summary {
		builder.WriteString(fmt.Sprintf(rowFormat, key, value))
	}

	return builder.String()
}

// migrateScanTargetsToDatabase migrates config-based scanTargets to the database.
// If a target exists in both config and DB, it will be updated to discoverable=true.
func migrateScanTargetsToDatabase(ds datastore.Datastore, discoveryCfg *pkgmodel.DiscoveryConfig, proc gen.Process) error {
	if len(discoveryCfg.ScanTargets) == 0 {
		return nil
	}

	createdCount, updatedCount, errorCount := 0, 0, 0
	for _, configTarget := range discoveryCfg.ScanTargets {
		existingTarget, err := ds.LoadTarget(configTarget.Label)
		if err != nil {
			proc.Log().Error("Failed to check if target exists in database", "target", configTarget.Label, "error", err)
			errorCount++
			continue
		}

		if existingTarget != nil {
			if !existingTarget.Discoverable {
				existingTarget.Discoverable = true
				_, err = ds.UpdateTarget(existingTarget)
				if err != nil {
					proc.Log().Error("Failed to update target to discoverable", "target", configTarget.Label, "error", err)
					errorCount++
					continue
				}
				updatedCount++
			} else {
				proc.Log().Debug("Target already discoverable, no update needed", "target", configTarget.Label)
			}
		} else {
			targetToCreate := configTarget
			targetToCreate.Discoverable = true

			_, err = ds.CreateTarget(&targetToCreate)
			if err != nil {
				proc.Log().Error("Failed to create target from scanTargets config", "target", configTarget.Label, "error", err)
				errorCount++
				continue
			}

			proc.Log().Debug("Created target from scanTargets config", "target", configTarget.Label)
			createdCount++
		}
	}

	if createdCount > 0 || updatedCount > 0 {
		message := fmt.Sprintf("DEPRECATION WARNING: The 'scanTargets' configuration option is deprecated and will be removed in a future release.\n\n"+
			"Please transition to setting 'discoverable' on individual targets in your Forma files instead.\n\n"+
			"For this release, any targets specified in 'scanTargets' have been automatically migrated:\n"+
			"- Created %d new target(s) with discoverable=true\n"+
			"- Updated %d existing target(s) to discoverable=true\n\n"+
			"No immediate action is required, but we recommend removing 'scanTargets' from your configuration file (~/.config/formae/formae.conf.pkl) and managing discovery through the 'discoverable' property on individual targets instead.\n\n"+
			"See: https://docs.formae.io/en/latest/core-concepts/target/",
			createdCount, updatedCount)
		proc.Log().Warning(message)
	}

	return nil
}

// TriggerOnce sends a synchronous discovery message and returns the result.
// Returns immediately when discovery starts (or if already running), not when it completes.
func TriggerOnce(proc gen.Process) (*DiscoverSyncResult, error) {
	discoveryPID := gen.ProcessID{
		Name: gen.Atom("Discovery"),
		Node: proc.Node().Name(),
	}

	result, err := proc.Call(discoveryPID, DiscoverSync{Once: true})
	if err != nil {
		return nil, err
	}

	return result.(*DiscoverSyncResult), nil
}
