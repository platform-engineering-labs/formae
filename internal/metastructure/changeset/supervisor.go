// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/actornames"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
)

type ChangesetSupervisor struct {
	act.Supervisor
	admittedDispatch map[string]gen.PID
}

func NewChangesetSupervisor() gen.ProcessBehavior {
	return &ChangesetSupervisor{}
}

type EnsureChangesetExecutor struct {
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
	case messages.RetireAdmittedDispatch:
		// Only the persister can attest that provider work is durably terminal.
		persister, err := s.Node().ProcessPID(actornames.FormaCommandPersister)
		if err == nil && from == persister {
			delete(s.admittedDispatch, msg.CommandID)
		}
		return nil
	case EnsureChangesetExecutor:
		err := s.ensureChangesetExecutor(from, msg)
		if err != nil {
			s.Log().Error("Failed to ensure ChangesetExecutor commandID=%s: %v", msg.CommandID, err)
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
	case DispatchAdmittedChangeset:
		return s.dispatchAdmittedChangeset(from, req), nil
	case EnsureChangesetExecutor:
		err := s.ensureChangesetExecutor(from, req)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure ChangesetExecutor for %s: %w", req.CommandID, err)
		}
		s.Log().Debug("ChangesetSupervisor ensured ChangesetExecutor for %s", req.CommandID)
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

// DispatchAdmittedChangeset serializes ownership and Start in the supervisor.
// A nil Changeset is an ownership query; unknown existing actors fail closed.
type DispatchAdmittedChangeset struct {
	CommandID string
	Changeset *Changeset
}
type AdmittedDispatchResult struct {
	Owned bool
	Error string
}

func (r AdmittedDispatchResult) CallError() string { return r.Error }
func (s *ChangesetSupervisor) dispatchAdmittedChangeset(from gen.PID, req DispatchAdmittedChangeset) AdmittedDispatchResult {
	if owner, known := s.admittedDispatch[req.CommandID]; known {
		current, err := s.Node().ProcessPID(actornames.ChangesetExecutor(req.CommandID))
		if err != nil || current != owner {
			return AdmittedDispatchResult{Error: "original command executor owner was lost; recovery required: " + req.CommandID}
		}
		return AdmittedDispatchResult{Owned: true}
	}
	if _, err := s.Node().ProcessPID(actornames.ChangesetExecutor(req.CommandID)); err == nil {
		return AdmittedDispatchResult{Error: "original command executor has uncertain ownership; recovery required: " + req.CommandID}
	}
	if req.Changeset == nil {
		return AdmittedDispatchResult{}
	}
	if req.Changeset.CommandID != req.CommandID {
		return AdmittedDispatchResult{Error: "changeset command identity mismatch"}
	}
	if err := s.ensureChangesetExecutor(from, EnsureChangesetExecutor{CommandID: req.CommandID}); err != nil {
		return AdmittedDispatchResult{Error: err.Error()}
	}
	// Record the owner only after successful delivery. On a send error a later retry must surface
	// recovery, not guess whether a provider was reached. No process-wide lease
	// or cross-agent exactly-once claim is made by this local owner record.
	if s.admittedDispatch == nil {
		s.admittedDispatch = map[string]gen.PID{}
	}
	owner, err := s.Node().ProcessPID(actornames.ChangesetExecutor(req.CommandID))
	if err != nil {
		return AdmittedDispatchResult{Error: err.Error()}
	}
	if err := s.Send(gen.ProcessID{Name: actornames.ChangesetExecutor(req.CommandID), Node: s.Node().Name()}, Start{Changeset: *req.Changeset}); err != nil {
		return AdmittedDispatchResult{Error: "original command dispatch requires recovery: " + req.CommandID + ": " + err.Error()}
	}
	s.admittedDispatch[req.CommandID] = owner
	return AdmittedDispatchResult{Owned: true}
}
