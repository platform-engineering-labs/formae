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

	plugins                   *namespaceRegistry        // namespace → plugin info (remote), case-insensitive
	registeredLocalNamespaces *namespaceSet             // namespaces registered with RateLimiter (local), case-insensitive
	testPlugin                plugin.FullResourcePlugin // test-only: directly injected plugin (e.g. FakeAWS) for workflow tests
	retryConfig               model.RetryConfig
	resourcePluginConfigs     map[string]model.ResourcePluginUserConfig // keyed by plugin name (lowercase)
}

// namespaceRegistry is a case-insensitive map keyed by plugin namespace.
// Keys are normalized to lowercase on Set; Get/Delete lowercase their input.
// The stored RegisteredPlugin.Namespace field keeps the announcement's original
// casing for display; only the map key is canonical.
type namespaceRegistry struct {
	m map[string]*RegisteredPlugin
}

func newNamespaceRegistry() *namespaceRegistry {
	return &namespaceRegistry{m: make(map[string]*RegisteredPlugin)}
}

func (r *namespaceRegistry) Set(ns string, p *RegisteredPlugin) {
	r.m[strings.ToLower(ns)] = p
}

func (r *namespaceRegistry) Get(ns string) (*RegisteredPlugin, bool) {
	p, ok := r.m[strings.ToLower(ns)]
	return p, ok
}

func (r *namespaceRegistry) Delete(ns string) {
	delete(r.m, strings.ToLower(ns))
}

// All returns the underlying map for iteration. Callers must not mutate it.
func (r *namespaceRegistry) All() map[string]*RegisteredPlugin {
	return r.m
}

// namespaceSet is a case-insensitive set of namespaces.
type namespaceSet struct {
	m map[string]struct{}
}

func newNamespaceSet() *namespaceSet {
	return &namespaceSet{m: make(map[string]struct{})}
}

func (s *namespaceSet) Has(ns string) bool {
	_, ok := s.m[strings.ToLower(ns)]
	return ok
}

func (s *namespaceSet) Add(ns string) {
	s.m[strings.ToLower(ns)] = struct{}{}
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
		// DiscoveryFilters: `!= nil` - explicit empty (user wrote `discoveryFilters { }`)
		// means "no filters, don't exclude anything," distinct from the plugin default.
		if userCfg.DiscoveryFilters != nil {
			merged.MatchFilters = userCfg.DiscoveryFilters
		}
		// ResourceTypesToDiscover: `len > 0` - empty and absent both mean "discover all"
		// at the discovery layer (see discovery.go handling of a zero-length list), so
		// explicit empty has no distinct meaning to preserve here.
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
	if p, ok := c.plugins.Get(namespace); ok && p.RetryConfig != nil {
		return *p.RetryConfig
	}
	return c.retryConfig
}

// NewPluginCoordinator creates a new PluginCoordinator actor
func NewPluginCoordinator() gen.ProcessBehavior {
	return &PluginCoordinator{}
}

func (c *PluginCoordinator) Init(args ...any) error {
	c.plugins = newNamespaceRegistry()
	c.registeredLocalNamespaces = newNamespaceSet()

	// Test-only: check for directly injected test plugin (e.g. FakeAWS for workflow tests)
	if tp, ok := c.Env("TestResourcePlugin"); ok {
		c.testPlugin = tp.(plugin.FullResourcePlugin)
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

	// Register the test plugin through the same merge path that external plugins
	// use at announcement time, so read paths (getPluginInfo, resolveRetryConfig)
	// see identical merged config regardless of whether the plugin is local or remote.
	// The NodeName is left empty to signal "local" to spawnPluginOperator.
	if c.testPlugin != nil {
		c.registerTestPlugin()
	}

	c.warnUnclaimedPluginConfigs()

	c.Log().Debug("PluginCoordinator started")
	return nil
}

// registerTestPlugin builds a synthetic RegisteredPlugin from the injected test
// plugin, runs it through mergePluginConfig to apply any user overrides, stores
// it in c.plugins, and registers the namespace with the RateLimiter using the
// merged RPS. This unifies config handling: all read paths go through c.plugins
// whether the plugin is remote (announced) or local (test-injected).
func (c *PluginCoordinator) registerTestPlugin() {
	namespace := c.testPlugin.Namespace()

	schemas := make(map[string]model.Schema)
	for _, rd := range c.testPlugin.SupportedResources() {
		if schema, err := c.testPlugin.SchemaForResourceType(rd.Type); err == nil {
			schemas[rd.Type] = schema
		}
	}

	announced := RegisteredPlugin{
		Namespace:            namespace,
		MaxRequestsPerSecond: c.testPlugin.RateLimit().MaxRequestsPerSecondForNamespace,
		RegisteredAt:         time.Now(),
		SupportedResources:   c.testPlugin.SupportedResources(),
		ResourceSchemas:      schemas,
		MatchFilters:         c.testPlugin.DiscoveryFilters(),
		LabelConfig:          c.testPlugin.LabelConfig(),
		// NodeName intentionally empty - signals "local" to spawnPluginOperator.
	}

	merged, enabled := c.mergePluginConfig(c.testPlugin.Name(), namespace, announced)
	if !enabled {
		c.Log().Info("Test plugin disabled by config, skipping registration: namespace=%s", namespace)
		return
	}
	c.plugins.Set(namespace, &merged)

	// Register namespace with RateLimiter using the merged RPS so per-plugin
	// rate-limit overrides apply to test plugins too. Required because
	// ChangesetExecutor requests tokens before SpawnPluginOperator.
	if err := c.Send(actornames.RateLimiter, changeset.RegisterNamespace{
		Namespace:            namespace,
		MaxRequestsPerSecond: merged.MaxRequestsPerSecond,
	}); err != nil {
		c.Log().Error("Failed to register test plugin namespace %s with RateLimiter: %v", namespace, err)
		return
	}
	c.registeredLocalNamespaces.Add(namespace)
	c.Log().Debug("Registered test plugin namespace %s with RateLimiter (maxRPS=%d)", namespace, merged.MaxRequestsPerSecond)
}

// warnUnclaimedPluginConfigs emits a warning for each user-configured plugin
// whose `type` does not match any installed plugin's name (case-insensitive).
// The install list comes from the shared ExternalResourcePlugins env var; a
// configured type with no matching installed plugin is silently inert
// otherwise, which is a common misconfiguration trap.
func (c *PluginCoordinator) warnUnclaimedPluginConfigs() {
	if len(c.resourcePluginConfigs) == 0 {
		return
	}

	installed := make(map[string]struct{})
	if val, ok := c.Env("ExternalResourcePlugins"); ok {
		if plugins, ok := val.([]plugin.ResourcePluginInfo); ok {
			for _, p := range plugins {
				installed[strings.ToLower(p.Name)] = struct{}{}
			}
		}
	}
	if c.testPlugin != nil {
		installed[strings.ToLower(c.testPlugin.Name())] = struct{}{}
	}

	for key, cfg := range c.resourcePluginConfigs {
		if _, ok := installed[key]; !ok {
			c.Log().Warning("Resource plugin config references unknown plugin - config will be ignored: type=%s", cfg.Type)
		}
	}
}

func (c *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case messages.GetPluginNode:
		plugin, ok := c.plugins.Get(req.Namespace)
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

		c.plugins.Set(msg.Namespace, &merged)
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
		if _, ok := c.plugins.Get(msg.Namespace); ok {
			c.plugins.Delete(msg.Namespace)
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

	// 1. Check if plugin is registered. NodeName distinguishes remote (announced
	//    from a plugin process) from local (test-injected). Registration itself
	//    is unified - see registerTestPlugin.
	if registeredPlugin, ok := c.plugins.Get(req.Namespace); ok && registeredPlugin.NodeName != "" {
		pid, err := c.remoteSpawn(req.Namespace, registeredPlugin.NodeName, registerName)
		if err != nil {
			c.Log().Error("Failed to remote spawn PluginOperator for namespace %s on node %s: %v", req.Namespace, registeredPlugin.NodeName, err)
			return messages.SpawnPluginOperatorResult{Error: err.Error()}
		}
		c.Log().Debug("Remote spawned PluginOperator for namespace %s on node %s: pid=%s", req.Namespace, registeredPlugin.NodeName, pid)
		return messages.SpawnPluginOperatorResult{PID: pid}
	}

	// 2. Fall back to the locally-injected test plugin.
	if localPlugin := c.findTestPlugin(req.Namespace); localPlugin != nil {
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
// Both remote plugins (announced) and local test plugins (registered at Init)
// live in c.plugins with their user-config overrides already merged in, so the
// lookup is a single path.
func (c *PluginCoordinator) getPluginInfo(req messages.GetPluginInfo) messages.PluginInfoResponse {
	registered, ok := c.plugins.Get(req.Namespace)
	if !ok {
		return messages.PluginInfoResponse{
			Found: false,
			Error: fmt.Sprintf("plugin not found: %s", req.Namespace),
		}
	}
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

// getRegisteredPlugins returns a list of all registered plugins
func (c *PluginCoordinator) getRegisteredPlugins() messages.GetRegisteredPluginsResult {
	all := c.plugins.All()
	plugins := make([]messages.RegisteredPluginInfo, 0, len(all))

	for _, registered := range all {
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
