// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

// PluginActor handles plugin lifecycle: announcement and heartbeats.
type PluginActor struct {
	act.Actor

	namespace string
	agentNode gen.Atom
}

func factoryPluginActor() gen.ProcessBehavior {
	return &PluginActor{}
}

func (p *PluginActor) Init(args ...any) error {
	// Get namespace from environment
	namespaceVal, ok := p.Env("Namespace")
	if !ok {
		return fmt.Errorf("PluginActor: missing 'Namespace' in environment")
	}
	p.namespace, ok = namespaceVal.(string)
	if !ok {
		return fmt.Errorf("PluginActor: 'Namespace' has wrong type")
	}

	// Get agent node from environment
	agentNodeVal, ok := p.Env("AgentNode")
	if !ok {
		return fmt.Errorf("PluginActor: missing 'AgentNode' in environment")
	}
	p.agentNode, ok = agentNodeVal.(gen.Atom)
	if !ok {
		return fmt.Errorf("PluginActor: 'AgentNode' has wrong type")
	}

	// Send announcement to PluginRegistry on agent node
	registryPID := gen.ProcessID{
		Name: "PluginRegistry",
		Node: p.agentNode,
	}
	announcement := PluginAnnouncement{
		Namespace: p.namespace,
	}

	if err := p.Send(registryPID, announcement); err != nil {
		p.Log().Error("Failed to send announcement", "error", err)
		return fmt.Errorf("failed to send announcement: %w", err)
	}

	p.Log().Info("Plugin announced", "namespace", p.namespace, "agent", p.agentNode)
	return nil
}

func (p *PluginActor) HandleMessage(from gen.PID, message any) error {
	// TODO: Handle heartbeat timer messages
	return nil
}

func (p *PluginActor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	return nil, fmt.Errorf("unknown request: %T", request)
}
