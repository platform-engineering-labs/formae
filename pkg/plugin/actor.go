// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

// setupMonitoring is sent to self after Init completes to defer monitoring setup.
// MonitorProcessID requires the process to be in Running state, not Init state.
type setupMonitoring struct{}

// PluginActor handles plugin lifecycle: announcement and heartbeats.
type PluginActor struct {
	act.Actor

	namespace string
	agentNode gen.Atom
	plugin    ResourcePlugin
}

func factoryPluginActor() gen.ProcessBehavior {
	return &PluginActor{}
}

func (p *PluginActor) Init(args ...any) error {
	// Get plugin from environment
	pluginVal, ok := p.Env("Plugin")
	if !ok {
		return fmt.Errorf("PluginActor: missing 'Plugin' in environment")
	}
	p.plugin, ok = pluginVal.(ResourcePlugin)
	if !ok {
		return fmt.Errorf("PluginActor: 'Plugin' has wrong type")
	}

	// Get namespace from plugin
	p.namespace = p.plugin.Namespace()

	// Get agent node from environment
	agentNodeVal, ok := p.Env("AgentNode")
	if !ok {
		return fmt.Errorf("PluginActor: missing 'AgentNode' in environment")
	}
	p.agentNode, ok = agentNodeVal.(gen.Atom)
	if !ok {
		return fmt.Errorf("PluginActor: 'AgentNode' has wrong type")
	}

	// Build and compress capabilities
	caps := PluginCapabilities{
		SupportedResources: p.plugin.SupportedResources(),
		ResourceSchemas:    buildSchemaMap(p.plugin),
		MatchFilters:       p.plugin.DiscoveryFilters(),
	}

	compressedCaps, err := CompressCapabilities(caps)
	if err != nil {
		p.Log().Error("Failed to compress capabilities: %s", err)
		return fmt.Errorf("failed to compress capabilities: %w", err)
	}

	p.Log().Debug("Compressed capabilities: %d resources, %d schemas, %d bytes compressed", len(caps.SupportedResources), len(caps.ResourceSchemas), len(compressedCaps))

	// Send announcement to PluginCoordinator on agent node
	registryPID := gen.ProcessID{
		Name: "PluginCoordinator",
		Node: p.agentNode,
	}
	announcement := PluginAnnouncement{
		Namespace:            p.namespace,
		NodeName:             string(p.Node().Name()),
		MaxRequestsPerSecond: p.plugin.RateLimit().MaxRequestsPerSecondForNamespace,
		Capabilities:         compressedCaps,
	}

	if err := p.Send(registryPID, announcement); err != nil {
		p.Log().Error("Failed to send announcement: %s", err)
		return fmt.Errorf("failed to send announcement: %w", err)
	}

	p.Log().Debug("Announcement sent for %s plugin from %s to %s", p.namespace, p.Node().Name(), p.agentNode)

	// Schedule monitoring setup after Init completes.
	// MonitorProcessID requires the process to be in Running state, not Init state.
	if err := p.Send(p.PID(), setupMonitoring{}); err != nil {
		p.Log().Error("Failed to schedule monitoring setup: %s", err)
	}

	return nil
}

func (p *PluginActor) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case setupMonitoring:
		// Now we're in Running state, MonitorProcessID is allowed
		registryPID := gen.ProcessID{Name: "PluginCoordinator", Node: p.agentNode}
		if err := p.MonitorProcessID(registryPID); err != nil {
			p.Log().Error("Failed to monitor PluginCoordinator: %s", err)
		} else {
			p.Log().Debug("Monitoring PluginCoordinator at %s", registryPID)
		}

	case gen.MessageDownProcessID:
		// PluginCoordinator terminated - shut down the plugin gracefully
		p.Log().Error("PluginCoordinator down (processID: %s, reason: %v), shutting down plugin", msg.ProcessID, msg.Reason)
		return fmt.Errorf("PluginCoordinator unavailable: %v", msg.Reason)

	case gen.MessageDownNode:
		// Agent node went down - shut down the plugin gracefully
		if msg.Name == p.agentNode {
			p.Log().Error("Agent node %s down, shutting down plugin", msg.Name)
			return fmt.Errorf("agent node unavailable: %s", msg.Name)
		}
	}
	return nil
}

func (p *PluginActor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch request.(type) {
	case GetFilters:
		filters := p.plugin.DiscoveryFilters()
		return GetFiltersResponse{Filters: filters}, nil

	default:
		return nil, fmt.Errorf("unknown request: %T", request)
	}
}

// buildSchemaMap creates a map of resource type to schema for all supported resources
func buildSchemaMap(plugin ResourcePlugin) map[string]model.Schema {
	schemas := make(map[string]model.Schema)
	for _, rd := range plugin.SupportedResources() {
		schema, err := plugin.SchemaForResourceType(rd.Type)
		if err == nil {
			schemas[rd.Type] = schema
		}
	}
	return schemas
}
