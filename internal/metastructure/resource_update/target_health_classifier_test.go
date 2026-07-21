// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"
	"time"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTrackedProgress(status resource.OperationStatus, errorCode resource.OperationErrorCode) *plugin.TrackedProgress {
	return &plugin.TrackedProgress{
		ProgressResult: resource.ProgressResult{
			OperationStatus: status,
			ErrorCode:       errorCode,
		},
		Attempts:    1,
		MaxAttempts: 0, // exhausted — no retries
	}
}

func TestTargetHealthObservation(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	label := "aws/us-east-1"

	cases := []struct {
		name          string
		tp            *plugin.TrackedProgress
		wantOK        bool
		wantState     string
		wantLastSeen  bool // whether LastSeenAt should be non-nil
		wantErrorCode string
	}{
		{
			name:         "terminal success → reachable with last_seen_at",
			tp:           makeTrackedProgress(resource.OperationStatusSuccess, ""),
			wantOK:       true,
			wantState:    pkgmodel.TargetHealthStateReachable,
			wantLastSeen: true,
		},
		{
			name:          "terminal failure NetworkFailure → unreachable with code",
			tp:            makeTrackedProgress(resource.OperationStatusFailure, resource.OperationErrorCodeNetworkFailure),
			wantOK:        true,
			wantState:     pkgmodel.TargetHealthStateUnreachable,
			wantLastSeen:  false,
			wantErrorCode: string(resource.OperationErrorCodeNetworkFailure),
		},
		{
			name:          "terminal failure ServiceTimeout → unreachable with code",
			tp:            makeTrackedProgress(resource.OperationStatusFailure, resource.OperationErrorCodeServiceTimeout),
			wantOK:        true,
			wantState:     pkgmodel.TargetHealthStateUnreachable,
			wantLastSeen:  false,
			wantErrorCode: string(resource.OperationErrorCodeServiceTimeout),
		},
		{
			name:   "terminal failure NotFound → no observation",
			tp:     makeTrackedProgress(resource.OperationStatusFailure, resource.OperationErrorCodeNotFound),
			wantOK: false,
		},
		{
			name:   "terminal failure other error → no observation",
			tp:     makeTrackedProgress(resource.OperationStatusFailure, resource.OperationErrorCodeAccessDenied),
			wantOK: false,
		},
		{
			name:   "non-terminal in-progress → no observation",
			tp: &plugin.TrackedProgress{
				ProgressResult: resource.ProgressResult{
					OperationStatus: resource.OperationStatusInProgress,
				},
				Attempts:    1,
				MaxAttempts: 3,
			},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, ok := targetHealthObservation(label, tc.tp, now)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				assert.Equal(t, pkgmodel.TargetHealthObservation{}, obs)
				return
			}
			assert.Equal(t, label, obs.TargetLabel)
			assert.Equal(t, tc.wantState, obs.State)
			assert.Equal(t, now, obs.ObservedAt)
			assert.Empty(t, obs.IncarnationID)
			assert.Equal(t, tc.wantErrorCode, obs.LastErrorCode)
			if tc.wantLastSeen {
				require.NotNil(t, obs.LastSeenAt)
				assert.Equal(t, now, *obs.LastSeenAt)
			} else {
				assert.Nil(t, obs.LastSeenAt)
			}
		})
	}
}
