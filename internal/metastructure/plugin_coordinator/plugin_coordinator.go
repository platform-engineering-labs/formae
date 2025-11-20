package plugin_coordinator

import (
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

type PluginCoordinator struct {
	act.Actor

	plugins map[string]*PluginInfo
}

type PluginInfo struct {
	namespace   string
	metaPortPID gen.PID
	nodeName    string
	healthy     bool
}

// NewPluginCoordinator creates a new PluginCoordinator actor
func NewPluginCoordinator() gen.ProcessBehavior {
	return &PluginCoordinator{}
}

func (p *PluginCoordinator) Init(args ...any) error {
	p.plugins = make(map[string]*PluginInfo)
	p.Log().Info("PluginCoordinator started")
	return nil
}

func (p *PluginCoordinator) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	// TODO: Implement message handling in later tasks
	return nil, nil
}

func (p *PluginCoordinator) HandleMessage(from gen.PID, message any) error {
	// TODO: Handle meta.Port messages in later tasks
	return nil
}
