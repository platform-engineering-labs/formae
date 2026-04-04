// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Unit tests for ResponseQueue ---

func TestResponseQueue_PopReturnsStepsInOrder(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "my-resource",
			Operation: "Create",
			Steps: []testcontrol.ResponseStep{
				{ErrorCode: "Throttling"},
				{ErrorCode: ""},
				{ErrorCode: "ServiceInternalError"},
			},
		},
	})

	step := rq.CheckCreate(json.RawMessage(`{"Name":"my-resource"}`))
	require.NotNil(t, step)
	assert.Equal(t, "Throttling", step.ErrorCode)

	step = rq.CheckCreate(json.RawMessage(`{"Name":"my-resource"}`))
	require.NotNil(t, step)
	assert.Equal(t, "", step.ErrorCode)

	step = rq.CheckCreate(json.RawMessage(`{"Name":"my-resource"}`))
	require.NotNil(t, step)
	assert.Equal(t, "ServiceInternalError", step.ErrorCode)
}

func TestResponseQueue_ReturnsNilWhenExhausted(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps: []testcontrol.ResponseStep{
				{ErrorCode: "Throttling"},
			},
		},
	})

	step := rq.CheckRead("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "Throttling", step.ErrorCode)

	// Queue exhausted -> nil
	step = rq.CheckRead("native-1")
	assert.Nil(t, step, "expected nil after queue exhausted")
}

func TestResponseQueue_ReturnsNilWhenNoQueueExists(t *testing.T) {
	rq := NewResponseQueue()
	// No programming at all
	assert.Nil(t, rq.CheckCreate(json.RawMessage(`{"Name":"unknown"}`)))
	assert.Nil(t, rq.CheckRead("unknown"))
	assert.Nil(t, rq.CheckUpdate("unknown"))
	assert.Nil(t, rq.CheckDelete("unknown"))
}

func TestResponseQueue_DifferentKeysAreIndependent(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
		{
			MatchKey:  "native-2",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}},
		},
	})

	step := rq.CheckRead("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "Throttling", step.ErrorCode)

	step = rq.CheckRead("native-2")
	require.NotNil(t, step)
	assert.Equal(t, "AccessDenied", step.ErrorCode)
}

func TestResponseQueue_SameKeyDifferentOperationsAreIndependent(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
		{
			MatchKey:  "native-1",
			Operation: "Update",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "NotUpdatable"}},
		},
	})

	step := rq.CheckRead("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "Throttling", step.ErrorCode)

	step = rq.CheckUpdate("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "NotUpdatable", step.ErrorCode)
}

func TestResponseQueue_ProgramAppendsToExistingQueues(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
	})

	// Append a different operation for the same key
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Delete",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}},
		},
	})

	// Both queues should be present
	step := rq.CheckRead("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "Throttling", step.ErrorCode)

	step = rq.CheckDelete("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "AccessDenied", step.ErrorCode)
}

func TestResponseQueue_ProgramAppendsStepsForSameKey(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
	})
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "NotFound"}},
		},
	})

	// First call returns first programmed step
	step := rq.CheckRead("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "Throttling", step.ErrorCode)

	// Second call returns appended step
	step = rq.CheckRead("native-1")
	require.NotNil(t, step)
	assert.Equal(t, "NotFound", step.ErrorCode)

	// Queue exhausted
	assert.Nil(t, rq.CheckRead("native-1"))
}

func TestResponseQueue_CheckCreateExtractsNameFromJSON(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "bucket-alpha",
			Operation: "Create",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "AlreadyExists"}},
		},
	})

	// Properties with matching Name
	step := rq.CheckCreate(json.RawMessage(`{"Name":"bucket-alpha","Value":"x"}`))
	require.NotNil(t, step)
	assert.Equal(t, "AlreadyExists", step.ErrorCode)

	// Properties with non-matching Name: returns nil
	step = rq.CheckCreate(json.RawMessage(`{"Name":"bucket-beta","Value":"y"}`))
	assert.Nil(t, step)
}

func TestResponseQueue_CheckCreateReturnsNilForInvalidJSON(t *testing.T) {
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "anything",
			Operation: "Create",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
	})

	step := rq.CheckCreate(json.RawMessage(`not valid json`))
	assert.Nil(t, step)
}

// --- Integration tests: CRUD methods with response queue ---

func TestResponseQueue_Create_ErrorFromQueue(t *testing.T) {
	cs := NewCloudState()
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "my-res",
			Operation: "Create",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
	})
	ol := NewOperationLog()
	p := &TestPlugin{cloudState: cs, responseQueue: rq, opLog: ol}

	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"my-res","Value":"v"}`),
	})

	require.NoError(t, err, "Go error should be nil; error conveyed via result")
	require.NotNil(t, result)
	require.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationStatusFailure, result.ProgressResult.OperationStatus)
	assert.Equal(t, resource.OperationErrorCode("Throttling"), result.ProgressResult.ErrorCode)

	// Cloud state should NOT be mutated
	snap := cs.Snapshot()
	assert.Empty(t, snap, "cloud state should be empty after queue error")

	// Op should be recorded
	entries := ol.Snapshot()
	require.Len(t, entries, 1)
	assert.Equal(t, "Create", entries[0].Operation)
}

func TestResponseQueue_Create_SuccessFromQueue(t *testing.T) {
	cs := NewCloudState()
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "my-res",
			Operation: "Create",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: ""}}, // success
		},
	})
	p := &TestPlugin{cloudState: cs, responseQueue: rq}

	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"my-res","Value":"v"}`),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)

	// Cloud state should be mutated
	snap := cs.Snapshot()
	assert.Len(t, snap, 1)
}

func TestResponseQueue_Read_ErrorFromQueue(t *testing.T) {
	cs := NewCloudState()
	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1"}`)
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Read",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "ServiceInternalError"}},
		},
	})
	ol := NewOperationLog()
	p := &TestPlugin{cloudState: cs, responseQueue: rq, opLog: ol}

	result, err := p.Read(context.Background(), &resource.ReadRequest{
		NativeID:     "native-1",
		ResourceType: "Test::Generic::Resource",
	})

	require.NoError(t, err, "Go error should be nil; error conveyed via result")
	require.NotNil(t, result)
	assert.Equal(t, resource.OperationErrorCode("ServiceInternalError"), result.ErrorCode)

	// Op should be recorded
	entries := ol.Snapshot()
	require.Len(t, entries, 1)
	assert.Equal(t, "Read", entries[0].Operation)
}

func TestResponseQueue_Update_ErrorFromQueue(t *testing.T) {
	cs := NewCloudState()
	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1","Value":"old"}`)
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Update",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "NotUpdatable"}},
		},
	})
	ol := NewOperationLog()
	p := &TestPlugin{cloudState: cs, responseQueue: rq, opLog: ol}

	result, err := p.Update(context.Background(), &resource.UpdateRequest{
		NativeID:          "native-1",
		ResourceType:      "Test::Generic::Resource",
		DesiredProperties: json.RawMessage(`{"Name":"r1","Value":"new"}`),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationStatusFailure, result.ProgressResult.OperationStatus)
	assert.Equal(t, resource.OperationErrorCode("NotUpdatable"), result.ProgressResult.ErrorCode)

	// Cloud state should NOT be mutated
	entry, ok := cs.Get("native-1")
	require.True(t, ok)
	assert.Contains(t, entry.Properties, `"old"`, "original value should be preserved")
}

func TestResponseQueue_Delete_ErrorFromQueue(t *testing.T) {
	cs := NewCloudState()
	cs.Put("native-1", "Test::Generic::Resource", `{"Name":"r1"}`)
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "native-1",
			Operation: "Delete",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "ResourceConflict"}},
		},
	})
	ol := NewOperationLog()
	p := &TestPlugin{cloudState: cs, responseQueue: rq, opLog: ol}

	result, err := p.Delete(context.Background(), &resource.DeleteRequest{
		NativeID:     "native-1",
		ResourceType: "Test::Generic::Resource",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationStatusFailure, result.ProgressResult.OperationStatus)
	assert.Equal(t, resource.OperationErrorCode("ResourceConflict"), result.ProgressResult.ErrorCode)

	// Cloud state should NOT be mutated (resource still exists)
	_, ok := cs.Get("native-1")
	assert.True(t, ok, "resource should still exist after queue error")
}

func TestResponseQueue_FallsThroughToInjection(t *testing.T) {
	cs := NewCloudState()
	rq := NewResponseQueue()
	// Empty queue: no sequences programmed
	inj := NewInjectionState()
	inj.AddErrorRule(ErrorRule{
		Operation: "Create",
		Error:     "injected error",
		Count:     1,
	})
	p := &TestPlugin{cloudState: cs, responseQueue: rq, injections: inj}

	// Response queue has nothing, so should fall through to injection
	_, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"fallthrough-test"}`),
	})

	require.Error(t, err, "should fall through to injection when queue returns nil")
	assert.Equal(t, "injected error", err.Error())
}

func TestResponseQueue_QueueTakesPriorityOverInjection(t *testing.T) {
	cs := NewCloudState()
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "priority-test",
			Operation: "Create",
			Steps:     []testcontrol.ResponseStep{{ErrorCode: "Throttling"}},
		},
	})
	inj := NewInjectionState()
	inj.AddErrorRule(ErrorRule{
		Operation: "Create",
		Error:     "injection should not fire",
		Count:     0,
	})
	p := &TestPlugin{cloudState: cs, responseQueue: rq, injections: inj}

	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"priority-test"}`),
	})

	require.NoError(t, err, "queue error returns via result, not Go error")
	require.NotNil(t, result)
	assert.Equal(t, resource.OperationErrorCode("Throttling"), result.ProgressResult.ErrorCode)
}

func TestResponseQueue_MultiStepSequence(t *testing.T) {
	cs := NewCloudState()
	rq := NewResponseQueue()
	rq.Program([]testcontrol.PluginOpSequence{
		{
			MatchKey:  "multi-step",
			Operation: "Create",
			Steps: []testcontrol.ResponseStep{
				{ErrorCode: "Throttling"},  // 1st call: error
				{ErrorCode: "Throttling"},  // 2nd call: error
				{ErrorCode: ""},            // 3rd call: success
			},
		},
	})
	p := &TestPlugin{cloudState: cs, responseQueue: rq}

	// 1st call: error
	result, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"multi-step","Value":"v"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusFailure, result.ProgressResult.OperationStatus)

	// 2nd call: error
	result, err = p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"multi-step","Value":"v"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusFailure, result.ProgressResult.OperationStatus)

	// 3rd call: success
	result, err = p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"multi-step","Value":"v"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)

	// 4th call: queue exhausted, falls through to normal success
	result, err = p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "Test::Generic::Resource",
		Properties:   json.RawMessage(`{"Name":"multi-step","Value":"v"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)
}
