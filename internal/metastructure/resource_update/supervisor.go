// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

type ResourceUpdaterSupervisor struct {
	act.Supervisor
}

func NewResourceUpdaterSupervisor() gen.ProcessBehavior {
	return &ResourceUpdaterSupervisor{}
}

type EnsureResourceUpdater struct {
	ResourceURI model.FormaeURI
	Operation   string
	CommandID   string
}

func (s *ResourceUpdaterSupervisor) Init(args ...any) (act.SupervisorSpec, error) {
	var spec act.SupervisorSpec

	spec.Type = act.SupervisorTypeOneForOne

	spec.Children = []act.SupervisorChildSpec{
		{
			Name:    "ResourceUpdater",
			Factory: newResourceUpdater,
			Args:    []any{s.PID()},
		},
	}
	spec.Restart.Strategy = act.SupervisorStrategyTransient
	spec.Restart.Intensity = 2 // How big bursts of restarts you want to tolerate.
	spec.Restart.Period = 1    // In seconds

	return spec, nil
}

func (s *ResourceUpdaterSupervisor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case EnsureResourceUpdater:
		err := s.ensureResourceUpdater(from, req)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure ResourceUpdater for %s: %w", req.ResourceURI, err)
		}
		s.Log().Debug("ResourceUpdaterSupervisor ensured ResourceUpdater for %s", req.ResourceURI)
		return true, nil
	default:
		return nil, fmt.Errorf("resourceUpdaterSupervisor received unknown request type %T", request)
	}
}

func (s *ResourceUpdaterSupervisor) ensureResourceUpdater(from gen.PID, req EnsureResourceUpdater) error {
	s.Log().Debug("ensuring ResourceUpdater for %s", req.ResourceURI)
	err := s.AddChild(act.SupervisorChildSpec{
		Name:    actornames.ResourceUpdater(req.ResourceURI, req.Operation, req.CommandID),
		Factory: newResourceUpdater,
		Args:    []any{from},
	})
	if err != nil && err != act.ErrSupervisorChildDuplicate {
		return fmt.Errorf("failed to add ResourceUpdater for %s: %w", req.ResourceURI, err)
	}

	return nil
}
