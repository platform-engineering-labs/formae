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
}

// RegisteredPlugin contains information about a registered plugin
type RegisteredPlugin struct {
	Namespace            string
	NodeName             gen.Atom // Ergo node where plugin runs (for remote spawn)
	MaxRequestsPerSecond int
	RegisteredAt         time.Time
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
				c.Log().Error("Failed to register local plugin namespace with RateLimiter",
					"namespace", namespace,
					"error", err)
			} else {
				c.registeredLocalNamespaces[namespace] = true
				c.Log().Info("Registered local plugin namespace %s with RateLimiter (maxRPS=%d)", namespace, maxRPS)
			}
		}
	}

	c.Log().Info("PluginCoordinator started")
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

	default:
		return nil, fmt.Errorf("unknown request: %T", request)
	}
}

func (c *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case messages.PluginAnnouncement:
		c.plugins[msg.Namespace] = &RegisteredPlugin{
			Namespace:            msg.Namespace,
			NodeName:             gen.Atom(msg.NodeName),
			MaxRequestsPerSecond: msg.MaxRequestsPerSecond,
			RegisteredAt:         time.Now(),
		}
		c.Log().Info("Plugin registered: namespace=%s node=%s rateLimit=%d", msg.Namespace, msg.NodeName, msg.MaxRequestsPerSecond)

		// Register the namespace with RateLimiter
		err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
			Namespace:            msg.Namespace,
			MaxRequestsPerSecond: msg.MaxRequestsPerSecond,
		})
		if err != nil {
			c.Log().Error("Failed to register namespace with RateLimiter", "namespace", msg.Namespace, "error", err)
		}
	default:
		c.Log().Debug("Received unknown message", "type", fmt.Sprintf("%T", message))
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
		pid, err := c.remoteSpawn(registeredPlugin.NodeName, registerName, req.RequestedBy)
		if err != nil {
			c.Log().Error("Failed to remote spawn PluginOperator",
				"namespace", req.Namespace,
				"node", registeredPlugin.NodeName,
				"error", err)
			return messages.SpawnPluginOperatorResult{Error: err.Error()}
		}
		c.Log().Debug("Remote spawned PluginOperator",
			"namespace", req.Namespace,
			"node", registeredPlugin.NodeName,
			"pid", pid)
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
					c.Log().Error("Failed to register namespace with RateLimiter",
						"namespace", req.Namespace,
						"error", err)
					// Don't fail the spawn - rate limiting is not critical
				} else {
					c.registeredLocalNamespaces[req.Namespace] = true
					c.Log().Info("Registered local plugin namespace with RateLimiter",
						"namespace", req.Namespace,
						"maxRequestsPerSecond", (*localPlugin).MaxRequestsPerSecond())
				}
			}

			pid, err := c.localSpawn(*localPlugin, registerName, req.RequestedBy)
			if err != nil {
				c.Log().Error("Failed to local spawn PluginOperator",
					"namespace", req.Namespace,
					"error", err)
				return messages.SpawnPluginOperatorResult{Error: err.Error()}
			}
			c.Log().Debug("Local spawned PluginOperator",
				"namespace", req.Namespace,
				"pid", pid)
			return messages.SpawnPluginOperatorResult{PID: pid}
		}
	}

	// 3. Plugin not found
	err := fmt.Errorf("plugin not found: %s", req.Namespace)
	c.Log().Error("Plugin not found for spawn", "namespace", req.Namespace)
	return messages.SpawnPluginOperatorResult{Error: err.Error()}
}

// remoteSpawn spawns a PluginOperator on a remote plugin node
func (c *PluginCoordinator) remoteSpawn(nodeName gen.Atom, registerName gen.Atom, requestedBy gen.PID) (gen.PID, error) {
	// Get connection to remote node
	remoteNode, err := c.Node().Network().GetNode(nodeName)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to get remote node %s: %w", nodeName, err)
	}

	// Remote spawn with registration
	// The remote node's environment already has Plugin, Context, and RetryConfig set
	// (configured in pkg/plugin/run.go)
	pid, err := remoteNode.SpawnRegister(registerName, plugin.PluginOperatorFactoryName, gen.ProcessOptions{}, requestedBy)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to remote spawn on %s: %w", nodeName, err)
	}

	return pid, nil
}

// localSpawn spawns a PluginOperator locally with the given plugin
func (c *PluginCoordinator) localSpawn(localPlugin plugin.ResourcePlugin, registerName gen.Atom, requestedBy gen.PID) (gen.PID, error) {
	// Get context and retry config from environment
	ctx := context.Background()
	if envCtx, ok := c.Env("Context"); ok {
		ctx = envCtx.(context.Context)
	}

	retryConfig := model.RetryConfig{
		StatusCheckInterval: 5 * time.Second,
		MaxRetries:          3,
		RetryDelay:          2 * time.Second,
	}
	if envConfig, ok := c.Env("RetryConfig"); ok {
		retryConfig = envConfig.(model.RetryConfig)
	}

	// Spawn locally with plugin passed via Env
	opts := gen.ProcessOptions{
		Env: map[gen.Env]any{
			gen.Env("Plugin"):      localPlugin,
			gen.Env("Context"):     ctx,
			gen.Env("RetryConfig"): retryConfig,
		},
	}

	pid, err := c.SpawnRegister(registerName, plugin.NewPluginOperator, opts, requestedBy)
	if err != nil {
		return gen.PID{}, fmt.Errorf("failed to local spawn: %w", err)
	}

	return pid, nil
}
