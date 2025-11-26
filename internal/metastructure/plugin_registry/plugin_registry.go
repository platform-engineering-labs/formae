// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_registry

import (
	"fmt"
	"time"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
)

// PluginRegistry maintains a registry of all available resource plugins (local and remote).
// Plugins announce themselves to the registry on startup.
type PluginRegistry struct {
	act.Actor

	plugins map[string]*RegisteredPlugin // namespace → plugin info
}

// RegisteredPlugin contains information about a registered plugin
type RegisteredPlugin struct {
	Namespace     string
	RegisteredAt  time.Time
}



// NewPluginRegistry creates a new PluginRegistry actor
func NewPluginRegistry() gen.ProcessBehavior {
	return &PluginRegistry{}
}

func (r *PluginRegistry) Init(args ...any) error {
	r.plugins = make(map[string]*RegisteredPlugin)
	r.Log().Info("PluginRegistry started")
	return nil
}

func (r *PluginRegistry) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	// TODO: Implement GetPluginOperator handling
	return nil, fmt.Errorf("unknown request: %T", request)
}

func (r *PluginRegistry) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case messages.PluginAnnouncement:
		r.plugins[msg.Namespace] = &RegisteredPlugin{
			Namespace:    msg.Namespace,
			RegisteredAt: time.Now(),
		}
		r.Log().Info("Plugin registered", "namespace", msg.Namespace)
	default:
		r.Log().Debug("Received unknown message", "type", fmt.Sprintf("%T", message))
	}
	return nil
}
