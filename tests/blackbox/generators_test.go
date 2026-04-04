// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
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
			assert.Nil(t, op.DrawnOutcomes, "DrawnOutcomes should be nil when EnableFailures=false")
		}
	})
}

func TestGenerator_RespectsConfig_NoCloudChanges(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		config := PropertyTestConfig{
			ResourceCount:      3,
			OperationCount:     Range{Min: 10, Max: 30},
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

		// With EnableFailures, apply/destroy ops should have DrawnOutcomes.
		for _, op := range ops {
			if op.Kind == OpApply || op.Kind == OpDestroy {
				assert.NotNil(t, op.DrawnOutcomes, "apply/destroy with EnableFailures should have DrawnOutcomes")
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

func TestGenerator_MultiStackUsesCrossStackPool(t *testing.T) {
	pool := resourcePoolForConfig(PropertyTestConfig{ResourceCount: 5, StackCount: 3})
	require.NotNil(t, pool)
	assert.Equal(t, 7, len(pool.Slots))
	assert.True(t, pool.IsCrossStack(5))
	assert.True(t, pool.IsCrossStack(6))
}

func TestGenerator_ProviderStackExcludesCrossStackSlots(t *testing.T) {
	pool := NewResourcePoolWithCrossStack(5)
	rapid.Check(t, func(rt *rapid.T) {
		ids := resourceIDsGenWithPool(rt, pool, 0, 1)
		for _, id := range ids {
			assert.False(t, pool.IsCrossStack(id), "provider stack should not draw cross-stack slots")
		}
	})
}

func TestGenerator_ConsumerStacksCanDrawCrossStackSlots(t *testing.T) {
	pool := NewResourcePoolWithCrossStack(5)
	rapid.Check(t, func(rt *rapid.T) {
		foundCrossStack := false
		for i := 0; i < 25; i++ {
			ids := resourceIDsGenWithPool(rt, pool, 1, 1)
			for _, id := range ids {
				if pool.IsCrossStack(id) {
					foundCrossStack = true
					break
				}
			}
			if foundCrossStack {
				break
			}
		}
		assert.True(t, foundCrossStack, "consumer stacks should be able to draw cross-stack slots")
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

// --- Response Sequence Generator Tests ---

func TestResponseSequenceGen(t *testing.T) {
	t.Run("pluginOpStepsGen_covers_all_four_outcomes", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Draw 200 outcomes and verify we see all four categories.
			var (
				sawSuccess       bool
				sawRecoverable   bool
				sawIrrecoverable bool
				sawExhaust       bool
			)
			for i := 0; i < 200; i++ {
				steps := pluginOpStepsGen(rt, fmt.Sprintf("step-%d", i))
				switch {
				case steps == nil:
					sawSuccess = true
				case len(steps) == 1 && steps[0].ErrorCode == "AccessDenied":
					sawIrrecoverable = true
				case len(steps) == 4 && steps[0].ErrorCode == "Throttling":
					sawExhaust = true
				case len(steps) >= 1 && len(steps) <= 3 && steps[0].ErrorCode == "Throttling":
					sawRecoverable = true
				}
			}
			// With 200 draws, probability of missing any bucket is vanishingly small.
			assert.True(t, sawSuccess, "should see success outcome (nil steps)")
			assert.True(t, sawRecoverable, "should see recoverable->success outcome (1-3 Throttling)")
			assert.True(t, sawIrrecoverable, "should see irrecoverable outcome (AccessDenied)")
			assert.True(t, sawExhaust, "should see exhaust-retries outcome (4 Throttling)")
		})
	})

	t.Run("drawnOutcomeGen_produces_ReadSteps_and_CRUDSteps", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			outcome := drawnOutcomeGen(rt, "test")
			// ReadSteps and CRUDSteps may be nil (success) or non-nil;
			// the type itself should always be valid.
			_ = outcome.ReadSteps
			_ = outcome.CRUDSteps

			// Verify that the steps, when non-nil, contain valid ResponseStep values.
			for _, step := range outcome.ReadSteps {
				assert.IsType(t, testcontrol.ResponseStep{}, step)
			}
			for _, step := range outcome.CRUDSteps {
				assert.IsType(t, testcontrol.ResponseStep{}, step)
			}
		})
	})

	t.Run("OpApply_with_EnableFailures_has_DrawnOutcomes", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  5,
				OperationCount: Range{Min: 1, Max: 1},
				EnableFailures: true,
				StackCount:     2,
			}
			op := Operation{Kind: OpApply}
			fillOperationFields(rt, &op, config)

			require.NotNil(t, op.DrawnOutcomes, "DrawnOutcomes should be non-nil when EnableFailures=true")
			// Should have entries for all resource slots in the target stack.
			for i := 0; i < config.ResourceCount; i++ {
				key := outcomeKey(op.StackIndex, i)
				_, exists := op.DrawnOutcomes[key]
				assert.True(t, exists, "DrawnOutcomes should have entry for key %s", key)
			}
		})
	})

	t.Run("OpApply_without_EnableFailures_has_nil_DrawnOutcomes", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  5,
				OperationCount: Range{Min: 1, Max: 1},
				EnableFailures: false,
				StackCount:     2,
			}
			op := Operation{Kind: OpApply}
			fillOperationFields(rt, &op, config)

			assert.Nil(t, op.DrawnOutcomes, "DrawnOutcomes should be nil when EnableFailures=false")
		})
	})

	t.Run("cascade_destroy_draws_outcomes_for_all_stacks", func(t *testing.T) {
		// Use a config with ResourceCount=5, StackCount=3 and force cascade.
		// We generate many OpDestroy operations and collect any that have cascade+EnableFailures.
		rapid.Check(t, func(rt *rapid.T) {
			config := PropertyTestConfig{
				ResourceCount:  5,
				OperationCount: Range{Min: 1, Max: 1},
				EnableFailures: true,
				StackCount:     3,
			}
			pool := resourcePoolForConfig(config)
			slotCount := slotCountForConfig(config, pool)

			// We need a pool for onDependents to be set.
			op := Operation{Kind: OpDestroy}
			fillOperationFields(rt, &op, config)

			require.NotNil(t, op.DrawnOutcomes, "DrawnOutcomes should be non-nil for OpDestroy with EnableFailures")

			if op.OnDependents == "cascade" {
				// Cascade destroy should have entries for ALL stacks.
				for s := 0; s < config.StackCount; s++ {
					for i := 0; i < slotCount; i++ {
						if pool != nil && s == 0 && pool.IsCrossStack(i) {
							continue
						}
						key := outcomeKey(s, i)
						_, exists := op.DrawnOutcomes[key]
						assert.True(t, exists, "cascade destroy should have entry for key %s", key)
					}
				}
				expected := slotCount * (config.StackCount - 1)
				if pool != nil {
					expected += config.ResourceCount
				} else {
					expected += slotCount
				}
				assert.Equal(t, expected, len(op.DrawnOutcomes),
					"cascade destroy should have entries for all stacks * resources")
			} else {
				// Non-cascade destroy should only have entries for the target stack.
				expected := slotCount
				if pool != nil && op.StackIndex == 0 {
					expected = config.ResourceCount
				}
				assert.Equal(t, expected, len(op.DrawnOutcomes),
					"non-cascade destroy should have entries only for target stack")
			}
		})
	})
}
