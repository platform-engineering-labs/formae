// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProgressResultFailed_ReturnsTrueWhenStatusIsFailure(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusFailure,
		ErrorCode:       OperationErrorCodeNetworkFailure,
	}

	assert.True(t, result.Failed())
}

func TestProgressResultFailed_ReturnsFalseWhenStatusIsSuccess(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusSuccess,
	}

	assert.False(t, result.Failed())
}

func TestProgressResultFailed_ReturnsFalseWhenStatusIsInProgress(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusInProgress,
	}

	assert.False(t, result.Failed())
}

func TestProgressResultFinishedSuccessfully_ReturnsTrueWhenSuccess(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusSuccess,
	}

	assert.True(t, result.FinishedSuccessfully())
}

func TestProgressResultHasFinished_ReturnsTrueWhenFailure(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusFailure,
	}

	assert.True(t, result.HasFinished())
}

func TestProgressResultInProgress_ReturnsTrueWhenInProgress(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusInProgress,
	}

	assert.True(t, result.InProgress())
}
