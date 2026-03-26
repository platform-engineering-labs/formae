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
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// slowStatusPlugin blocks on Status until the context is cancelled.
type slowStatusPlugin struct {
	mockPlugin
}

func (s *slowStatusPlugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// fastStatusPlugin returns immediately with IN_PROGRESS.
type fastStatusPlugin struct {
	mockPlugin
}

func (f *fastStatusPlugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:     resource.OperationCreate,
			StatusMessage: "still creating",
		},
	}, nil
}

func TestCallStatusWithTimeout_SlowCall_ReturnsTimeout(t *testing.T) {
	plugin := &slowStatusPlugin{}
	cfg := model.RetryConfig{StatusCheckInterval: 100 * time.Millisecond}
	parentCtx := context.Background()

	result, err := callStatusWithTimeout(parentCtx, plugin, cfg, &resource.StatusRequest{
		RequestID:    "test-request",
		ResourceType: "AWS::RDS::DBInstance",
	})

	assert.Nil(t, result)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCallStatusWithTimeout_FastCall_ReturnsResult(t *testing.T) {
	plugin := &fastStatusPlugin{}
	cfg := model.RetryConfig{StatusCheckInterval: 5 * time.Second}
	parentCtx := context.Background()

	result, err := callStatusWithTimeout(parentCtx, plugin, cfg, &resource.StatusRequest{
		RequestID:    "test-request",
		ResourceType: "AWS::RDS::DBInstance",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "still creating", result.ProgressResult.StatusMessage)
}

func TestCallStatusWithTimeout_ParentCancelled_PropagatesCancellation(t *testing.T) {
	plugin := &slowStatusPlugin{}
	cfg := model.RetryConfig{StatusCheckInterval: 10 * time.Second}
	parentCtx, cancel := context.WithCancel(context.Background())

	// Cancel parent after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := callStatusWithTimeout(parentCtx, plugin, cfg, &resource.StatusRequest{
		RequestID:    "test-request",
		ResourceType: "AWS::RDS::DBInstance",
	})

	assert.Nil(t, result)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
