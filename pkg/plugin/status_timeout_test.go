// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// slowStatusPlugin simulates a plugin whose Status call takes longer than the timeout.
type slowStatusPlugin struct {
	mockPlugin
	delay time.Duration
}

func (s *slowStatusPlugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	select {
	case <-time.After(s.delay):
		return &resource.StatusResult{
			ProgressResult: &resource.ProgressResult{
				Operation:     resource.OperationCreate,
				StatusMessage: "still creating",
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestStatusCheckTimeout_ReturnsDeadlineExceeded(t *testing.T) {
	plugin := &slowStatusPlugin{delay: 5 * time.Second}
	timeout := 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, err := plugin.Status(ctx, &resource.StatusRequest{
		RequestID:    "test-request",
		NativeID:     "test-native-id",
		ResourceType: "AWS::RDS::DBInstance",
	})

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStatusCheckTimeout_FastCallSucceeds(t *testing.T) {
	plugin := &slowStatusPlugin{delay: 10 * time.Millisecond}
	timeout := 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := plugin.Status(ctx, &resource.StatusRequest{
		RequestID:    "test-request",
		NativeID:     "test-native-id",
		ResourceType: "AWS::RDS::DBInstance",
	})

	assert.NoError(t, err)
	assert.Equal(t, "still creating", result.ProgressResult.StatusMessage)
}
