// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProgressResultFailed_ReturnsFalseWhenErrorIsRecoverableAndAttemptsAreNotExhausted(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusFailure,
		ErrorCode:       OperationErrorCodeNetworkFailure,
		Attempts:        2,
		MaxAttempts:     3,
	}

	assert.False(t, result.Failed())
}

func TestProgressResultFailed_ReturnsTrueWhenErrorIsRecoverableAndAttemptsAreExhausted(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusFailure,
		ErrorCode:       OperationErrorCodeNetworkFailure,
		Attempts:        4,
		MaxAttempts:     3,
	}

	assert.True(t, result.Failed())
}

func TestProgressResultFailed_ReturnsTrueWhenErrorIsNotRecoverable(t *testing.T) {
	result := ProgressResult{
		OperationStatus: OperationStatusFailure,
		ErrorCode:       OperationErrorCodeUnforeseenError,
		Attempts:        1,
		MaxAttempts:     3,
	}

	assert.True(t, result.Failed())
}
