// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// readResultPlugin answers every Read with a canned ReadResult.
type readResultPlugin struct {
	*recordingPlugin
	result *resource.ReadResult
}

func (p *readResultPlugin) Read(ctx context.Context, _ *resource.ReadRequest) (*resource.ReadResult, error) {
	p.record(resource.OperationRead, ctx)
	return p.result, nil
}

// TestRead_CarriesPluginStatusMessage covers a read that comes back with an
// error code: the plugin's explanation must reach the ProgressResult so a
// NotFound that tombstones a resource during sync can be traced to the
// provider's actual answer.
func TestRead_CarriesPluginStatusMessage(t *testing.T) {
	const msg = "ResourceNotFoundException: Resource of type 'Test::Resource' with identifier 'resource-1' was not found."

	tests := []struct {
		name       string
		errorCode  resource.OperationErrorCode
		isSync     bool
		wantStatus resource.OperationStatus
	}{
		{
			name:       "not found during sync",
			errorCode:  resource.OperationErrorCodeNotFound,
			isSync:     true,
			wantStatus: resource.OperationStatusSuccess,
		},
		{
			name:       "not found outside sync",
			errorCode:  resource.OperationErrorCodeNotFound,
			wantStatus: resource.OperationStatusFailure,
		},
		{
			name:       "access denied",
			errorCode:  resource.OperationErrorCodeAccessDenied,
			isSync:     true,
			wantStatus: resource.OperationStatusFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &readResultPlugin{
				recordingPlugin: newRecordingPlugin(),
				result: &resource.ReadResult{
					ResourceType:  "Test::Resource",
					ErrorCode:     tt.errorCode,
					StatusMessage: msg,
				},
			}
			proc := newOperatorProcess(nil, nil)
			op := ReadResource{
				Namespace:    operatorTestNamespace,
				NativeID:     "resource-1",
				ResourceType: "Test::Resource",
				IsSync:       tt.isSync,
			}

			_, _, progress, _, err := read(gen.PID{}, StateNotStarted, operatorTestData(plugin), op, proc)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantStatus, progress.OperationStatus)
			assert.Equal(t, tt.errorCode, progress.ErrorCode)
			assert.Equal(t, msg, progress.StatusMessage)
		})
	}
}
