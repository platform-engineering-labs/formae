// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestGenerator_StackIndexWithinBounds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:  3,
			StackCount:     3,
			OperationCount: Range{Min: 20, Max: 50},
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		for _, op := range ops {
			if op.Kind == OpApply || op.Kind == OpDestroy {
				assert.GreaterOrEqual(t, op.StackIndex, 0, "StackIndex should be >= 0")
				assert.Less(t, op.StackIndex, config.StackCount, "StackIndex should be < StackCount")
			}
		}
	})
}

func TestGenerator_ConcurrencyProducesNonBlocking(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:     3,
			StackCount:        2,
			OperationCount:    Range{Min: 50, Max: 100},
			EnableConcurrency: true,
		}
		ops := OperationSequenceGen(config).Draw(rt, "ops")

		hasNonBlocking := false
		for _, op := range ops {
			if (op.Kind == OpApply || op.Kind == OpDestroy) && !op.Blocking {
				hasNonBlocking = true
				break
			}
		}
		assert.True(t, hasNonBlocking, "with EnableConcurrency, should produce at least one non-blocking operation in 50+ ops")
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

func TestGenerator_PropertiesAreValidJSON(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		props := resourcePropsGen(rt)

		// Must be valid JSON
		var parsed map[string]any
		err := json.Unmarshal([]byte(props), &parsed)
		require.NoError(t, err, "properties must be valid JSON: %s", props)

		// Must have all required fields
		assert.Contains(t, parsed, "Name", "must have Name field")
		assert.Contains(t, parsed, "Value", "must have Value field")
		assert.Contains(t, parsed, "SetTags", "must have SetTags field")
		assert.Contains(t, parsed, "EntityTags", "must have EntityTags field")
		assert.Contains(t, parsed, "OrderedItems", "must have OrderedItems field")

		// SetTags must be a string array
		setTags, ok := parsed["SetTags"].([]any)
		assert.True(t, ok, "SetTags must be an array")
		for _, tag := range setTags {
			_, isString := tag.(string)
			assert.True(t, isString, "SetTags elements must be strings")
		}

		// EntityTags must be array of objects with Key and Value
		entityTags, ok := parsed["EntityTags"].([]any)
		assert.True(t, ok, "EntityTags must be an array")
		for _, tag := range entityTags {
			obj, isObj := tag.(map[string]any)
			assert.True(t, isObj, "EntityTags elements must be objects")
			if isObj {
				assert.Contains(t, obj, "Key")
				assert.Contains(t, obj, "Value")
			}
		}

		// OrderedItems must be a string array
		orderedItems, ok := parsed["OrderedItems"].([]any)
		assert.True(t, ok, "OrderedItems must be an array")
		for _, item := range orderedItems {
			_, isString := item.(string)
			assert.True(t, isString, "OrderedItems elements must be strings")
		}
	})
}

func TestGenerator_PropertiesEntityTagsHaveUniqueKeys(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		props := resourcePropsGen(rt)

		var parsed map[string]any
		err := json.Unmarshal([]byte(props), &parsed)
		require.NoError(t, err)

		entityTags := parsed["EntityTags"].([]any)
		keys := make(map[string]bool)
		for _, tag := range entityTags {
			obj := tag.(map[string]any)
			key := obj["Key"].(string)
			assert.False(t, keys[key], "EntityTags keys must be unique, duplicate: %s", key)
			keys[key] = true
		}
	})
}
