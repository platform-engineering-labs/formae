// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_coordinator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// PluginCoordinator maintains a registry of all available resource plugins (local and remote).
// Plugins announce themselves to the coordinator on startup.
// It is the single source of truth for "where is plugin X" - supporting both local and remote plugins.
// It also handles spawning PluginOperator actors for resource operations.
type PluginCoordinator struct {
	act.Actor

	plugins                   map[string]*RegisteredPlugin // namespace → plugin info (remote)
	registeredLocalNamespaces map[string]bool              // namespaces registered with RateLimiter (local)
	testPlugin                plugin.FullResourcePlugin    // test-only: directly injected plugin (e.g. FakeAWS) for workflow tests
	retryConfig               model.RetryConfig
	resourcePluginConfigs     map[string]model.ResourcePluginUserConfig // keyed by plugin name (lowercase)
}

// RegisteredPlugin contains information about a registered plugin
type RegisteredPlugin struct {
	Namespace            string
	Version              string
	NodeName             gen.Atom // Ergo node where plugin runs (for remote spawn)
	MaxRequestsPerSecond int
	RegisteredAt         time.Time

	// Cached capabilities from announcement
	SupportedResources []plugin.ResourceDescriptor
	ResourceSchemas    map[string]model.Schema
	MatchFilters       []model.MatchFilter
	LabelConfig        model.LabelConfig

	// Per-plugin config (merged from user config)
	ResourceTypesToDiscover []string
	RetryConfig             *model.RetryConfig
}

// findPluginByNamespace performs a case-insensitive lookup for a plugin by namespace.
// We use case-insensitive matching because different parts of the system may use different
// casing conventions for namespaces. For example, the PKL Target schema normalizes namespace
// to uppercase (via toUpperCase()), while plugins may register with mixed case (e.g., "Azure"
// vs "AZURE"). This ensures seamless plugin discovery regardless of casing.
// Note: If we ever need to support plugins with the same name but different casing (unlikely),
// we would need to revisit this approach.
func (c *PluginCoordinator) findPluginByNamespace(namespace string) (*RegisteredPlugin, bool) {
	for ns, plugin := range c.plugins {
		if strings.EqualFold(ns, namespace) {
			return plugin, true
		}
	}
	return nil, false
}

// findTestPlugin returns the test plugin if it matches the given namespace (case-insensitive).
func (c *PluginCoordinator) findTestPlugin(namespace string) plugin.FullResourcePlugin {
	if c.testPlugin != nil && strings.EqualFold(c.testPlugin.Namespace(), namespace) {
		return c.testPlugin
	}

	return nil
}

// mergePluginConfig overlays user config on top of plugin-announced defaults.
// Returns a zero-value RegisteredPlugin and false if the plugin is disabled.
// Config is looked up by plugin name (from manifest), not namespace.
func (c *PluginCoordinator) mergePluginConfig(name, namespace string, announced RegisteredPlugin) (RegisteredPlugin, bool) {
	userCfg, hasUserConfig := c.resourcePluginConfigs[strings.ToLower(name)]

	if hasUserConfig && !userCfg.Enabled {
		c.Log().Info("Plugin disabled by config, skipping registration: name=%s namespace=%s", name, namespace)
		return RegisteredPlugin{}, false
	}

	merged := announced

	if hasUserConfig {
		if userCfg.RateLimit != nil {
			merged.MaxRequestsPerSecond = userCfg.RateLimit.MaxRequestsPerSecondForNamespace
		}
		if userCfg.LabelConfig != nil {
			merged.LabelConfig = *userCfg.LabelConfig
		}
		if userCfg.DiscoveryFilters != nil {
			merged.MatchFilters = userCfg.DiscoveryFilters
		}
		if len(userCfg.ResourceTypesToDiscover) > 0 {
			merged.ResourceTypesToDiscover = userCfg.ResourceTypesToDiscover
		}
		if userCfg.Retry != nil {
			merged.RetryConfig = userCfg.Retry
		}
	}

	return merged, true
}

// resolveRetryConfig returns per-plugin RetryConfig if set, otherwise the global fallback.
func (c *PluginCoordinator) resolveRetryConfig(namespace string) model.RetryConfig {
	if p, ok := c.findPluginByNamespace(namespace); ok && p.RetryConfig != nil {
		return *p.RetryConfig
	}
	return c.retryConfig
}

// NewPluginCoordinator creates a new PluginCoordinator actor
func NewPluginCoordinator() gen.ProcessBehavior {
	return &PluginCoordinator{}
}

func (c *PluginCoordinator) Init(args ...any) error {
	c.plugins = make(map[string]*RegisteredPlugin)
	c.registeredLocalNamespaces = make(map[string]bool)

	// Test-only: check for directly injected test plugin (e.g. FakeAWS for workflow tests)
	if tp, ok := c.Env("TestResourcePlugin"); ok {
		c.testPlugin = tp.(plugin.FullResourcePlugin)
	}

	// Register test plugin namespace with RateLimiter at startup
	// This is needed because ChangesetExecutor requests tokens before SpawnPluginOperator
	if c.testPlugin != nil {
		namespace := c.testPlugin.Namespace()
		maxRPS := c.testPlugin.RateLimit().MaxRequestsPerSecondForNamespace
		err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
			Namespace:            namespace,
			MaxRequestsPerSecond: maxRPS,
		})
		if err != nil {
			c.Log().Error("Failed to register test plugin namespace %s with RateLimiter: %v", namespace, err)
		} else {
			c.registeredLocalNamespaces[namespace] = true
			c.Log().Debug("Registered test plugin namespace %s with RateLimiter (maxRPS=%d)", namespace, maxRPS)
		}
	}

	retryCfg, ok := c.Env("RetryConfig")
	if !ok {
		c.Log().Error("ResourceUpdater: missing 'RetryConfig' environment variable")
		return fmt.Errorf("resourceUpdater: missing 'RetryConfig' environment variable")
	}
	c.retryConfig = retryCfg.(model.RetryConfig)

	if rpcs, ok := c.Env("ResourcePluginConfigs"); ok {
		configs := rpcs.([]model.ResourcePluginUserConfig)
		c.resourcePluginConfigs = make(map[string]model.ResourcePluginUserConfig, len(configs))
		for _, cfg := range configs {
			c.resourcePluginConfigs[strings.ToLower(cfg.Type)] = cfg
		}
	} else {
		c.resourcePluginConfigs = make(map[string]model.ResourcePluginUserConfig)
	}

	c.Log().Debug("PluginCoordinator started")
	return nil
}

func (c *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case messages.GetPluginNode:
		plugin, ok := c.findPluginByNamespace(req.Namespace)
		if !ok {
			return nil, fmt.Errorf("plugin not found: %s", req.Namespace)
		}
		return messages.PluginNode{NodeName: plugin.NodeName}, nil

	case messages.SpawnPluginOperator:
		result := c.spawnPluginOperator(req)
		return result, nil

	case messages.GetPluginInfo:
		return c.getPluginInfo(req), nil

	case messages.GetRegisteredPlugins:
		return c.getRegisteredPlugins(), nil

	default:
		return nil, fmt.Errorf("unknown request: %T", request)
	}
}

func (c *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case messages.PluginAnnouncement:
		caps := msg.Capabilities

		c.Log().Debug("Received capabilities for namespace %s: %d resources, %d schemas", msg.Namespace, len(caps.SupportedResources), len(caps.ResourceSchemas))

		announced := RegisteredPlugin{
			Namespace:            msg.Namespace,
			Version:              msg.Version,
			NodeName:             from.Node,
			MaxRequestsPerSecond: msg.MaxRequestsPerSecond,
			RegisteredAt:         time.Now(),
			SupportedResources:   caps.SupportedResources,
			ResourceSchemas:      caps.ResourceSchemas,
			MatchFilters:         caps.MatchFilters,
			LabelConfig:          caps.LabelConfig,
		}

		merged, enabled := c.mergePluginConfig(msg.Name, msg.Namespace, announced)
		if !enabled {
			return nil
		}

		c.plugins[msg.Namespace] = &merged
		c.Log().Info("Plugin registered: namespace=%s node=%s rateLimit=%d resources=%d",
			msg.Namespace, msg.NodeName, merged.MaxRequestsPerSecond, len(caps.SupportedResources))

		// Register the namespace with RateLimiter
		if err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
			Namespace:            msg.Namespace,
			MaxRequestsPerSecond: merged.MaxRequestsPerSecond,
		}); err != nil {
			c.Log().Error("Failed to register namespace %s with RateLimiter: %v", msg.Namespace, err)
		}

	case messages.UnregisterPlugin:
		if _, ok := c.plugins[msg.Namespace]; ok {
			delete(c.plugins, msg.Namespace)
			c.Log().Debug("Plugin unregistered: namespace=%s reason=%s", msg.Namespace, msg.Reason)
		}

	default:
		c.Log().Debug("Received unknown message type: %T", message)
	}
	return nil
}

// spawnPluginOperator spawns a PluginOperator for the given resource operation.
// It first tries to spawn remotely on a registered plugin node (distributed mode),
// then falls back to local spawning if a local plugin is available.
func (c *PluginCoordinator) spawnPluginOperator(req messages.SpawnPluginOperator) messages.SpawnPluginOperatorResult {
	// Generate a registered name for the operator
	registerName := actornames.PluginOperator(
		model.FormaeURI(req.ResourceURI),
		req.Operation,
		req.OperationID,
	)

	// 1. Check if plugin is registered (distributed mode)
	if registeredPlugin, ok := c.findPluginByNamespace(req.Namespace); ok {
		pid, err := c.remoteSpawn(req.Namespace, registeredPlugin.NodeName, registerName)
		if err != nil {
			c.Log().Error("Failed to remote spawn PluginOperator for namespace %s on node %s: %v", req.Namespace, registeredPlugin.NodeName, err)
			return messages.SpawnPluginOperatorResult{Error: err.Error()}
		}
		c.Log().Debug("Remote spawned PluginOperator for namespace %s on node %s: pid=%s", req.Namespace, registeredPlugin.NodeName, pid)
		return messages.SpawnPluginOperatorResult{PID: pid}
	}

	// 2. Fallback: Check test plugin / legacy PluginManager
	if localPlugin := c.findTestPlugin(req.Namespace); localPlugin != nil {
		// Register namespace with RateLimiter if not already registered
		if !c.registeredLocalNamespaces[req.Namespace] {
			maxRPS := localPlugin.RateLimit().MaxRequestsPerSecondForNamespace
			err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
				Namespace:            req.Namespace,
				MaxRequestsPerSecond: maxRPS,
			})
			if err != nil {
				c.Log().Error("Failed to register namespace %s with RateLimiter: %v", req.Namespace, err)
				// Don't fail the spawn - rate limiting is not critical
			} else {
				c.registeredLocalNamespaces[req.Namespace] = true
				c.Log().Debug("Registered local plugin namespace %s with RateLimiter (maxRPS=%d)", req.Namespace, maxRPS)
			}
		}

		pid, err := c.localSpawn(req.Namespace, localPlugin, registerName)
		if err != nil {
			c.Log().Error("Failed to local spawn PluginOperator for namespace %s: %v", req.Namespace, err)
			return messages.SpawnPluginOperatorResult{Error: err.Error()}
		}
		c.Log().Debug("Local spawned PluginOperator for namespace %s: pid=%s", req.Namespace, pid)
		return messages.SpawnPluginOperatorResult{PID: pid}
	}

	// 3. Plugin not found
	err := fmt.Errorf("plugin not found: %s", req.Namespace)
	c.Log().Error("Plugin not found for spawn: namespace=%s", req.Namespace)
	return messages.SpawnPluginOperatorResult{Error: err.Error()}
}

// remoteSpawn spawns a PluginOperator on a remote plugin node
func (c *PluginCoordinator) remoteSpawn(namespace string, nodeName gen.Atom, registerName gen.Atom) (gen.PID, error) {
	// Get connection to remote node
	remoteNode, err := c.Node().Network().GetNode(nodeName)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to get remote node %s: %w", nodeName, err)
	}

	// Remote spawn with registration
	// The remote node's environment already has Plugin, Context, and OTelConfig set
	// (configured in pkg/plugin/run.go)
	opts := gen.ProcessOptions{
		Env: map[gen.Env]any{
			gen.Env("RetryConfig"): c.resolveRetryConfig(namespace),
		},
	}
	start := time.Now()
	pid, err := remoteNode.SpawnRegister(registerName, plugin.PluginOperatorFactoryName, opts)
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		c.Log().Warning("PluginCoordinator: SLOW SpawnRegister on %s took %s (err=%v)", nodeName, elapsed, err)
	}
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to remote spawn on %s (took %s): %w", nodeName, elapsed, err)
	}

	return pid, nil
}

// localSpawn spawns a PluginOperator locally with the given plugin
func (c *PluginCoordinator) localSpawn(namespace string, localPlugin plugin.FullResourcePlugin, registerName gen.Atom) (gen.PID, error) {
	// Get context and retry config from environment
	ctx := context.Background()
	if envCtx, ok := c.Env("Context"); ok {
		ctx = envCtx.(context.Context)
	}

	// Spawn locally with plugin passed via Env
	opts := gen.ProcessOptions{
		Env: map[gen.Env]any{
			gen.Env("Plugin"):      localPlugin,
			gen.Env("Context"):     ctx,
			gen.Env("RetryConfig"): c.resolveRetryConfig(namespace),
		},
	}

	pid, err := c.SpawnRegister(registerName, plugin.NewPluginOperator, opts)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to local spawn: %w", err)
	}

	return pid, nil
}

// getPluginInfo retrieves plugin information for the given namespace.
// It first checks registered external plugins, then falls back to local plugins.
// Filters are cached from the initial plugin announcement (no refresh needed since filters are static).
func (c *PluginCoordinator) getPluginInfo(req messages.GetPluginInfo) messages.PluginInfoResponse {
	// 1. Check external plugins first
	if registered, ok := c.findPluginByNamespace(req.Namespace); ok {
		return messages.PluginInfoResponse{
			Found:                   true,
			Namespace:               req.Namespace,
			SupportedResources:      registered.SupportedResources,
			ResourceSchemas:         registered.ResourceSchemas,
			MatchFilters:            registered.MatchFilters,
			LabelConfig:             registered.LabelConfig,
			ResourceTypesToDiscover: registered.ResourceTypesToDiscover,
		}
	}

	// 2. Fall back to test plugin / legacy PluginManager
	localPlugin := c.findTestPlugin(req.Namespace)
	if localPlugin == nil {
		return messages.PluginInfoResponse{
			Found: false,
			Error: fmt.Sprintf("plugin not found: %s", req.Namespace),
		}
	}

	// Build schema map from local plugin
	schemas := make(map[string]model.Schema)
	for _, rd := range localPlugin.SupportedResources() {
		schema, err := localPlugin.SchemaForResourceType(rd.Type)
		if err == nil {
			schemas[rd.Type] = schema
		}
	}

	resp := messages.PluginInfoResponse{
		Found:              true,
		Namespace:          req.Namespace,
		SupportedResources: localPlugin.SupportedResources(),
		ResourceSchemas:    schemas,
		MatchFilters:       localPlugin.DiscoveryFilters(),
		LabelConfig:        localPlugin.LabelConfig(),
	}

	// Overlay user config for local/test plugins (lookup by name, not namespace)
	if userCfg, ok := c.resourcePluginConfigs[strings.ToLower(localPlugin.Name())]; ok {
		if userCfg.LabelConfig != nil {
			resp.LabelConfig = *userCfg.LabelConfig
		}
		if userCfg.DiscoveryFilters != nil {
			resp.MatchFilters = userCfg.DiscoveryFilters
		}
		if len(userCfg.ResourceTypesToDiscover) > 0 {
			resp.ResourceTypesToDiscover = userCfg.ResourceTypesToDiscover
		}
	}

	return resp
}

// getRegisteredPlugins returns a list of all registered plugins
func (c *PluginCoordinator) getRegisteredPlugins() messages.GetRegisteredPluginsResult {
	plugins := make([]messages.RegisteredPluginInfo, 0, len(c.plugins))

	for _, registered := range c.plugins {
		plugins = append(plugins, messages.RegisteredPluginInfo{
			Namespace:               registered.Namespace,
			Version:                 registered.Version,
			NodeName:                string(registered.NodeName),
			MaxRequestsPerSecond:    registered.MaxRequestsPerSecond,
			ResourceCount:           len(registered.SupportedResources),
			ResourceTypesToDiscover: registered.ResourceTypesToDiscover,
			RetryConfig:             registered.RetryConfig,
			LabelConfig:             registered.LabelConfig,
			DiscoveryFilters:        registered.MatchFilters,
		})
	}

	return messages.GetRegisteredPluginsResult{Plugins: plugins}
}
