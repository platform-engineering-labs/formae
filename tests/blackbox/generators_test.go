// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestGenerator_ProducesValidOperations(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  5,
			OperationCount: Range{Min: 5, Max: 20},
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		assert.GreaterOrEqual(t, len(ops), 5)
		assert.LessOrEqual(t, len(ops), 20)

		for _, op := range ops {
			assert.GreaterOrEqual(t, int(op.Kind), int(OpApply))
			assert.LessOrEqual(t, int(op.Kind), int(OpVerifyState))

			for _, id := range op.ResourceIDs {
				assert.GreaterOrEqual(t, id, 0)
				assert.Less(t, id, config.ResourceCount)
			}
		}
	})
}

func TestGenerator_RespectsConfig_NoFailures(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  3,
			OperationCount: Range{Min: 10, Max: 30},
			EnableFailures: false,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			assert.NotEqual(t, OpInjectError, op.Kind, "should not generate OpInjectError when EnableFailures=false")
			assert.NotEqual(t, OpInjectLatency, op.Kind, "should not generate OpInjectLatency when EnableFailures=false")
			assert.NotEqual(t, OpClearInjections, op.Kind, "should not generate OpClearInjections when EnableFailures=false")
		}
	})
}

func TestGenerator_RespectsConfig_NoCloudChanges(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  3,
			OperationCount: Range{Min: 10, Max: 30},
			EnableCloudChanges: false,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			assert.NotEqual(t, OpCloudModify, op.Kind, "should not generate OpCloudModify when EnableCloudChanges=false")
			assert.NotEqual(t, OpCloudDelete, op.Kind, "should not generate OpCloudDelete when EnableCloudChanges=false")
			assert.NotEqual(t, OpCloudCreate, op.Kind, "should not generate OpCloudCreate when EnableCloudChanges=false")
		}
	})
}

func TestGenerator_RespectsConfig_NoConcurrency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:     3,
			OperationCount:    Range{Min: 10, Max: 30},
			EnableConcurrency: false,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			if op.Kind == OpApply || op.Kind == OpDestroy {
				assert.True(t, op.Blocking, "all apply/destroy ops should be blocking when EnableConcurrency=false")
			}
		}
	})
}

func TestGenerator_RespectsConfig_NoCancel(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  3,
			OperationCount: Range{Min: 10, Max: 30},
			EnableCancel:   false,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			assert.NotEqual(t, OpCancel, op.Kind, "should not generate OpCancel when EnableCancel=false")
		}
	})
}

func TestGenerator_WithFailuresEnabled(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  3,
			OperationCount: Range{Min: 50, Max: 100},
			EnableFailures: true,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			if op.Kind == OpInjectError {
				assert.NotEmpty(t, op.ErrorMsg, "OpInjectError should have an error message")
				assert.NotEmpty(t, op.TargetOperation, "OpInjectError should have a target operation")
			}
			if op.Kind == OpInjectLatency {
				assert.Greater(t, op.Latency.Milliseconds(), int64(0), "OpInjectLatency should have positive latency")
				assert.NotEmpty(t, op.TargetOperation, "OpInjectLatency should have a target operation")
			}
		}
	})
}

func TestGenerator_WithCloudChangesEnabled(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:      3,
			OperationCount:     Range{Min: 50, Max: 100},
			EnableCloudChanges: true,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			if op.Kind == OpCloudModify || op.Kind == OpCloudCreate {
				assert.NotEmpty(t, op.Properties, "cloud modify/create should have properties")
			}
			if op.Kind == OpCloudCreate {
				assert.NotEmpty(t, op.NativeID, "cloud create should have a native ID")
				assert.NotEmpty(t, op.ResourceType, "cloud create should have a resource type")
			}
		}
	})
}

func TestGenerator_ApplyModeIsValid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  5,
			OperationCount: Range{Min: 20, Max: 50},
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			if op.Kind == OpApply {
				assert.Contains(t, []string{"patch", "reconcile"}, op.ApplyMode,
					"ApplyMode should be 'patch' or 'reconcile'")
				assert.NotEmpty(t, op.ResourceIDs, "OpApply should have at least one resource")
			}
		}
	})
}
