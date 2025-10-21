// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
)

type ChangesetSupervisor struct {
	act.Supervisor
}

func NewChangesetSupervisor() gen.ProcessBehavior {
	return &ChangesetSupervisor{}
}

type EnsureChangesetExecutor struct {
	CommandID string
}

type EnsureResolveCache struct {
	CommandID string
}

func (s *ChangesetSupervisor) Init(args ...any) (act.SupervisorSpec, error) {
	var spec act.SupervisorSpec
	spec.Type = act.SupervisorTypeOneForOne
	spec.Children = []act.SupervisorChildSpec{
		{
			Name:    "DummyResolveCache",
			Factory: NewResolveCache,
			Args:    []any{s.PID()},
		},
		{
			Name:    "DummyChangesetExecutor",
			Factory: NewChangesetExecutor,
			Args:    []any{s.PID()},
		},
	}
	spec.Restart.Strategy = act.SupervisorStrategyTransient
	spec.Restart.Intensity = 2 // How big bursts of restarts you want to tolerate.
	spec.Restart.Period = 1    // In seconds

	return spec, nil
}

func (s *ChangesetSupervisor) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case EnsureChangesetExecutor:
		err := s.ensureChangesetExecutor(from, msg)
		if err != nil {
			s.Log().Error("Failed to ensure ChangesetExecutor", "commandID", msg.CommandID, "error", err)
			return fmt.Errorf("failed to ensure ChangesetExecutor for %s: %w", msg.CommandID, err)
		}
		s.Log().Debug("ChangesetSupervisor ensured ChangesetExecutor for %s", msg.CommandID)
		return nil
	default:
		return fmt.Errorf("changesetSupervisor received unknown request type %T", message)
	}
}

func (s *ChangesetSupervisor) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch req := request.(type) {
	case EnsureChangesetExecutor:
		err := s.ensureChangesetExecutor(from, req)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure ChangesetExecutor for %s: %w", req.CommandID, err)
		}
		s.Log().Debug("ChangesetSupervisor ensured ChangesetExecutor for %s", req.CommandID)
		return true, nil

	case EnsureResolveCache:
		err := s.ensureResolveCache(from, req)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure ResolveCache for %s: %w", req.CommandID, err)
		}
		s.Log().Debug("ChangesetSupervisor ensured ResolveCache for %s", req.CommandID)
		return true, nil

	default:
		return nil, fmt.Errorf("changesetSupervisor received unknown request type %T", request)
	}
}

func (s *ChangesetSupervisor) ensureChangesetExecutor(from gen.PID, req EnsureChangesetExecutor) error {
	s.Log().Debug("ensuring ChangesetExecutor for command %s", req.CommandID)

	err := s.AddChild(act.SupervisorChildSpec{
		Name:    actornames.ChangesetExecutor(req.CommandID),
		Factory: NewChangesetExecutor,
		Args:    []any{from},
	})
	if err != nil {
		return fmt.Errorf("failed to add ChangesetExecutor for command %s: %w", req.CommandID, err)
	}

	return nil
}

func (s *ChangesetSupervisor) ensureResolveCache(from gen.PID, req EnsureResolveCache) error {
	s.Log().Debug("ensuring ResolveCache for command %s", req.CommandID)

	err := s.AddChild(act.SupervisorChildSpec{
		Name:    actornames.ResolveCache(req.CommandID),
		Factory: NewResolveCache,
		Args:    []any{from},
	})
	if err != nil && err != act.ErrSupervisorChildDuplicate {
		return fmt.Errorf("failed to add ResolveCache for command %s: %w", req.CommandID, err)
	}

	return nil
}
