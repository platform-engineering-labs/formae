// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"ergo.services/ergo/meta"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestPluginCoordinator manages plugin communication for the test harness.
// It receives plugin announcements, spawns PluginOperators on plugin nodes,
// and orchestrates Create/Delete operations for discovery tests.
type TestPluginCoordinator struct {
	act.Actor

	plugins       map[string]*RegisteredPluginInfo // namespace → plugin info
	retryConfig   model.RetryConfig
	agentNodeName string // The test harness node name (plugins will announce to this)
	networkCookie string // Network cookie for plugin communication

	// latestProgress stores the most recent progress for each operator PID
	// Updated by HandleMessage when ProgressResult arrives
	latestProgress map[gen.PID]resource.ProgressResult

	// spawnedPlugins tracks the Aliases of spawned plugin meta processes
	// These need to be terminated when stopping the test harness
	spawnedPlugins []gen.Alias
}

// RegisteredPluginInfo contains information about a registered plugin
type RegisteredPluginInfo struct {
	Namespace          string
	NodeName           gen.Atom
	SupportedResources []plugin.ResourceDescriptor
	ResourceSchemas    map[string]model.Schema
	MatchFilters       []plugin.MatchFilter
	RegisteredAt       time.Time
}

// NewPluginCoordinator creates a new TestPluginCoordinator actor factory
func NewPluginCoordinator() gen.ProcessBehavior {
	return &TestPluginCoordinator{}
}

func (c *TestPluginCoordinator) Init(args ...any) error {
	c.plugins = make(map[string]*RegisteredPluginInfo)
	c.latestProgress = make(map[gen.PID]resource.ProgressResult)
	c.spawnedPlugins = make([]gen.Alias, 0)

	// Default retry config for plugin operations
	c.retryConfig = model.RetryConfig{
		StatusCheckInterval: 5 * time.Second,
		MaxRetries:          3,
		RetryDelay:          2 * time.Second,
	}

	// Extract args: agentNodeName, networkCookie
	if len(args) >= 2 {
		if nodeName, ok := args[0].(string); ok {
			c.agentNodeName = nodeName
		}
		if cookie, ok := args[1].(string); ok {
			c.networkCookie = cookie
		}
	}

	c.Log().Info("TestPluginCoordinator started",
		"agentNode", c.agentNodeName,
		"hasCookie", c.networkCookie != "")
	return nil
}

// --- Request/Response types for Call handlers ---

// CheckPluginAvailable checks if a plugin is registered
type CheckPluginAvailable struct {
	Namespace string
}

// CheckPluginAvailableResult is the response
type CheckPluginAvailableResult struct {
	Available bool
	Info      *RegisteredPluginInfo
}

// GetPluginInfoRequest requests plugin info for a namespace
type GetPluginInfoRequest struct {
	Namespace string
}

// GetPluginInfoResult contains plugin capabilities
type GetPluginInfoResult struct {
	Found              bool
	Namespace          string
	SupportedResources []plugin.ResourceDescriptor
	ResourceSchemas    map[string]model.Schema
	Error              string
}

// SpawnPluginOperatorRequest requests spawning a PluginOperator on the plugin node
type SpawnPluginOperatorRequest struct {
	Namespace   string
	OperationID string
}

// SpawnPluginOperatorResult is the response
type SpawnPluginOperatorResult struct {
	PID   gen.PID
	Error string
}

// CreateResourceRequest orchestrates creating a resource via plugin
type CreateResourceRequest struct {
	ResourceType string
	Namespace    string
	Label        string
	Properties   json.RawMessage
	Target       model.Target
}

// CreateResourceResult is the response
type CreateResourceResult struct {
	OperatorPID     gen.PID                 // PID of the PluginOperator (for polling)
	InitialProgress resource.ProgressResult // Initial progress from the call
	Error           string
}

// DeleteResourceRequest orchestrates deleting a resource via plugin
type DeleteResourceRequest struct {
	ResourceType string
	Namespace    string
	NativeID     string
	Target       model.Target
}

// DeleteResourceResult is the response
type DeleteResourceResult struct {
	OperatorPID     gen.PID                 // PID of the PluginOperator (for polling)
	InitialProgress resource.ProgressResult // Initial progress from the call
	Error           string
}

// GetLatestProgressRequest gets the latest progress for an operation
type GetLatestProgressRequest struct {
	OperatorPID gen.PID
}

// GetLatestProgressResult is the response
type GetLatestProgressResult struct {
	Found    bool
	Progress resource.ProgressResult
}

// LaunchPluginRequest requests launching a plugin process
type LaunchPluginRequest struct {
	PluginBinaryPath string
	Namespace        string
	TestRunID        string // Unique test run ID for plugin node name disambiguation
}

// LaunchPluginResult is the response
type LaunchPluginResult struct {
	Error string
}

// TerminatePluginsRequest requests termination of all spawned plugin processes
type TerminatePluginsRequest struct{}

// TerminatePluginsResult is the response
type TerminatePluginsResult struct {
	Terminated int
	Error      string
}

// --- HandleCall for synchronous requests ---

func (c *TestPluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case CheckPluginAvailable:
		info, ok := c.plugins[req.Namespace]
		return CheckPluginAvailableResult{Available: ok, Info: info}, nil

	case GetPluginInfoRequest:
		return c.getPluginInfo(req), nil

	case SpawnPluginOperatorRequest:
		return c.spawnPluginOperator(req), nil

	case CreateResourceRequest:
		return c.createResource(req), nil

	case DeleteResourceRequest:
		return c.deleteResource(req), nil

	case GetLatestProgressRequest:
		return c.getLatestProgress(req), nil

	case LaunchPluginRequest:
		return c.launchPlugin(req), nil

	case TerminatePluginsRequest:
		return c.terminatePlugins(), nil

	default:
		return nil, fmt.Errorf("unknown request: %T", request)
	}
}

// --- HandleMessage for async messages (like plugin announcements) ---

func (c *TestPluginCoordinator) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case plugin.PluginAnnouncement:
		// Decompress capabilities from the announcement
		caps, err := plugin.DecompressCapabilities(msg.Capabilities)
		if err != nil {
			c.Log().Error("Failed to decompress capabilities",
				"namespace", msg.Namespace,
				"error", err)
			return fmt.Errorf("failed to decompress capabilities: %w", err)
		}

		c.Log().Info("Plugin announced",
			"namespace", msg.Namespace,
			"node", msg.NodeName,
			"resources", len(caps.SupportedResources))

		c.plugins[msg.Namespace] = &RegisteredPluginInfo{
			Namespace:          msg.Namespace,
			NodeName:           gen.Atom(msg.NodeName),
			SupportedResources: caps.SupportedResources,
			ResourceSchemas:    caps.ResourceSchemas,
			MatchFilters:       caps.MatchFilters,
			RegisteredAt:       time.Now(),
		}

	case plugin.TrackedProgress:
		// PluginOperator sends progress updates asynchronously via Send
		// The 'from' PID is the PluginOperator that sent this message
		c.Log().Debug("Received TrackedProgress",
			"from", from,
			"status", msg.OperationStatus,
			"nativeID", msg.NativeID)

		// Store the latest progress for this operator (extract embedded ProgressResult)
		c.latestProgress[from] = msg.ProgressResult

	default:
		c.Log().Debug("Received unknown message", "type", fmt.Sprintf("%T", message))
	}
	return nil
}

// --- Helper methods ---

func (c *TestPluginCoordinator) getLatestProgress(req GetLatestProgressRequest) GetLatestProgressResult {
	progress, found := c.latestProgress[req.OperatorPID]
	return GetLatestProgressResult{
		Found:    found,
		Progress: progress,
	}
}

func (c *TestPluginCoordinator) getPluginInfo(req GetPluginInfoRequest) GetPluginInfoResult {
	registered, ok := c.plugins[req.Namespace]
	if !ok {
		return GetPluginInfoResult{
			Found: false,
			Error: fmt.Sprintf("plugin not found: %s", req.Namespace),
		}
	}

	return GetPluginInfoResult{
		Found:              true,
		Namespace:          req.Namespace,
		SupportedResources: registered.SupportedResources,
		ResourceSchemas:    registered.ResourceSchemas,
	}
}

func (c *TestPluginCoordinator) spawnPluginOperator(req SpawnPluginOperatorRequest) SpawnPluginOperatorResult {
	registered, ok := c.plugins[req.Namespace]
	if !ok {
		return SpawnPluginOperatorResult{Error: fmt.Sprintf("plugin not found: %s", req.Namespace)}
	}

	// Get connection to remote node
	remoteNode, err := c.Node().Network().GetNode(registered.NodeName)
	if err != nil {
		c.Log().Error("Failed to get remote node",
			"namespace", req.Namespace,
			"node", registered.NodeName,
			"error", err)
		return SpawnPluginOperatorResult{Error: fmt.Sprintf("failed to get remote node: %v", err)}
	}

	// Generate a unique name for the operator
	registerName := gen.Atom(fmt.Sprintf("test-operator-%s-%s", req.Namespace, req.OperationID))

	// Remote spawn with registration
	opts := gen.ProcessOptions{
		Env: map[gen.Env]any{
			gen.Env("RetryConfig"): c.retryConfig,
		},
	}

	pid, err := remoteNode.SpawnRegister(registerName, plugin.PluginOperatorFactoryName, opts)
	if err != nil {
		c.Log().Error("Failed to remote spawn PluginOperator",
			"namespace", req.Namespace,
			"node", registered.NodeName,
			"error", err)
		return SpawnPluginOperatorResult{Error: fmt.Sprintf("failed to remote spawn: %v", err)}
	}

	c.Log().Debug("Remote spawned PluginOperator",
		"namespace", req.Namespace,
		"node", registered.NodeName,
		"pid", pid)

	return SpawnPluginOperatorResult{PID: pid}
}

func (c *TestPluginCoordinator) createResource(req CreateResourceRequest) CreateResourceResult {
	// Step 1: Spawn PluginOperator on plugin node
	operationID := fmt.Sprintf("create-%d", time.Now().UnixNano())
	spawnResult := c.spawnPluginOperator(SpawnPluginOperatorRequest{
		Namespace:   req.Namespace,
		OperationID: operationID,
	})
	if spawnResult.Error != "" {
		return CreateResourceResult{Error: spawnResult.Error}
	}

	// Step 2: Send Create request to PluginOperator
	createReq := plugin.CreateResource{
		Namespace:    req.Namespace,
		ResourceType: req.ResourceType,
		Label:        req.Label,
		Properties:   req.Properties,
		TargetConfig: req.Target.Config,
	}

	c.Log().Info("Sending CreateResource to PluginOperator",
		"resourceType", req.ResourceType,
		"operatorPID", spawnResult.PID)

	result, err := c.CallWithTimeout(spawnResult.PID, createReq, 120) // 2 minute timeout
	if err != nil {
		return CreateResourceResult{Error: fmt.Sprintf("CreateResource call failed: %v", err)}
	}

	trackedProgress, ok := result.(plugin.TrackedProgress)
	if !ok {
		return CreateResourceResult{Error: fmt.Sprintf("unexpected response type: %T", result)}
	}

	c.Log().Info("CreateResource initial response",
		"resourceType", req.ResourceType,
		"status", trackedProgress.OperationStatus,
		"nativeID", trackedProgress.NativeID)

	// Return immediately with the operator PID and initial progress
	// The caller is responsible for polling via GetLatestProgressRequest if needed
	return CreateResourceResult{
		OperatorPID:     spawnResult.PID,
		InitialProgress: trackedProgress.ProgressResult,
	}
}

func (c *TestPluginCoordinator) deleteResource(req DeleteResourceRequest) DeleteResourceResult {
	// Step 1: Spawn PluginOperator on plugin node
	operationID := fmt.Sprintf("delete-%d", time.Now().UnixNano())
	spawnResult := c.spawnPluginOperator(SpawnPluginOperatorRequest{
		Namespace:   req.Namespace,
		OperationID: operationID,
	})
	if spawnResult.Error != "" {
		return DeleteResourceResult{Error: spawnResult.Error}
	}

	// Step 2: Send Delete request to PluginOperator
	deleteReq := plugin.DeleteResource{
		Namespace:    req.Namespace,
		NativeID:     req.NativeID,
		ResourceType: req.ResourceType,
		Resource: model.Resource{
			Type:     req.ResourceType,
			NativeID: req.NativeID,
		},
		TargetConfig: req.Target.Config,
	}

	c.Log().Info("Sending DeleteResource to PluginOperator",
		"resourceType", req.ResourceType,
		"nativeID", req.NativeID,
		"operatorPID", spawnResult.PID)

	result, err := c.CallWithTimeout(spawnResult.PID, deleteReq, 120) // 2 minute timeout
	if err != nil {
		return DeleteResourceResult{Error: fmt.Sprintf("DeleteResource call failed: %v", err)}
	}

	trackedProgress, ok := result.(plugin.TrackedProgress)
	if !ok {
		return DeleteResourceResult{Error: fmt.Sprintf("unexpected response type: %T", result)}
	}

	c.Log().Info("DeleteResource initial response",
		"resourceType", req.ResourceType,
		"nativeID", req.NativeID,
		"status", trackedProgress.OperationStatus)

	// Return immediately with the operator PID and initial progress
	// The caller is responsible for polling via GetLatestProgressRequest if needed
	return DeleteResourceResult{
		OperatorPID:     spawnResult.PID,
		InitialProgress: trackedProgress.ProgressResult,
	}
}

func (c *TestPluginCoordinator) launchPlugin(req LaunchPluginRequest) LaunchPluginResult {
	// Create unique node name for the test harness plugin (different from agent's plugin)
	// This avoids conflicts when both test harness and agent try to run the same plugin
	pluginNodeName := fmt.Sprintf("test-%s-plugin-%s@localhost", strings.ToLower(req.Namespace), req.TestRunID)

	// Configure meta.Port options to spawn the plugin process
	portOptions := meta.PortOptions{
		Cmd:         req.PluginBinaryPath,
		EnableEnvOS: true, // Inherit OS environment (PATH, etc.) so plugins can find dependencies like pkl
		Env: map[gen.Env]string{
			gen.Env("FORMAE_AGENT_NODE"):     c.agentNodeName, // Plugin will announce to our node
			gen.Env("FORMAE_PLUGIN_NODE"):    pluginNodeName,
			gen.Env("FORMAE_NETWORK_COOKIE"): c.networkCookie,
		},
		Tag:     req.Namespace,
		Process: gen.Atom("PluginCoordinator"), // Route messages to our coordinator
	}

	// Create meta.Port
	metaport, err := meta.CreatePort(portOptions)
	if err != nil {
		return LaunchPluginResult{Error: fmt.Sprintf("failed to create meta.Port: %v", err)}
	}

	// Spawn the plugin process using the actor's SpawnMeta method
	metaPID, err := c.SpawnMeta(metaport, gen.MetaOptions{})
	if err != nil {
		return LaunchPluginResult{Error: fmt.Sprintf("failed to spawn plugin via meta.Port: %v", err)}
	}

	// Track the spawned plugin PID so we can terminate it later
	c.spawnedPlugins = append(c.spawnedPlugins, metaPID)

	c.Log().Info("Launched plugin", "namespace", req.Namespace, "node", pluginNodeName, "metaPID", metaPID)
	return LaunchPluginResult{}
}

// terminatePlugins terminates all spawned plugin processes
func (c *TestPluginCoordinator) terminatePlugins() TerminatePluginsResult {
	terminated := 0
	for _, alias := range c.spawnedPlugins {
		c.Log().Info("Terminating plugin process", "alias", alias)
		// Send terminate message to the meta.Port process
		if err := c.Send(alias, meta.MessagePortTerminate{}); err != nil {
			c.Log().Warning("Failed to send terminate to plugin", "alias", alias, "error", err)
		} else {
			terminated++
		}
	}
	// Clear the list
	c.spawnedPlugins = make([]gen.Alias, 0)
	return TerminatePluginsResult{Terminated: terminated}
}
