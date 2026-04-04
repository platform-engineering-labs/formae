// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testcontrol

// --- Response Sequence Types ---

// ResponseStep represents a single plugin response in a programmed sequence.
type ResponseStep struct {
	ErrorCode string // empty = success, otherwise a resource.OperationErrorCode value
}

// PluginOpSequence is an ordered list of responses for a specific plugin CRUD call.
// The plugin returns these in order. After exhausting the list, returns success.
type PluginOpSequence struct {
	MatchKey  string         // Name (for Create) or NativeID (for Read/Update/Delete)
	Operation string         // "Create", "Read", "Update", "Delete"
	Steps     []ResponseStep // ordered responses; empty = always succeed
}

// --- Program Responses Messages ---

// ProgramResponsesRequest programs the test plugin with response sequences.
type ProgramResponsesRequest struct {
	Sequences []PluginOpSequence
}

// ProgramResponsesResponse is the reply to ProgramResponsesRequest.
type ProgramResponsesResponse struct{}

// UnprogramResponsesRequest removes previously programmed sequences.
// Used to roll back responses when a command is rejected after programming.
type UnprogramResponsesRequest struct {
	Sequences []PluginOpSequence
}

// UnprogramResponsesResponse is the reply to UnprogramResponsesRequest.
type UnprogramResponsesResponse struct{}
