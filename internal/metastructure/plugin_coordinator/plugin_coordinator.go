// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_coordinator

import (
	"context"
	"fmt"
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
	pluginManager             *plugin.Manager              // for local plugin fallback
	retryConfig               model.RetryConfig
}

// RegisteredPlugin contains information about a registered plugin
type RegisteredPlugin struct {
	Namespace            string
	NodeName             gen.Atom // Ergo node where plugin runs (for remote spawn)
	MaxRequestsPerSecond int
	RegisteredAt         time.Time

	// Cached capabilities from announcement
	SupportedResources []plugin.ResourceDescriptor
	ResourceSchemas    map[string]model.Schema
	MatchFilters       []plugin.MatchFilter
}

// NewPluginCoordinator creates a new PluginCoordinator actor
func NewPluginCoordinator() gen.ProcessBehavior {
	return &PluginCoordinator{}
}

func (c *PluginCoordinator) Init(args ...any) error {
	c.plugins = make(map[string]*RegisteredPlugin)
	c.registeredLocalNamespaces = make(map[string]bool)

	// Get PluginManager from environment (for local plugin fallback)
	if pm, ok := c.Env("PluginManager"); ok {
		c.pluginManager = pm.(*plugin.Manager)
	}

	// Register all local plugin namespaces with RateLimiter at startup
	// This is needed because ChangesetExecutor requests tokens before SpawnPluginOperator
	if c.pluginManager != nil {
		for _, rp := range c.pluginManager.ListResourcePlugins() {
			namespace := (*rp).Namespace()
			maxRPS := (*rp).MaxRequestsPerSecond()
			err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
				Namespace:            namespace,
				MaxRequestsPerSecond: maxRPS,
			})
			if err != nil {
				c.Log().Error("Failed to register local plugin namespace %s with RateLimiter: %v", namespace, err)
			} else {
				c.registeredLocalNamespaces[namespace] = true
				c.Log().Debug("Registered local plugin namespace %s with RateLimiter (maxRPS=%d)", namespace, maxRPS)
			}
		}
	}

	retryCfg, ok := c.Env("RetryConfig")
	if !ok {
		c.Log().Error("ResourceUpdater: missing 'RetryConfig' environment variable")
		return fmt.Errorf("resourceUpdater: missing 'RetryConfig' environment variable")
	}
	c.retryConfig = retryCfg.(model.RetryConfig)

	c.Log().Debug("PluginCoordinator started")
	return nil
}

func (c *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case messages.GetPluginNode:
		plugin, ok := c.plugins[req.Namespace]
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
		// Decompress capabilities from the announcement
		caps, err := plugin.DecompressCapabilities(msg.Capabilities)
		if err != nil {
			c.Log().Error("Failed to decompress capabilities for namespace %s: %v", msg.Namespace, err)
			return fmt.Errorf("failed to decompress capabilities: %w", err)
		}

		c.Log().Debug("Decompressed capabilities for namespace %s: %d resources, %d schemas, %d bytes compressed", msg.Namespace, len(caps.SupportedResources), len(caps.ResourceSchemas), len(msg.Capabilities))

		c.plugins[msg.Namespace] = &RegisteredPlugin{
			Namespace:            msg.Namespace,
			NodeName:             from.Node,
			MaxRequestsPerSecond: msg.MaxRequestsPerSecond,
			RegisteredAt:         time.Now(),
			// Store decompressed capabilities
			SupportedResources: caps.SupportedResources,
			ResourceSchemas:    caps.ResourceSchemas,
			MatchFilters:       caps.MatchFilters,
		}
		c.Log().Info("Plugin registered: namespace=%s node=%s rateLimit=%d resources=%d",
			msg.Namespace, msg.NodeName, msg.MaxRequestsPerSecond, len(caps.SupportedResources))

		// Register the namespace with RateLimiter
		err = c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
			Namespace:            msg.Namespace,
			MaxRequestsPerSecond: msg.MaxRequestsPerSecond,
		})
		if err != nil {
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
	if registeredPlugin, ok := c.plugins[req.Namespace]; ok {
		pid, err := c.remoteSpawn(registeredPlugin.NodeName, registerName)
		if err != nil {
			c.Log().Error("Failed to remote spawn PluginOperator for namespace %s on node %s: %v", req.Namespace, registeredPlugin.NodeName, err)
			return messages.SpawnPluginOperatorResult{Error: err.Error()}
		}
		c.Log().Debug("Remote spawned PluginOperator for namespace %s on node %s: pid=%s", req.Namespace, registeredPlugin.NodeName, pid)
		return messages.SpawnPluginOperatorResult{PID: pid}
	}

	// 2. Fallback: Check if plugin exists locally via PluginManager
	if c.pluginManager != nil {
		// Debug: list available plugins
		availablePlugins := c.pluginManager.ListResourcePlugins()
		c.Log().Debug("Available resource plugins: %d", len(availablePlugins))
		for _, rp := range availablePlugins {
			c.Log().Debug("  - Plugin namespace: %s", (*rp).Namespace())
		}

		localPlugin, err := c.pluginManager.ResourcePlugin(req.Namespace)
		if err != nil {
			c.Log().Error("No plugin found for namespace: %s (error: %v)", req.Namespace, err)
		}
		if err == nil && localPlugin != nil {
			// Register namespace with RateLimiter if not already registered
			if !c.registeredLocalNamespaces[req.Namespace] {
				err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
					Namespace:            req.Namespace,
					MaxRequestsPerSecond: (*localPlugin).MaxRequestsPerSecond(),
				})
				if err != nil {
					c.Log().Error("Failed to register namespace %s with RateLimiter: %v", req.Namespace, err)
					// Don't fail the spawn - rate limiting is not critical
				} else {
					c.registeredLocalNamespaces[req.Namespace] = true
					c.Log().Debug("Registered local plugin namespace %s with RateLimiter (maxRPS=%d)", req.Namespace, (*localPlugin).MaxRequestsPerSecond())
				}
			}

			pid, err := c.localSpawn(*localPlugin, registerName)
			if err != nil {
				c.Log().Error("Failed to local spawn PluginOperator for namespace %s: %v", req.Namespace, err)
				return messages.SpawnPluginOperatorResult{Error: err.Error()}
			}
			c.Log().Debug("Local spawned PluginOperator for namespace %s: pid=%s", req.Namespace, pid)
			return messages.SpawnPluginOperatorResult{PID: pid}
		}
	}

	// 3. Plugin not found
	err := fmt.Errorf("plugin not found: %s", req.Namespace)
	c.Log().Error("Plugin not found for spawn: namespace=%s", req.Namespace)
	return messages.SpawnPluginOperatorResult{Error: err.Error()}
}

// remoteSpawn spawns a PluginOperator on a remote plugin node
func (c *PluginCoordinator) remoteSpawn(nodeName gen.Atom, registerName gen.Atom) (gen.PID, error) {
	// Get connection to remote node
	remoteNode, err := c.Node().Network().GetNode(nodeName)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to get remote node %s: %w", nodeName, err)
	}

	// Remote spawn with registration
	// The remote node's environment already has Plugin and Context set
	// (configured in pkg/plugin/run.go)
	opts := gen.ProcessOptions{
		Env: map[gen.Env]any{
			gen.Env("RetryConfig"): c.retryConfig,
		},
	}
	pid, err := remoteNode.SpawnRegister(registerName, plugin.PluginOperatorFactoryName, opts)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to remote spawn on %s: %w", nodeName, err)
	}

	return pid, nil
}

// localSpawn spawns a PluginOperator locally with the given plugin
func (c *PluginCoordinator) localSpawn(localPlugin plugin.ResourcePlugin, registerName gen.Atom) (gen.PID, error) {
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
			gen.Env("RetryConfig"): c.retryConfig,
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
func (c *PluginCoordinator) getPluginInfo(req messages.GetPluginInfo) messages.PluginInfoResponse {
	// 1. Check external plugins first
	if registered, ok := c.plugins[req.Namespace]; ok {
		filters := registered.MatchFilters

		// If RefreshFilters is requested, call the remote PluginActor
		if req.RefreshFilters {
			pluginActorPID := gen.ProcessID{
				Name: "PluginActor",
				Node: registered.NodeName,
			}

			resp, err := c.Call(pluginActorPID, messages.GetFilters{})
			if err != nil {
				c.Log().Error("Failed to refresh filters from remote plugin %s on node %s, using cached: %v", req.Namespace, registered.NodeName, err)
			} else if filterResp, ok := resp.(messages.GetFiltersResponse); ok {
				if filterResp.Error != "" {
					c.Log().Error("Remote plugin %s returned error for GetFilters, using cached: %s", req.Namespace, filterResp.Error)
				} else {
					c.Log().Debug("Refreshed filters from remote plugin %s: %d filters", req.Namespace, len(filterResp.Filters))
					filters = filterResp.Filters
					// Update cache
					registered.MatchFilters = filters
				}
			} else {
				c.Log().Error("Unexpected response type %T from GetFilters for namespace %s, using cached", resp, req.Namespace)
			}
		}

		return messages.PluginInfoResponse{
			Found:              true,
			Namespace:          req.Namespace,
			SupportedResources: registered.SupportedResources,
			ResourceSchemas:    registered.ResourceSchemas,
			MatchFilters:       filters,
		}
	}

	// 2. Fall back to local plugins
	if c.pluginManager == nil {
		return messages.PluginInfoResponse{
			Found: false,
			Error: fmt.Sprintf("plugin not found: %s", req.Namespace),
		}
	}

	resourcePlugin, err := c.pluginManager.ResourcePlugin(req.Namespace)
	if err != nil {
		return messages.PluginInfoResponse{
			Found: false,
			Error: fmt.Sprintf("plugin not found: %s", req.Namespace),
		}
	}

	rp := *resourcePlugin

	// Build schema map from local plugin
	schemas := make(map[string]model.Schema)
	for _, rd := range rp.SupportedResources() {
		schema, err := rp.SchemaForResourceType(rd.Type)
		if err == nil {
			schemas[rd.Type] = schema
		}
	}

	return messages.PluginInfoResponse{
		Found:              true,
		Namespace:          req.Namespace,
		SupportedResources: rp.SupportedResources(),
		ResourceSchemas:    schemas,
		MatchFilters:       rp.GetMatchFilters(),
	}
}

// getRegisteredPlugins returns a list of all registered plugins
func (c *PluginCoordinator) getRegisteredPlugins() messages.GetRegisteredPluginsResult {
	plugins := make([]messages.RegisteredPluginInfo, 0, len(c.plugins))

	for _, registered := range c.plugins {
		plugins = append(plugins, messages.RegisteredPluginInfo{
			Namespace:            registered.Namespace,
			NodeName:             string(registered.NodeName),
			MaxRequestsPerSecond: registered.MaxRequestsPerSecond,
			ResourceCount:        len(registered.SupportedResources),
		})
	}

	return messages.GetRegisteredPluginsResult{Plugins: plugins}
}
