// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin_operation

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

type PluginOperatorSupervisor struct {
	act.Supervisor
}

func NewPluginOperatorSupervisor() gen.ProcessBehavior {
	return &PluginOperatorSupervisor{}
}

type EnsurePluginOperator struct {
	ResourceURI model.FormaeURI
	Operation   resource.Operation
	OperationID string
}

func (s *PluginOperatorSupervisor) Init(args ...any) (act.SupervisorSpec, error) {
	var spec act.SupervisorSpec

	spec.Type = act.SupervisorTypeOneForOne

	spec.Children = []act.SupervisorChildSpec{
		{
			Name:    "PluginOperator",
			Factory: newPluginOperator,
			Args:    []any{s.PID()},
		},
	}

	spec.Restart.Strategy = act.SupervisorStrategyTransient
	spec.Restart.Intensity = 2 // How big bursts of restarts you want to tolerate.
	spec.Restart.Period = 1    // In seconds

	return spec, nil
}

func (s *PluginOperatorSupervisor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case EnsurePluginOperator:
		err := s.ensurePluginOperator(from, req)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure PluginOperator for %s: %w", req.ResourceURI, err)
		}
		s.Log().Debug("PluginOperatorSupervisor ensured PluginOperator for %s", req.ResourceURI)
		return true, nil
	default:
		return nil, fmt.Errorf("pluginOperatorSupervisor received unknown request type %T", request)
	}
}

func (s *PluginOperatorSupervisor) ensurePluginOperator(from gen.PID, req EnsurePluginOperator) error {
	address := actornames.PluginOperator(req.ResourceURI, string(req.Operation), req.OperationID)
	s.Log().Debug("Ensuring PluginOperator for %s", address)
	err := s.AddChild(act.SupervisorChildSpec{
		Name:    gen.Atom(address),
		Factory: newPluginOperator,
		Args:    []any{from},
	})
	if err != nil && err != act.ErrSupervisorChildDuplicate {
		return fmt.Errorf("failed to start PluginOperator for %s: %w", address, err)
	}
	s.Log().Debug("PluginOperatorSupervisor ensured PluginOperator for %s", address)

	return nil
}
