// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
)

type TargetUpdaterSupervisor struct {
	act.Supervisor
}

func NewTargetUpdaterSupervisor() gen.ProcessBehavior {
	return &TargetUpdaterSupervisor{}
}

type EnsureTargetUpdater struct {
	Label     string
	Operation string
	CommandID string
}

func (s *TargetUpdaterSupervisor) Init(args ...any) (act.SupervisorSpec, error) {
	var spec act.SupervisorSpec

	spec.Type = act.SupervisorTypeOneForOne

	spec.Children = []act.SupervisorChildSpec{
		{
			Name:    "TargetUpdater",
			Factory: NewTargetUpdater,
			Args:    []any{s.PID()},
		},
	}
	spec.Restart.Strategy = act.SupervisorStrategyTransient
	spec.Restart.Intensity = 2 // How big bursts of restarts you want to tolerate.
	spec.Restart.Period = 1    // In seconds

	return spec, nil
}

func (s *TargetUpdaterSupervisor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case EnsureTargetUpdater:
		err := s.ensureTargetUpdater(from, req)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure TargetUpdater for %s: %w", req.Label, err)
		}
		s.Log().Debug("TargetUpdaterSupervisor ensured TargetUpdater for %s", req.Label)
		return true, nil
	default:
		return nil, fmt.Errorf("targetUpdaterSupervisor received unknown request type %T", request)
	}
}

func (s *TargetUpdaterSupervisor) ensureTargetUpdater(from gen.PID, req EnsureTargetUpdater) error {
	s.Log().Debug("ensuring TargetUpdater for %s", req.Label)
	err := s.AddChild(act.SupervisorChildSpec{
		Name:    actornames.TargetUpdater(req.Label, req.Operation, req.CommandID),
		Factory: NewTargetUpdater,
		Args:    []any{from},
	})
	if err != nil && err != act.ErrSupervisorChildDuplicate {
		return fmt.Errorf("failed to add TargetUpdater for %s: %w", req.Label, err)
	}

	return nil
}
