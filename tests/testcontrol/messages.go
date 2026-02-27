// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package testcontrol defines the shared message types and data structures
// used for Ergo-based communication between the blackbox test harness and the
// TestController actor running in the test plugin.
package testcontrol

import (
	"time"

	"ergo.services/ergo/gen"
)

// TestControllerName is the registered name of the TestController actor
// on the test plugin's Ergo node.
const TestControllerName = gen.Atom("TestController")

// --- Data types ---

// CloudStateEntry represents a single resource as it exists in the simulated cloud.
type CloudStateEntry struct {
	NativeID     string
	ResourceType string
	Properties   string // JSON
}

// OperationLogEntry records a single plugin CRUD operation.
type OperationLogEntry struct {
	Operation    string    // "Create", "Read", "Update", "Delete", "List"
	ResourceType string    // the resource type acted upon
	NativeID     string    // the native ID (may be empty for Create/List)
	Timestamp    time.Time // when the operation occurred
}

// --- Error Injection Messages ---

// InjectErrorRequest asks the TestController to add an error injection rule.
type InjectErrorRequest struct {
	Operation    string // "Create", "Read", "Update", "Delete", "List"
	ResourceType string // empty matches all
	Error        string // error message to return
	Count        int    // 0 = permanent, >0 = fire N times
}

// InjectErrorResponse is the reply to InjectErrorRequest.
type InjectErrorResponse struct{}

// --- Latency Injection Messages ---

// InjectLatencyRequest asks the TestController to add a latency injection rule.
type InjectLatencyRequest struct {
	Operation    string        // "Create", "Read", "Update", "Delete", "List"
	ResourceType string        // empty matches all
	Duration     time.Duration // how long to sleep
}

// InjectLatencyResponse is the reply to InjectLatencyRequest.
type InjectLatencyResponse struct{}

// --- Clear Injection Messages ---

// ClearInjectionsRequest asks the TestController to remove all injection rules.
type ClearInjectionsRequest struct{}

// ClearInjectionsResponse is the reply to ClearInjectionsRequest.
type ClearInjectionsResponse struct{}

// --- Cloud State Messages ---

// PutCloudStateRequest asks the TestController to create or update a cloud state entry.
type PutCloudStateRequest struct {
	NativeID     string
	ResourceType string
	Properties   string // JSON
}

// PutCloudStateResponse is the reply to PutCloudStateRequest.
type PutCloudStateResponse struct{}

// DeleteCloudStateRequest asks the TestController to delete a cloud state entry.
type DeleteCloudStateRequest struct {
	NativeID string
}

// DeleteCloudStateResponse is the reply to DeleteCloudStateRequest.
type DeleteCloudStateResponse struct{}

// GetCloudStateSnapshotRequest asks the TestController for a snapshot of the cloud state.
type GetCloudStateSnapshotRequest struct{}

// GetCloudStateSnapshotResponse is the reply to GetCloudStateSnapshotRequest.
type GetCloudStateSnapshotResponse struct {
	Entries map[string]CloudStateEntry
}

// --- Operation Log Messages ---

// GetOperationLogRequest asks the TestController for the operation log snapshot.
type GetOperationLogRequest struct{}

// GetOperationLogResponse is the reply to GetOperationLogRequest.
type GetOperationLogResponse struct {
	Entries []OperationLogEntry
}
