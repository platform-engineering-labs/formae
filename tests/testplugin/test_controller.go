// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// TestController is an Ergo actor that handles control messages from the test
// harness. It delegates to CloudState, InjectionState, and OperationLog which
// are retrieved from the process environment during Init().
type TestController struct {
	act.Actor

	cloudState    *CloudState
	injections    *InjectionState
	opLog         *OperationLog
	responseQueue *ResponseQueue
}

// NewTestController is the factory function for the TestController actor.
func NewTestController() gen.ProcessBehavior {
	return &TestController{}
}

func (tc *TestController) Init(args ...any) error {
	cs, ok := tc.Env("CloudState")
	if !ok {
		return fmt.Errorf("TestController: missing 'CloudState' environment variable")
	}
	tc.cloudState = cs.(*CloudState)

	inj, ok := tc.Env("InjectionState")
	if !ok {
		return fmt.Errorf("TestController: missing 'InjectionState' environment variable")
	}
	tc.injections = inj.(*InjectionState)

	ol, ok := tc.Env("OperationLog")
	if !ok {
		return fmt.Errorf("TestController: missing 'OperationLog' environment variable")
	}
	tc.opLog = ol.(*OperationLog)

	rq, ok := tc.Env("ResponseQueue")
	if !ok {
		return fmt.Errorf("TestController: missing 'ResponseQueue' environment variable")
	}
	tc.responseQueue = rq.(*ResponseQueue)

	tc.Log().Info("TestController initialized")
	return nil
}

func (tc *TestController) HandleCall(from gen.PID, ref gen.Ref, request any) (any, error) {
	switch msg := request.(type) {
	// --- Injection ---
	case testcontrol.InjectErrorRequest:
		tc.injections.AddErrorRule(ErrorRule{
			Operation:    msg.Operation,
			ResourceType: msg.ResourceType,
			Error:        msg.Error,
			Count:        msg.Count,
		})
		return testcontrol.InjectErrorResponse{}, nil

	case testcontrol.InjectLatencyRequest:
		tc.injections.AddLatencyRule(LatencyRule{
			Operation:    msg.Operation,
			ResourceType: msg.ResourceType,
			Duration:     msg.Duration,
		})
		return testcontrol.InjectLatencyResponse{}, nil

	case testcontrol.ClearInjectionsRequest:
		tc.injections.Clear()
		return testcontrol.ClearInjectionsResponse{}, nil

	// --- Cloud State ---
	case testcontrol.PutCloudStateRequest:
		tc.cloudState.Put(msg.NativeID, msg.ResourceType, msg.Properties)
		return testcontrol.PutCloudStateResponse{}, nil

	case testcontrol.DeleteCloudStateRequest:
		tc.cloudState.Delete(msg.NativeID)
		return testcontrol.DeleteCloudStateResponse{}, nil

	case testcontrol.GetCloudStateSnapshotRequest:
		return testcontrol.GetCloudStateSnapshotResponse{
			Entries: tc.cloudState.Snapshot(),
		}, nil

	// --- Response Queue ---
	case testcontrol.ProgramResponsesRequest:
		tc.responseQueue.Program(msg.Sequences)
		return testcontrol.ProgramResponsesResponse{}, nil

	case testcontrol.UnprogramResponsesRequest:
		tc.responseQueue.Unprogram(msg.Sequences)
		return testcontrol.UnprogramResponsesResponse{}, nil

	// --- Operation Log ---
	case testcontrol.GetOperationLogRequest:
		return testcontrol.GetOperationLogResponse{
			Entries: tc.opLog.Snapshot(),
		}, nil

	default:
		return nil, fmt.Errorf("TestController: unhandled message type: %T", request)
	}
}

func (tc *TestController) HandleMessage(from gen.PID, message any) error {
	tc.Log().Debug("TestController received unexpected async message: %T", message)
	return nil
}