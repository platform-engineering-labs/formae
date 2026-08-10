// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// statusCheckFor builds the check the operator polls with while it waits for a
// create to finish, carrying the create that started it.
func statusCheckFor(request any) PluginOperatorCheckStatus {
	return PluginOperatorCheckStatus{
		Namespace:         deadlineTestNamespace,
		RequestID:         "request-1",
		NativeID:          "resource-1",
		ResourceType:      "Test::Resource",
		ResourceOperation: resource.OperationCreate,
		Request:           request,
	}
}

func TestStatus_NamespaceMismatchReportsBeforeTerminating(t *testing.T) {
	const callTimeout = 90 * time.Second

	plugin := newRecordingPlugin()
	proc := newOperatorProcess(nil, nil)

	check := statusCheckFor(nil)
	check.Namespace = "other"

	state, _, _, err := status(gen.PID{}, StateWaitingForResource,
		deadlineTestData(plugin, callTimeout), check, proc)

	require.NoError(t, err)
	assert.Equal(t, StateFinishedWithError, state)

	sent := proc.sentProgress()
	require.Len(t, sent, 1, "a terminating status check must always report to the resource updater")
	assert.Equal(t, resource.OperationStatusFailure, sent[0].OperationStatus)
	assert.Equal(t, resource.OperationErrorCodePluginNotFound, sent[0].ErrorCode)
	assert.True(t, sent[0].Failed())

	assert.Equal(t, check.RequestID, sent[0].RequestID, "the requester needs the identifiers it polls with")
	assert.Equal(t, check.NativeID, sent[0].NativeID, "the requester must not lose the native id it already recorded")
	assert.Equal(t, check.ResourceOperation, sent[0].Operation)
}

func TestStatus_ClassifiesStatusCallError(t *testing.T) {
	const callTimeout = 90 * time.Second

	tests := []struct {
		name         string
		err          error
		wantCode     resource.OperationErrorCode
		wantState    gen.Atom
		wantTerminal bool
	}{
		{
			name:         "deadline",
			err:          fmt.Errorf("calling the cloud API: %w", context.DeadlineExceeded),
			wantCode:     resource.OperationErrorCodeServiceTimeout,
			wantState:    StateWaitingForResource,
			wantTerminal: false,
		},
		{
			name:         "throttling",
			err:          errors.New("ThrottlingException: Rate exceeded"),
			wantCode:     resource.OperationErrorCodeThrottling,
			wantState:    StateWaitingForResource,
			wantTerminal: false,
		},
		{
			name:         "throttled past its deadline",
			err:          fmt.Errorf("ThrottlingException: Rate exceeded: %w", context.DeadlineExceeded),
			wantCode:     resource.OperationErrorCodeServiceTimeout,
			wantState:    StateWaitingForResource,
			wantTerminal: false,
		},
		{
			name:         "anything else",
			err:          errors.New("the cloud API refused the connection"),
			wantCode:     resource.OperationErrorCodeUnforeseenError,
			wantState:    StateFinishedWithError,
			wantTerminal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := newRecordingPlugin()
			plugin.err = tt.err
			proc := newOperatorProcess(nil, nil)

			check := statusCheckFor(nil)
			state, _, _, err := status(gen.PID{}, StateWaitingForResource,
				deadlineTestData(plugin, callTimeout), check, proc)

			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state)

			sent := proc.sentProgress()
			require.Len(t, sent, 1, "a status call that fails must still report to the resource updater")
			assert.Equal(t, resource.OperationStatusFailure, sent[0].OperationStatus)
			assert.Equal(t, tt.wantCode, sent[0].ErrorCode)
			assert.Equal(t, tt.wantTerminal, sent[0].Failed())

			assert.Equal(t, check.RequestID, sent[0].RequestID, "the requester needs the identifiers it polls with")
			assert.Equal(t, check.NativeID, sent[0].NativeID)
			assert.Equal(t, check.ResourceOperation, sent[0].Operation)
			assert.NotEmpty(t, sent[0].StatusMessage, "the failure must carry a diagnosable message")
		})
	}
}

// TestStatus_FailingCallConsumesTheRetryLadder covers a status API that keeps
// failing: every failed call must count as an attempt, so the ladder escalates
// its backoff and eventually gives up, and every attempt must report its own
// error rather than repeating the first one.
func TestStatus_FailingCallConsumesTheRetryLadder(t *testing.T) {
	const callTimeout = 90 * time.Second

	plugin := newRecordingPlugin()
	proc := newOperatorProcess(nil, nil)
	check := statusCheckFor(nil)

	plugin.err = errors.New("ThrottlingException: Rate exceeded polling for the first time")
	_, data, _, err := status(gen.PID{}, StateWaitingForResource,
		deadlineTestData(plugin, callTimeout), check, proc)
	require.NoError(t, err)

	plugin.err = errors.New("ThrottlingException: Rate exceeded polling for the second time")
	state, _, _, err := status(gen.PID{}, StateWaitingForResource, data, check, proc)
	require.NoError(t, err)
	assert.Equal(t, StateWaitingForResource, state)

	sent := proc.sentProgress()
	require.Len(t, sent, 2)
	assert.Greater(t, sent[1].Attempts, sent[0].Attempts, "a failing status call must consume an attempt")
	assert.Contains(t, sent[1].StatusMessage, "second time", "every attempt must report its own failure")
	assert.NotContains(t, sent[1].StatusMessage, "first time")
}

// TestStatus_FailingCallsExhaustTheRetryLadder covers a status API that never
// recovers: the operation must give up once its attempts are spent instead of
// polling forever.
func TestStatus_FailingCallsExhaustTheRetryLadder(t *testing.T) {
	const callTimeout = 90 * time.Second

	plugin := newRecordingPlugin()
	plugin.err = errors.New("ThrottlingException: Rate exceeded")
	proc := newOperatorProcess(nil, nil)
	check := statusCheckFor(nil)

	data := deadlineTestData(plugin, callTimeout)
	maxAttempts := int(data.config.MaxRetries) + 1

	var state gen.Atom
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var err error
		state, data, _, err = status(gen.PID{}, StateWaitingForResource, data, check, proc)
		require.NoError(t, err)
	}

	assert.Equal(t, StateFinishedWithError, state, "a status call that never succeeds must not poll forever")

	sent := proc.sentProgress()
	require.Len(t, sent, maxAttempts)
	assert.True(t, sent[maxAttempts-1].Failed(), "the resource updater must be told the operation failed")
	assert.Len(t, proc.scheduled(), maxAttempts-1, "the exhausted ladder must not schedule another check")
}

func TestRetry_UnsupportedOperationReportsTerminalFailure(t *testing.T) {
	const callTimeout = 90 * time.Second

	plugin := newRecordingPlugin()
	proc := newOperatorProcess(nil, nil)

	state, _, _, err := retry(gen.PID{}, StateRetrying, deadlineTestData(plugin, callTimeout),
		PluginOperatorRetry{ResourceOperation: resource.OperationNotSupported}, proc)

	require.NoError(t, err)
	assert.Equal(t, StateFinishedWithError, state)

	sent := proc.sentProgress()
	require.Len(t, sent, 1)
	assert.Equal(t, resource.OperationStatusFailure, sent[0].OperationStatus,
		"an unhandled operation must never be reported as still in progress")
	assert.Equal(t, resource.OperationErrorCodeUnforeseenError, sent[0].ErrorCode)
	assert.True(t, sent[0].Failed())
	assert.NotEmpty(t, sent[0].StatusMessage, "the failure must carry a diagnosable message")
	assert.Empty(t, proc.scheduled(), "an unhandled operation must not be retried")
}
