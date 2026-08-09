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

const progressTestCallTimeout = 90 * time.Second

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
	plugin := newRecordingPlugin()
	proc := newOperatorProcess(nil, nil)

	check := statusCheckFor(nil)
	check.Namespace = "other"

	state, _, _, err := status(gen.PID{}, StateWaitingForResource,
		deadlineTestData(plugin, progressTestCallTimeout), check, proc)

	require.NoError(t, err)
	assert.Equal(t, StateFinishedWithError, state)

	sent := proc.sentProgress()
	require.Len(t, sent, 1, "a terminating status check must always report to the resource updater")
	assert.Equal(t, resource.OperationStatusFailure, sent[0].OperationStatus)
	assert.Equal(t, resource.OperationErrorCodePluginNotFound, sent[0].ErrorCode)
	assert.True(t, sent[0].Failed())
}

func TestStatus_ClassifiesStatusCallError(t *testing.T) {
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
				deadlineTestData(plugin, progressTestCallTimeout), check, proc)

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

// TestStatus_RecoverableCallErrorNeverReissuesTheOriginalOperation covers a
// status check that carries the create that started it. A create that may
// already have reached the provider must never be sent again, so a status call
// that fails recoverably reschedules another status check.
func TestStatus_RecoverableCallErrorNeverReissuesTheOriginalOperation(t *testing.T) {
	plugin := newRecordingPlugin()
	plugin.err = errors.New("ThrottlingException: Rate exceeded")
	proc := newOperatorProcess(nil, nil)

	check := statusCheckFor(CreateResource{Namespace: deadlineTestNamespace, ResourceType: "Test::Resource"})

	state, _, _, err := status(gen.PID{}, StateWaitingForResource,
		deadlineTestData(plugin, progressTestCallTimeout), check, proc)

	require.NoError(t, err)
	assert.Equal(t, StateWaitingForResource, state)

	scheduled := proc.scheduled()
	require.Len(t, scheduled, 1)
	rescheduledCheck, ok := scheduled[0].(PluginOperatorCheckStatus)
	require.True(t, ok, "a failed status call must reschedule a status check, got %T", scheduled[0])
	assert.Nil(t, rescheduledCheck.Request, "the rescheduled check must not carry the request that would re-issue the operation")
	assert.Equal(t, check.RequestID, rescheduledCheck.RequestID)
	assert.Equal(t, check.NativeID, rescheduledCheck.NativeID)
	assert.Equal(t, check.ResourceType, rescheduledCheck.ResourceType)
	assert.Equal(t, check.ResourceOperation, rescheduledCheck.ResourceOperation)

	_, called := plugin.ctxs[resource.OperationCreate]
	assert.False(t, called, "the mutating operation must never be re-issued")
}

func TestRetry_UnsupportedOperationReportsTerminalFailure(t *testing.T) {
	plugin := newRecordingPlugin()
	proc := newOperatorProcess(nil, nil)

	state, _, _, err := retry(gen.PID{}, StateRetrying, deadlineTestData(plugin, progressTestCallTimeout),
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
