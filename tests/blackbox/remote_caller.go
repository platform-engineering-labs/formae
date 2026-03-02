// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

// remoteCall is sent to the RemoteCaller actor to make a synchronous call
// to a named process on a remote Ergo node.
type remoteCall struct {
	Target   gen.ProcessID
	Request  any
	Response chan<- any
	Errors   chan<- error
	Timeout  int // seconds, 0 = default 5
}

// remoteCaller is an Ergo actor that bridges Go test code to the actor system
// for making cross-node calls. Unlike testutil.TestHelperActor which only
// calls local actors, this supports calling actors on remote nodes.
type remoteCaller struct {
	act.Actor
}

func newRemoteCaller() gen.ProcessBehavior {
	return &remoteCaller{}
}

func (rc *remoteCaller) Init(args ...any) error {
	rc.Log().Info("RemoteCaller initialized")
	return nil
}

func (rc *remoteCaller) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case remoteCall:
		timeout := msg.Timeout
		if timeout <= 0 {
			timeout = 5
		}
		rc.Log().Info("Calling %v with %T (timeout: %ds)", msg.Target, msg.Request, timeout)
		res, err := rc.CallWithTimeout(msg.Target, msg.Request, timeout)
		if err != nil {
			rc.Log().Error("Call to %v failed: %s", msg.Target, err)
			msg.Errors <- err
		} else {
			msg.Response <- res
		}
	default:
		rc.Log().Warning("RemoteCaller received unexpected message: %T", message)
	}
	return nil
}

// callRemote sends a synchronous call from test code through the RemoteCaller
// actor to a named process on a remote Ergo node.
func callRemote(node gen.Node, target gen.ProcessID, request any, timeoutSecs int) (any, error) {
	res := make(chan any, 1)
	errs := make(chan error, 1)

	err := node.Send(
		gen.ProcessID{Name: "RemoteCaller", Node: node.Name()},
		remoteCall{
			Target:   target,
			Request:  request,
			Response: res,
			Errors:   errs,
			Timeout:  timeoutSecs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send to RemoteCaller: %w", err)
	}

	select {
	case r := <-res:
		return r, nil
	case e := <-errs:
		return nil, e
	}
}
