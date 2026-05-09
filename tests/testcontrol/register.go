// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testcontrol

import (
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
)

// RegisterEDFTypes registers all testcontrol message types with the Ergo
// EDF codec so they can be sent across Ergo node boundaries.
// Both the test plugin and the test harness must call this during startup.
func RegisterEDFTypes() error {
	types := []any{
		// Data types
		time.Duration(0),
		CloudStateEntry{},
		OperationLogEntry{},

		// Error injection
		InjectErrorRequest{},
		InjectErrorResponse{},

		// Latency injection
		InjectLatencyRequest{},
		InjectLatencyResponse{},

		// Clear injections
		ClearInjectionsRequest{},
		ClearInjectionsResponse{},

		// Cloud state manipulation
		PutCloudStateRequest{},
		PutCloudStateResponse{},
		DeleteCloudStateRequest{},
		DeleteCloudStateResponse{},
		GetCloudStateSnapshotRequest{},
		GetCloudStateSnapshotResponse{},

		// Operation log
		GetOperationLogRequest{},
		GetOperationLogResponse{},

		// Response sequences
		ResponseStep{},
		PluginOpSequence{},
		ProgramResponsesRequest{},
		ProgramResponsesResponse{},
		UnprogramResponsesRequest{},
		UnprogramResponsesResponse{},

		// Gate
		OpenGateRequest{},
		OpenGateResponse{},

		// Native ID counter
		SetNativeIDCounterRequest{},
		SetNativeIDCounterResponse{},
	}

	for _, t := range types {
		if err := edf.RegisterTypeOf(t); err != nil && err != gen.ErrTaken {
			return err
		}
	}

	return nil
}
