// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package discovery

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"ergo.services/actor/statemachine"
	"ergo.services/ergo/gen"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_persister"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
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

type hierarchyNode struct {
	resourceType string
	discoverable bool
	children     map[*hierarchyNode][]plugin.ListParameter
}

func (n *hierarchyNode) hasChildren() bool {
	return len(n.children) > 0
}

func NewDiscovery() gen.ProcessBehavior {
	return &Discovery{}
}

// Messages processed by Discovery

type Discover struct {
	Once bool
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
	resourceHierarchy             map[string]*hierarchyNode
	resourceDescriptors           map[string]plugin.ResourceDescriptor
	queuedListOperations          map[string][]ListOperation
	outstandingListOperations     map[string]ListOperation
	outstandingSyncCommands       map[string]ListOperation
	recentlyDiscoveredResourceIDs map[string]struct{}
	timeStarted                   time.Time
	summary                       map[string]int
	// pauseCount tracks the number of active pause requests. When > 0, Discovery
	// will not start new list operations, but will allow outstanding operations
	// to complete. Uses reference counting to support multiple concurrent pausers.
	pauseCount int
	// hasPendingResumeScan tracks whether a delayed ResumeScanning message is in flight.
	// This prevents sending redundant delayed messages when one is already pending.
	hasPendingResumeScan bool
	// Cached plugin info per namespace, refreshed at start of each discovery cycle
	pluginInfoCache map[string]*messages.PluginInfoResponse
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
	ParentKSUID  string
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

	data := DiscoveryData{
		pluginManager:                 pluginManager.(*plugin.Manager),
		ds:                            ds,
		discoveryCfg:                  &discoveryCfg,
		serverCfg:                     &serverCfg,
		targets:                       make(map[string]pkgmodel.Target),
		resourceHierarchy:             make(map[string]*hierarchyNode),
		resourceDescriptors:           make(map[string]plugin.ResourceDescriptor),
		queuedListOperations:          make(map[string][]ListOperation),
		outstandingListOperations:     make(map[string]ListOperation),
		outstandingSyncCommands:       make(map[string]ListOperation),
		recentlyDiscoveredResourceIDs: make(map[string]struct{}),
		summary:                       make(map[string]int),
		pluginInfoCache:               make(map[string]*messages.PluginInfoResponse),
	}

	spec := statemachine.NewStateMachineSpec(StateIdle,
		statemachine.WithData(data),

		statemachine.WithStateEnterCallback(onStateChange),

		statemachine.WithStateMessageHandler(StateIdle, discover),
		statemachine.WithStateMessageHandler(StateDiscovering, discover),
		statemachine.WithStateMessageHandler(StateDiscovering, resumeScanning),
		statemachine.WithStateMessageHandler(StateDiscovering, processListing),
		statemachine.WithStateMessageHandler(StateDiscovering, syncCompleted),

		// Handle pause/resume in both states to allow pausing at any time
		statemachine.WithStateCallHandler(StateIdle, pauseDiscovery),
		statemachine.WithStateCallHandler(StateDiscovering, pauseDiscovery),
		statemachine.WithStateMessageHandler(StateIdle, resumeDiscovery),
		statemachine.WithStateMessageHandler(StateDiscovering, resumeDiscovery),
	)

	if discoveryCfg.Enabled {
		if _, err := d.SendAfter(d.PID(), Discover{}, 10*time.Second); err != nil {
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

func pauseDiscovery(from gen.PID, state gen.Atom, data DiscoveryData, message messages.PauseDiscovery, proc gen.Process) (gen.Atom, DiscoveryData, messages.PauseDiscoveryResponse, []statemachine.Action, error) {
	data.pauseCount++
	proc.Log().Debug("Discovery paused", "pauseCount", data.pauseCount, "outstandingListOps", len(data.outstandingListOperations), "outstandingSyncCmds", len(data.outstandingSyncCommands))
	return state, data, messages.PauseDiscoveryResponse{}, nil, nil
}

func resumeDiscovery(from gen.PID, state gen.Atom, data DiscoveryData, message messages.ResumeDiscovery, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	if data.pauseCount > 0 {
		data.pauseCount--
	}
	proc.Log().Debug("Discovery resume requested", "pauseCount", data.pauseCount)

	// If we're no longer paused and have queued operations, resume scanning
	if data.pauseCount == 0 && len(data.queuedListOperations) > 0 {
		err := proc.Send(proc.PID(), ResumeScanning{})
		if err != nil {
			proc.Log().Error("Failed to send ResumeScanning after unpause", "error", err)
			return state, data, nil, gen.TerminateReasonPanic
		}
	}

	return state, data, nil, nil
}

// getPluginInfo fetches plugin info from PluginCoordinator
func getPluginInfo(proc gen.Process, namespace string, refreshFilters bool) (*messages.PluginInfoResponse, error) {
	result, err := proc.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
		messages.GetPluginInfo{Namespace: namespace, RefreshFilters: refreshFilters},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get plugin info for %s: %w", namespace, err)
	}

	response, ok := result.(messages.PluginInfoResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response type from PluginCoordinator")
	}

	if !response.Found {
		return nil, fmt.Errorf("plugin not found: %s", response.Error)
	}

	return &response, nil
}

func discover(from gen.PID, state gen.Atom, data DiscoveryData, message Discover, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	// Check overlap protection FIRST - don't modify any state if discovery is already running
	// This prevents clearing the pluginInfoCache while a previous cycle still has in-flight messages
	if state == StateDiscovering {
		proc.Log().Debug("Discovery already running, consider configuring a longer interval")
		return state, data, nil, nil
	}

	// Only initialize new cycle state after confirming we're starting a new cycle
	data.isScheduledDiscovery = !message.Once
	data.timeStarted = time.Now()
	data.summary = make(map[string]int)

	// Clear plugin info cache at start of each discovery cycle
	data.pluginInfoCache = make(map[string]*messages.PluginInfoResponse)
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
		// Get plugin info from PluginCoordinator (with filter refresh for fresh filters)
		pluginInfo, err := getPluginInfo(proc, target.Namespace, true)
		if err != nil {
			proc.Log().Info("Discovery: no plugin for namespace %s, skipping: %v", target.Namespace, err)
			continue
		}

		// Cache for later use
		data.pluginInfoCache[target.Namespace] = pluginInfo

		discoverableResources := pluginInfo.SupportedResources
		supportedResources := make([]plugin.ResourceDescriptor, 0)
		for _, desc := range discoverableResources {
			if len(data.discoveryCfg.ResourceTypesToDiscover) == 0 ||
				slices.Contains(data.discoveryCfg.ResourceTypesToDiscover, desc.Type) {
				supportedResources = append(supportedResources, desc)
				data.resourceDescriptors[desc.Type] = desc
			}
		}

		newHierarchy := buildResourceHierarchy(supportedResources)
		maps.Copy(data.resourceHierarchy, newHierarchy)

		for resourceType, node := range newHierarchy {
			originalDesc, exists := data.resourceDescriptors[resourceType]
			if !exists {
				proc.Log().Error("Resource descriptor not found for type %s", resourceType)
				continue
			}
			if len(originalDesc.ParentResourceTypesWithMappingProperties) == 0 {
				data.queuedListOperations[target.Namespace] = append(data.queuedListOperations[target.Namespace],
					ListOperation{ResourceType: node.resourceType, TargetLabel: target.Label, ListParams: util.MapToString(make(map[string]plugin.ListParam))})
			}
		}
		err = proc.Send(proc.PID(), ResumeScanning{})
		if err != nil {
			proc.Log().Error("Discovery failed to send ResumeScanning message: %v", err)
			return state, data, nil, gen.TerminateReasonPanic
		}
	}

	return StateDiscovering, data, nil, nil
}

func resumeScanning(from gen.PID, state gen.Atom, data DiscoveryData, message ResumeScanning, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	// The delayed message has arrived - clear the pending flag
	data.hasPendingResumeScan = false

	// Don't start new list operations if Discovery is paused
	if data.pauseCount > 0 {
		proc.Log().Debug("Discovery is paused, skipping resumeScanning", "pauseCount", data.pauseCount)
		return state, data, nil, nil
	}

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
			nextOp := data.queuedListOperations[namespace][0]
			err := scanTargetForResourceType(data.targets[nextOp.TargetLabel], nextOp, data, proc)
			if err != nil {
				proc.Log().Error("Failed to scan target for resource type %s: %v", nextOp.ResourceType, err)
				return state, data, nil, gen.TerminateReasonPanic
			}
			data.queuedListOperations[namespace] = data.queuedListOperations[namespace][1:]
		}
	}
	for namespace, done := range finished {
		if !done {
			// Send a delayed message to continue processing
			_, err := proc.SendAfter(proc.PID(), ResumeScanning{}, 1*time.Second)
			if err != nil {
				proc.Log().Error("Discovery failed to send ResumeScanning message: %v", err)
				return state, data, nil, gen.TerminateReasonPanic
			}
			data.hasPendingResumeScan = true
			return state, data, nil, nil
		}
		delete(data.queuedListOperations, namespace)
	}

	return StateDiscovering, data, nil, nil
}

func scanTargetForResourceType(target pkgmodel.Target, op ListOperation, data DiscoveryData, proc gen.Process) error {
	if !data.resourceDescriptors[op.ResourceType].Discoverable {
		proc.Log().Debug("Skipping non-discoverable resource type %s in target %s", op.ResourceType, target.Label)
		return nil
	}

	// Register the operation as remaining work
	mapKey := listOperationMapKey(op.ResourceType, op.TargetLabel, op.ListParams)
	data.outstandingListOperations[mapKey] = op
	if _, exists := data.summary[op.ResourceType]; !exists {
		data.summary[op.ResourceType] = 0
	}

	uri := discoveryURI(op.ResourceType)
	operation := resource.OperationList
	operationID := uuid.New().String()

	// Spawn PluginOperator via PluginCoordinator
	spawnResult, err := proc.Call(
		gen.ProcessID{Name: actornames.PluginCoordinator, Node: proc.Node().Name()},
		messages.SpawnPluginOperator{
			Namespace:   target.Namespace,
			ResourceURI: string(uri),
			Operation:   string(operation),
			OperationID: operationID,
			RequestedBy: proc.PID(),
		})
	if err != nil {
		delete(data.outstandingListOperations, mapKey)
		return fmt.Errorf("failed to spawn PluginOperator for %s: %w", uri, err)
	}
	listParameters := util.StringToMap[plugin.ListParam](op.ListParams)
	spawnRes, ok := spawnResult.(messages.SpawnPluginOperatorResult)
	if !ok {
		delete(data.outstandingListOperations, mapKey)
		return fmt.Errorf("unexpected result type from PluginCoordinator: %T", spawnResult)
	}
	if spawnRes.Error != "" {
		delete(data.outstandingListOperations, mapKey)
		return fmt.Errorf("failed to spawn PluginOperator: %s", spawnRes.Error)
	}

	err = proc.Send(spawnRes.PID, plugin.ListResources{
		Namespace:      target.Namespace,
		ResourceType:   op.ResourceType,
		Target:         target,
		ListParameters: listParameters,
	})
	if err != nil {
		delete(data.outstandingListOperations, mapKey)
		return fmt.Errorf("discovery failed to send ListResources for %s: %w", uri, err)
	}
	proc.Log().Debug("Scanning resource type %s in target %s with list properties %v in %f seconds", op.ResourceType, target.Label, listParameters)

	return nil
}

func processListing(from gen.PID, state gen.Atom, data DiscoveryData, message plugin.Listing, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
	proc.Log().Debug("Processing listing for %s in target %s with list parameters %v", message.ResourceType, message.Target.Label, message.ListParameters)

	mapKey := listOperationMapKey(message.ResourceType, message.Target.Label, util.MapToString(message.ListParameters))
	op := data.outstandingListOperations[mapKey]

	delete(data.outstandingListOperations, mapKey)

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
		if !resourceExists(resource.NativeID, message.ResourceType, data) {
			newResources = append(newResources, resource)
			key := fmt.Sprintf("%s::%s", message.ResourceType, resource.NativeID)
			data.recentlyDiscoveredResourceIDs[key] = struct{}{}
			data.summary[message.ResourceType]++
		}
	}

	// If there are no new resources, we can skip the synchronization
	// However, we still need to discover nested resources if this parent resource type has any
	if len(newResources) == 0 {
		proc.Log().Debug("No new resources to synchronize for %s in target %s", message.ResourceType, message.Target.Label)
		if err := discoverChildren(op, data, proc); err != nil {
			proc.Log().Error("Failed to discover children for %s in target %s: %v", message.ResourceType, message.Target.Label, err)
			if !data.HasOutstandingWork() {
				return StateIdle, data, nil, nil
			}
			return state, data, nil, nil
		}
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

	data.outstandingSyncCommands[commandID] = op

	// Discover nested resources for existing parents immediately, even though we're also syncing new ones.
	// This ensures we don't miss nested resources for existing parents while waiting for new ones to sync.
	// New nested resources will be discovered after sync completes in syncCompleted().
	if err := discoverChildren(op, data, proc); err != nil {
		proc.Log().Error("Failed to discover children for %s in target %s: %v", message.ResourceType, message.Target.Label, err)
	}

	return StateDiscovering, data, nil, nil
}

func listOperationMapKey(resourceType, targetLabel, listParams string) string {
	return fmt.Sprintf("%s#%s#%s", resourceType, targetLabel, listParams)
}

// getSchemaFromCache retrieves a schema from the cached plugin info
func getSchemaFromCache(data *DiscoveryData, namespace, resourceType string) (pkgmodel.Schema, error) {
	pluginInfo, ok := data.pluginInfoCache[namespace]
	if !ok {
		return pkgmodel.Schema{}, fmt.Errorf("no cached plugin info for %s", namespace)
	}

	schema, ok := pluginInfo.ResourceSchemas[resourceType]
	if !ok {
		return pkgmodel.Schema{}, fmt.Errorf("schema not found for %s", resourceType)
	}

	return schema, nil
}

// getMatchFiltersFromCache retrieves MatchFilters from the cached plugin info
func getMatchFiltersFromCache(data *DiscoveryData, namespace string) []plugin.MatchFilter {
	pluginInfo, ok := data.pluginInfoCache[namespace]
	if !ok {
		return nil
	}
	return pluginInfo.MatchFilters
}

// findMatchFiltersForType finds all MatchFilters that apply to the given resource type
func findMatchFiltersForType(filters []plugin.MatchFilter, resourceType string) []plugin.MatchFilter {
	var result []plugin.MatchFilter
	for i := range filters {
		for _, rt := range filters[i].ResourceTypes {
			if rt == resourceType {
				result = append(result, filters[i])
				break
			}
		}
	}
	return result
}

func synchronizeResources(op ListOperation, namespace string, target pkgmodel.Target, resources []resource.Resource, data DiscoveryData, proc gen.Process) (string, error) {
	// Get schema from cache instead of calling plugin directly
	schema, err := getSchemaFromCache(&data, namespace, op.ResourceType)
	if err != nil {
		proc.Log().Error("failed to synchronize resources of type %s in target %s: %v", op.ResourceType, op.TargetLabel, err)
		return "", err
	}

	// For backward compatibility with function-based filters:
	var nativeIDs []string
	for _, resource := range resources {
		nativeIDs = append(nativeIDs, resource.NativeID)
	}

	var resourcesToSynchronize []pkgmodel.Resource
	for _, nativeID := range nativeIDs {
		resourcesToSynchronize = append(resourcesToSynchronize, pkgmodel.Resource{
			Label:      url.QueryEscape(nativeID),
			Type:       op.ResourceType,
			Stack:      constants.UnmanagedStack,
			Target:     target.Label,
			NativeID:   nativeID,
			Properties: injectResolvables("{}", op),
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

	resourceUpdates, err := resource_update.GenerateResourceUpdates(&forma, pkgmodel.CommandSync, formaCommandConfig.Mode, resource_update.FormaCommandSourceDiscovery, existingTargets, data.ds)
	if err != nil {
		proc.Log().Error("failed to generate resource updates: %v", err)
		return "", fmt.Errorf("failed to generate resource updates: %w", err)
	}

	// Attach MatchFilters from cache to resource updates for declarative filtering
	matchFilters := getMatchFiltersFromCache(&data, namespace)
	for i := range resourceUpdates {
		filters := findMatchFiltersForType(matchFilters, resourceUpdates[i].Resource.Type)
		if len(filters) > 0 {
			resourceUpdates[i].MatchFilters = filters
		}
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

func discoverChildren(op ListOperation, data DiscoveryData, proc gen.Process) error {
	parentNode, exists := data.resourceHierarchy[op.ResourceType]
	if !exists || !parentNode.hasChildren() {
		return nil
	}

	query := datastore.ResourceQuery{
		Type:   &datastore.QueryItem[string]{Item: op.ResourceType, Constraint: datastore.Required},
		Target: &datastore.QueryItem[string]{Item: op.TargetLabel, Constraint: datastore.Required},
	}
	parents, err := data.ds.QueryResources(&query)
	if err != nil {
		proc.Log().Error("Failed to load parent resources", "type", op.ResourceType, "target", op.TargetLabel, "error", err)
		return fmt.Errorf("failed to load parent resources: %w", err)
	}

	for _, parent := range parents {
		for childNode, mappingProps := range parentNode.children {
			listParams := make(map[string]plugin.ListParam)
			for _, param := range mappingProps {
				value, found := parent.GetProperty(param.ParentProperty)
				if found {
					listParams[param.ListProperty] = plugin.ListParam{
						ParentProperty: param.ParentProperty,
						ListParam:      param.ListProperty,
						ListValue:      value,
					}
				} else {
					proc.Log().Error("Missing parent property", "property", param.ParentProperty, "parent_id", parent.NativeID)
				}
			}
			data.queuedListOperations[data.targets[op.TargetLabel].Namespace] = append(data.queuedListOperations[data.targets[op.TargetLabel].Namespace], ListOperation{
				ResourceType: childNode.resourceType,
				TargetLabel:  op.TargetLabel,
				ParentKSUID:  parent.Ksuid,
				ListParams:   util.MapToString(listParams),
			})
			// Only send immediate ResumeScanning if no delayed message is pending.
			// If a delayed message is pending, it will process the queued work when it arrives.
			if !data.hasPendingResumeScan {
				err = proc.Send(proc.PID(), ResumeScanning{})
				if err != nil {
					proc.Log().Error("Failed to send ResumeScanning", "error", err)
					return fmt.Errorf("failed to send ResumeScanning: %w", err)
				}
			}
		}
	}

	return nil
}

func syncCompleted(from gen.PID, state gen.Atom, data DiscoveryData, message changeset.ChangesetCompleted, proc gen.Process) (gen.Atom, DiscoveryData, []statemachine.Action, error) {
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

	// Discover nested resources after parent resources are completely sync'd
	if err := discoverChildren(op, data, proc); err != nil {
		proc.Log().Error("Failed to discover children for %s in target %s: %v", op.ResourceType, op.TargetLabel, err)
		if !data.HasOutstandingWork() {
			return StateIdle, data, nil, nil
		}
		return state, data, nil, nil
	}
	if !data.HasOutstandingWork() {
		return StateIdle, data, nil, nil
	}

	return state, data, nil, nil
}

// Some resource types are global and might be discovered in multiple targets. This introduces a race condition. A resource can be discovered, but before the resource has
// been synchronized and created in the database the resource is then discovered again. Therefore we cannot rely on the database for deduplication but we maintain a set of
// recently discovered resources locally in the actor.
func resourceExists(nativeID string, resourceType string, data DiscoveryData) bool {
	key := fmt.Sprintf("%s::%s", resourceType, nativeID)
	_, recentlyDiscovered := data.recentlyDiscoveredResourceIDs[key]
	if recentlyDiscovered {
		return true
	}

	resource, err := data.ds.LoadResourceByNativeID(nativeID, resourceType)
	if err != nil {
		slog.Error("Failed to load resource by native ID", "nativeID", nativeID, "resourceType", resourceType, "error", err)
		return false
	}
	return resource != nil
}

func buildResourceHierarchy(descriptors []plugin.ResourceDescriptor) map[string]*hierarchyNode {
	hierarchy := make(map[string]*hierarchyNode)
	childDescriptors := make(map[string]plugin.ResourceDescriptor)

	for _, desc := range descriptors {
		if !desc.Discoverable {
			continue
		}

		hierarchy[desc.Type] = &hierarchyNode{
			resourceType: desc.Type,
			discoverable: desc.Discoverable,
			children:     make(map[*hierarchyNode][]plugin.ListParameter),
		}

		if len(desc.ParentResourceTypesWithMappingProperties) > 0 {
			childDescriptors[desc.Type] = desc
		}
	}

	for _, desc := range childDescriptors {
		childNode := hierarchy[desc.Type]
		for parentType, mappingProps := range desc.ParentResourceTypesWithMappingProperties {
			parentNode, exists := hierarchy[parentType]
			if !exists {
				slog.Error("Parent resource type not found", "parent", parentType, "child", desc.Type)
				continue
			}
			parentNode.children[childNode] = mappingProps
		}
	}

	return hierarchy
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

func injectResolvables(props string, op ListOperation) json.RawMessage {
	if op.ParentKSUID == "" {
		return json.RawMessage(props)
	}

	params := util.StringToMap[plugin.ListParam](op.ListParams)
	for _, v := range params {
		// Check if there's an existing value and validate it matches the parent's expected value
		// This prevents incorrect parent associations when resources are discovered via multiple paths
		existingProp := gjson.Get(props, v.ListParam)
		if existingProp.Exists() {
			// If it's already a reference object, check if the $value matches
			if existingProp.IsObject() && existingProp.Get("$ref").Exists() {
				existingValue := existingProp.Get("$value").String()
				if existingValue != v.ListValue {
					slog.Debug("Skipping parent reference injection - existing ref has different value",
						"field", v.ListParam,
						"existingValue", existingValue,
						"expectedValue", v.ListValue,
						"parentKSUID", op.ParentKSUID)
					continue
				}
				// Value matches, but already has a reference - skip to avoid overwriting
				continue
			}

			// If it's a plain string value, validate it matches before converting to reference
			if existingValue := existingProp.String(); existingValue != v.ListValue {
				slog.Debug("Skipping parent reference injection - value mismatch",
					"field", v.ListParam,
					"existingValue", existingValue,
					"expectedValue", v.ListValue,
					"parentKSUID", op.ParentKSUID)
				continue
			}
		}

		// Either no existing value, or it matches the expected value - inject the reference
		prop := struct {
			Ref   string `json:"$ref"`
			Value string `json:"$value"`
		}{
			Ref:   fmt.Sprintf("formae://%s#/%s", op.ParentKSUID, v.ParentProperty),
			Value: v.ListValue,
		}
		props, _ = sjson.Set(props, v.ListParam, prop)
	}

	return json.RawMessage(props)
}
