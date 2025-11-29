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
		MatchFilters:       p.plugin.GetMatchFilters(),
	}

	compressedCaps, err := CompressCapabilities(caps)
	if err != nil {
		p.Log().Error("Failed to compress capabilities", "error", err)
		return fmt.Errorf("failed to compress capabilities: %w", err)
	}

	p.Log().Info("Compressed capabilities",
		"resources", len(caps.SupportedResources),
		"schemas", len(caps.ResourceSchemas),
		"compressedSize", len(compressedCaps))

	// Send announcement to PluginCoordinator on agent node
	registryPID := gen.ProcessID{
		Name: "PluginCoordinator",
		Node: p.agentNode,
	}
	announcement := PluginAnnouncement{
		Namespace:            p.namespace,
		NodeName:             string(p.Node().Name()),
		MaxRequestsPerSecond: p.plugin.MaxRequestsPerSecond(),
		Capabilities:         compressedCaps,
	}

	if err := p.Send(registryPID, announcement); err != nil {
		p.Log().Error("Failed to send announcement", "error", err)
		return fmt.Errorf("failed to send announcement: %w", err)
	}

	p.Log().Info("Plugin announced", "namespace", p.namespace, "node", p.Node().Name(), "agent", p.agentNode)
	return nil
}

func (p *PluginActor) HandleMessage(from gen.PID, message any) error {
	// TODO: Handle heartbeat timer messages
	return nil
}

func (p *PluginActor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	return nil, fmt.Errorf("unknown request: %T", request)
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
