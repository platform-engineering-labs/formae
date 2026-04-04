// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
	"pgregory.net/rapid"
)

func resourcePoolForConfig(config PropertyTestConfig) *ResourcePool {
	if config.ResourceCount%SlotsPerTree != 0 {
		return nil
	}
	if config.StackCount > 1 {
		return NewResourcePoolWithCrossStack(config.ResourceCount)
	}
	return NewResourcePool(config.ResourceCount)
}

func slotCountForConfig(config PropertyTestConfig, pool *ResourcePool) int {
	if pool != nil {
		return len(pool.Slots)
	}
	return config.ResourceCount
}

// OperationSequenceGen returns a rapid generator that produces a slice of
// operations whose kinds and parameters respect the given config.
func OperationSequenceGen(config PropertyTestConfig) *rapid.Generator[[]Operation] {
	return rapid.Custom(func(t *rapid.T) []Operation {
		count := rapid.IntRange(config.OperationCount.Min, config.OperationCount.Max).Draw(t, "count")
		ops := make([]Operation, count)
		for i := range ops {
			ops[i] = SingleOperationGen(config).Draw(t, fmt.Sprintf("op-%d", i))
		}

		// Enforce OpCrashAgent constraints: at most once, not first or last.
		crashSeen := false
		for i := range ops {
			if ops[i].Kind == OpCrashAgent {
				if crashSeen || i == 0 || i == len(ops)-1 {
					ops[i].Kind = OpVerifyState
				} else {
					crashSeen = true
				}
			}
		}

		return ops
	})
}

// SingleOperationGen returns a rapid generator that produces a single operation
// whose kind and parameters respect the given config.
func SingleOperationGen(config PropertyTestConfig) *rapid.Generator[Operation] {
	return rapid.Custom(func(t *rapid.T) Operation {
		kinds := allowedKinds(config)
		kindIdx := rapid.IntRange(0, len(kinds)-1).Draw(t, "kind")
		kind := kinds[kindIdx]

		op := Operation{Kind: kind}
		fillOperationFields(t, &op, config)
		return op
	})
}

// allowedKinds returns the set of operation kinds permitted by the config.
func allowedKinds(config PropertyTestConfig) []OperationKind {
	// Base operations always available
	kinds := []OperationKind{OpApply, OpDestroy, OpVerifyState, OpTriggerSync, OpTriggerDiscovery}

	if config.EnableCloudChanges {
		kinds = append(kinds, OpCloudModify, OpCloudDelete, OpCloudCreate)
	}
	if config.EnableCancel {
		kinds = append(kinds, OpCancel)
	}
	if config.EnableForceReconcile {
		kinds = append(kinds, OpForceReconcile)
	}
	if config.EnableTTL {
		kinds = append(kinds, OpCheckTTL, OpSetTTLPolicy)
	}
	if config.EnableCrashInjection {
		kinds = append(kinds, OpCrashAgent)
	}

	return kinds
}

// stackIndexGen generates a stack index in [0, stackCount).
// When stackCount <= 1, always returns 0.
func stackIndexGen(t *rapid.T, config PropertyTestConfig) int {
	if config.StackCount <= 1 {
		return 0
	}
	return rapid.IntRange(0, config.StackCount-1).Draw(t, "stackIdx")
}

// outcomeKey builds the map key for DrawnOutcomes.
func outcomeKey(stackIdx, slotIdx int) string {
	return fmt.Sprintf("%d:%d", stackIdx, slotIdx)
}

// pluginOpStepsGen draws a response sequence for a single plugin operation.
// Weighted: ~75% success, ~10% recoverable->success, ~10% irrecoverable, ~5% recoverable->fail.
//
// Retry semantics: with MaxRetries=N, the agent sets maxAttempts=N+1 and starts
// with attempts=1. The retry condition (data.attempts <= maxAttempts) allows
// retrying at attempts 1,2,3 — giving 4 total calls (initial + 3 retries).
// With MaxRetries=2 (harness config): survives up to 3 Throttling errors; need 4 to exhaust.
func pluginOpStepsGen(t *rapid.T, label string) []testcontrol.ResponseStep {
	roll := rapid.IntRange(0, 99).Draw(t, label)
	switch {
	case roll < 75: // success — empty steps (falls through to success)
		return nil
	case roll < 85: // recoverable -> success: 1-4 Throttling (within retry budget)
		retries := rapid.IntRange(1, maxSurvivableErrors).Draw(t, label+"-retries")
		steps := make([]testcontrol.ResponseStep, retries)
		for i := range steps {
			steps[i] = testcontrol.ResponseStep{ErrorCode: "Throttling"}
		}
		return steps
	case roll < 95: // irrecoverable failure
		return []testcontrol.ResponseStep{{ErrorCode: "AccessDenied"}}
	default: // recoverable -> exhaust retries
		steps := make([]testcontrol.ResponseStep, exhaustRetryCount)
		for i := range steps {
			steps[i] = testcontrol.ResponseStep{ErrorCode: "Throttling"}
		}
		return steps
	}
}

// Retry budget constants derived from the harness RetryConfig (MaxRetries=2).
// maxAttempts = MaxRetries + 1 = 3. The PluginOperator starts with attempts=1.
// The retry condition (data.attempts <= maxAttempts) allows retrying at
// attempts 1,2,3 — giving 4 total calls (initial + 3 retries).
// Can survive maxAttempts-1 = 3 recoverable errors (success on 4th call).
// 4 errors exhaust retries (fails on 4th attempt since 4 > maxAttempts).
const (
	harnessMaxRetries   = 2
	maxAttempts         = harnessMaxRetries + 1   // 3
	maxSurvivableErrors = maxAttempts             // 3 — agent survives and succeeds on 4th call
	exhaustRetryCount   = maxSurvivableErrors + 1 // 4 — agent exhausts retries and fails
)

// drawnOutcomeGen draws a complete outcome for one resource slot.
func drawnOutcomeGen(t *rapid.T, label string) DrawnOutcome {
	return DrawnOutcome{
		ReadSteps: pluginOpStepsGen(t, label+"-read"),
		CRUDSteps: pluginOpStepsGen(t, label+"-crud"),
	}
}

// fillOperationFields populates the kind-specific fields on the operation.
func fillOperationFields(t *rapid.T, op *Operation, config PropertyTestConfig) {
	pool := resourcePoolForConfig(config)
	slotCount := slotCountForConfig(config, pool)

	switch op.Kind {
	case OpApply:
		op.StackIndex = stackIndexGen(t, config)
		op.ApplyMode = applyModeGen(t, config)
		if pool != nil {
			// Reconcile mode declares complete desired state: always include full
			// ancestry so we don't generate impossible formas (children without
			// their parent in a reconcile would force a cascade-delete of the
			// parent while also keeping the children — a contradictory changeset).
			// Patch mode only applies what's specified, so a child-without-parent
			// is valid and exercises the clean-fail path.
			if op.ApplyMode == "reconcile" {
				op.ResourceIDs = resourceIDsGenWithPoolAncestry(t, pool, op.StackIndex, 1)
			} else {
				op.ResourceIDs = resourceIDsGenWithPool(t, pool, op.StackIndex, 1)
			}
			op.ChildProperties = childPropsGen(t)
		} else {
			op.ResourceIDs = resourceIDsGen(t, config.ResourceCount, 1)
		}
		op.Properties = resourcePropsGen(t)
		if config.EnableFailures {
			op.DrawnOutcomes = make(map[string]DrawnOutcome)
			for i := 0; i < slotCount; i++ {
				if pool != nil && op.StackIndex == 0 && pool.IsCrossStack(i) {
					continue
				}
				op.DrawnOutcomes[outcomeKey(op.StackIndex, i)] = drawnOutcomeGen(t, fmt.Sprintf("outcome-%d", i))
			}
		}

	case OpDestroy:
		op.StackIndex = stackIndexGen(t, config)
		if pool != nil {
			op.ResourceIDs = resourceIDsGenWithPool(t, pool, op.StackIndex, 1)
			op.OnDependents = onDependentsGen(t)
		} else {
			op.ResourceIDs = resourceIDsGen(t, config.ResourceCount, 1)
		}
		if config.EnableFailures {
			op.DrawnOutcomes = make(map[string]DrawnOutcome)
			for i := 0; i < slotCount; i++ {
				if pool != nil && op.StackIndex == 0 && pool.IsCrossStack(i) {
					continue
				}
				op.DrawnOutcomes[outcomeKey(op.StackIndex, i)] = drawnOutcomeGen(t, fmt.Sprintf("outcome-%d", i))
			}
			// For cascade destroys, also draw outcomes for other stacks
			if op.OnDependents == "cascade" {
				for s := 0; s < config.StackCount; s++ {
					if s == op.StackIndex {
						continue
					}
					for i := 0; i < slotCount; i++ {
						if pool != nil && s == 0 && pool.IsCrossStack(i) {
							continue
						}
						op.DrawnOutcomes[outcomeKey(s, i)] = drawnOutcomeGen(t, fmt.Sprintf("cascade-%d-%d", s, i))
					}
				}
			}
		}

	case OpCancel:
		// CommandID is set during execution, nothing to generate

	case OpTriggerSync, OpTriggerDiscovery, OpVerifyState:
		// No additional fields needed

	case OpCloudModify:
		op.CloudTargetManaged = rapid.Bool().Draw(t, "cloudTargetManaged")
		op.NativeID = cloudNativeIDGen(t)
		op.Properties = cloudPropertiesGen(t)

	case OpCloudDelete:
		op.CloudTargetManaged = rapid.Bool().Draw(t, "cloudTargetManaged")
		op.NativeID = cloudNativeIDGen(t)

	case OpForceReconcile:
		op.StackIndex = stackIndexGen(t, config)
		if config.EnableFailures {
			op.DrawnOutcomes = make(map[string]DrawnOutcome)
			for i := 0; i < slotCount; i++ {
				if pool != nil && op.StackIndex == 0 && pool.IsCrossStack(i) {
					continue
				}
				op.DrawnOutcomes[outcomeKey(op.StackIndex, i)] = drawnOutcomeGen(t, fmt.Sprintf("outcome-%d", i))
			}
		}

	case OpCheckTTL:
		// No additional fields needed

	case OpSetTTLPolicy:
		op.StackIndex = stackIndexGen(t, config)
		op.TTLExpired = rapid.Bool().Draw(t, "ttlExpired")

	case OpCloudCreate:
		op.NativeID = cloudNativeIDGen(t)
		op.Properties = cloudPropertiesGen(t)
		op.ResourceType = "Test::Generic::Resource"
		if pool != nil {
			tupleSize := rapid.IntRange(1, 3).Draw(t, "tupleSize")
			if tupleSize >= 2 {
				parentName := extractNameFromProps(op.Properties)
				childNativeID := op.NativeID + "-child-0"
				childProps := cloudChildPropsGen(t, parentName)
				op.CloudChildren = append(op.CloudChildren, CloudChildResource{
					NativeID:     childNativeID,
					ResourceType: "Test::Generic::ChildResource",
					Properties:   childProps,
				})
				if tupleSize >= 3 {
					childName := extractNameFromProps(childProps)
					gcNativeID := op.NativeID + "-gc-0"
					gcProps := cloudChildPropsGen(t, childName)
					op.CloudChildren = append(op.CloudChildren, CloudChildResource{
						NativeID:     gcNativeID,
						ResourceType: "Test::Generic::GrandchildResource",
						Properties:   gcProps,
					})
				}
			}
		}
	}
}

// resourceIDsGen generates a non-empty slice of unique resource indices in [0, poolSize).
func resourceIDsGen(t *rapid.T, poolSize int, minCount int) []int {
	count := rapid.IntRange(minCount, poolSize).Draw(t, "resCount")

	// Generate unique indices by drawing from a shuffled pool
	all := make([]int, poolSize)
	for i := range all {
		all[i] = i
	}
	// Fisher-Yates shuffle using rapid
	for i := poolSize - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("shuffle-%d", i))
		all[i], all[j] = all[j], all[i]
	}
	return all[:count]
}

// resourceIDsGenWithPool generates resource indices from the tree pool.
// The drawn set is returned as-is, without expanding to include ancestors.
// Use this for patch-mode applies where a child-without-parent is valid input.
func resourceIDsGenWithPool(t *rapid.T, pool *ResourcePool, stackIndex, minCount int) []int {
	all := selectablePoolIndices(pool, stackIndex)
	count := rapid.IntRange(minCount, len(all)).Draw(t, "resCount")

	for i := len(all) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("shuffle-%d", i))
		all[i], all[j] = all[j], all[i]
	}

	result := make([]int, count)
	copy(result, all[:count])
	sort.Ints(result)
	return result
}

// resourceIDsGenWithPoolAncestry generates resource indices from the tree pool,
// expanding the drawn set to include the full ancestry chain for every slot.
// Use this for reconcile-mode applies: reconcile declares complete desired state,
// so including a child without its parent would force a cascade-delete of the
// parent while simultaneously keeping the child — a contradictory changeset.
func resourceIDsGenWithPoolAncestry(t *rapid.T, pool *ResourcePool, stackIndex, minCount int) []int {
	all := selectablePoolIndices(pool, stackIndex)
	count := rapid.IntRange(minCount, len(all)).Draw(t, "resCount")

	for i := len(all) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("shuffle-%d", i))
		all[i], all[j] = all[j], all[i]
	}
	drawn := all[:count]

	selected := make(map[int]bool)
	for _, idx := range drawn {
		for _, ancestor := range pool.AncestryChain(idx) {
			selected[ancestor] = true
		}
	}

	result := make([]int, 0, len(selected))
	for idx := range selected {
		result = append(result, idx)
	}
	sort.Ints(result)
	return result
}

func selectablePoolIndices(pool *ResourcePool, stackIndex int) []int {
	all := make([]int, 0, len(pool.Slots))
	for i := range pool.Slots {
		if stackIndex == 0 && pool.IsCrossStack(i) {
			continue
		}
		all = append(all, i)
	}
	return all
}

// childPropsGen generates a JSON properties string for child/grandchild resources.
// Name and ParentId are placeholders replaced during forma building.
func childPropsGen(t *rapid.T) string {
	value := scalarValues[rapid.IntRange(0, len(scalarValues)-1).Draw(t, "childValue")]
	props := map[string]any{
		"Name":     "NAME",
		"ParentId": "PARENT_ID",
		"Value":    value,
	}
	b, _ := json.Marshal(props)
	return string(b)
}

// onDependentsGen generates the on-dependents mode for destroy operations.
func onDependentsGen(t *rapid.T) string {
	modes := []string{"abort", "cascade"}
	return modes[rapid.IntRange(0, len(modes)-1).Draw(t, "onDependents")]
}

func applyModeGen(t *rapid.T, config PropertyTestConfig) string {
	modes := []string{"patch", "reconcile"}
	return modes[rapid.IntRange(0, len(modes)-1).Draw(t, "mode")]
}

func cloudNativeIDGen(t *rapid.T) string {
	return fmt.Sprintf("cloud-%d", rapid.IntRange(1, 100).Draw(t, "nativeID"))
}

func cloudPropertiesGen(t *rapid.T) string {
	name := fmt.Sprintf("cloud-res-%d", rapid.IntRange(1, 100).Draw(t, "propName"))
	return strings.Replace(resourcePropsGen(t), `"NAME"`, `"`+name+`"`, 1)
}

// extractNameFromProps parses a properties JSON string and returns the "Name" field value.
func extractNameFromProps(propsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(propsJSON), &m); err != nil {
		panic(fmt.Sprintf("extractNameFromProps: invalid JSON: %v", err))
	}
	name, ok := m["Name"].(string)
	if !ok {
		panic(fmt.Sprintf("extractNameFromProps: missing or non-string Name in %s", propsJSON))
	}
	return name
}

// cloudChildPropsGen generates cloud state properties for a child/grandchild resource.
// The ParentId is set to a plain string (not a resolvable) matching the parent's Name.
func cloudChildPropsGen(t *rapid.T, parentName string) string {
	value := scalarValues[rapid.IntRange(0, len(scalarValues)-1).Draw(t, "cloudChildValue")]
	name := fmt.Sprintf("cloud-child-%d", rapid.IntRange(1, 100).Draw(t, "cloudChildName"))
	props := map[string]any{
		"Name":     name,
		"ParentId": parentName,
		"Value":    value,
	}
	b, _ := json.Marshal(props)
	return string(b)
}

// --- Property generators ---

// Vocabularies -- small sets for effective rapid shrinking.
var (
	scalarValues    = []string{"v1", "v2", "v3", "v4"}
	setTagValues    = []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	entityKeyValues = []string{"env", "team", "cost-center", "owner"}
	entityValValues = []string{"a", "b", "c"}
	orderedValues   = []string{"first", "second", "third", "fourth"}
)

// resourcePropsGen generates a JSON properties string with all field types.
// The Name field is left as a placeholder "NAME" to be replaced by the caller.
func resourcePropsGen(t *rapid.T) string {
	value := scalarValues[rapid.IntRange(0, len(scalarValues)-1).Draw(t, "value")]
	setTags := subsetGen(t, setTagValues, "setTag")
	entityTags := entityTagsGen(t)
	orderedItems := subsequenceGen(t, orderedValues, "orderedItem")

	props := map[string]any{
		"Name":         "NAME",
		"Value":        value,
		"SetTags":      setTags,
		"EntityTags":   entityTags,
		"OrderedItems": orderedItems,
	}

	b, _ := json.Marshal(props)
	return string(b)
}

// subsetGen draws a random subset (possibly empty) from the given values.
func subsetGen(t *rapid.T, values []string, label string) []string {
	count := rapid.IntRange(0, len(values)).Draw(t, label+"Count")
	if count == 0 {
		return []string{}
	}
	// Shuffle and take first N
	indices := make([]int, len(values))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("%sShuffle-%d", label, i))
		indices[i], indices[j] = indices[j], indices[i]
	}
	result := make([]string, count)
	for i := range count {
		result[i] = values[indices[i]]
	}
	return result
}

// entityTagsGen generates a slice of {"Key":..., "Value":...} objects
// with unique keys drawn from the entity key vocabulary.
func entityTagsGen(t *rapid.T) []map[string]string {
	count := rapid.IntRange(0, len(entityKeyValues)).Draw(t, "entityTagCount")
	if count == 0 {
		return []map[string]string{}
	}
	// Shuffle keys and take first N for uniqueness
	indices := make([]int, len(entityKeyValues))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("entityKeyShuffle-%d", i))
		indices[i], indices[j] = indices[j], indices[i]
	}
	result := make([]map[string]string, count)
	for i := range count {
		key := entityKeyValues[indices[i]]
		val := entityValValues[rapid.IntRange(0, len(entityValValues)-1).Draw(t, fmt.Sprintf("entityVal-%d", i))]
		result[i] = map[string]string{"Key": key, "Value": val}
	}
	return result
}

// subsequenceGen draws an ordered subsequence (possibly empty) from the given
// values, preserving the original order.
func subsequenceGen(t *rapid.T, values []string, label string) []string {
	// Each element is independently included or not
	var result []string
	for i, v := range values {
		if rapid.Bool().Draw(t, fmt.Sprintf("%s-%d", label, i)) {
			result = append(result, v)
		}
	}
	if result == nil {
		return []string{}
	}
	return result
}
