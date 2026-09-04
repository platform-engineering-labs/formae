// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import "errors"

// Request-scoped failures are answers, not termination reasons: an actor that
// returns an error from HandleCall is terminated by the framework without
// replying, so the caller times out, every request queued in the actor's
// mailbox dies with it, and the supervisor respawns the actor only for the
// next bad request to repeat the cycle. The persisters therefore reply with
// typed result messages that carry their own success/failure status, and the
// HandleCall error return keeps the meaning ergo assigns to it: terminate the
// actor — reserved for genuine faults (invariant violations, protocol bugs),
// where a crash and supervised restart is the right medicine.

// PersistVersionsResult is the reply to the bulk persist requests (target,
// stack, policy, and generator updates): the stored versions on success, or
// the failure that refused the write.
type PersistVersionsResult struct {
	Versions []string
	Error    string
}

func (r PersistVersionsResult) CallError() string { return r.Error }

// UnwrapCall folds a failed typed reply into the error return, so a call site
// reads as one (result, error) pair whether the failure was transport-level
// (the real error return) or request-scoped (a reply whose status carries the
// failure). Wrap the Call directly:
// result, err := messages.UnwrapCall(proc.Call(target, req)).
func UnwrapCall(result any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if r, ok := result.(interface{ CallError() string }); ok && r.CallError() != "" {
		return nil, errors.New(r.CallError())
	}
	return result, nil
}
