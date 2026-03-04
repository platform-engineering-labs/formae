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
	"time"

	"pgregory.net/rapid"
)

// OperationSequenceGen returns a rapid generator that produces a slice of
// operations whose kinds and parameters respect the given config.
func OperationSequenceGen(config PropertyTestConfig) *rapid.Generator[[]Operation] {
	return rapid.Custom(func(t *rapid.T) []Operation {
		count := rapid.IntRange(config.OperationCount.Min, config.OperationCount.Max).Draw(t, "count")
		ops := make([]Operation, count)
		for i := range ops {
			ops[i] = SingleOperationGen(config).Draw(t, fmt.Sprintf("op-%d", i))
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

	if config.EnableFailures {
		kinds = append(kinds, OpInjectError, OpInjectLatency, OpClearInjections)
	}
	if config.EnableCloudChanges {
		kinds = append(kinds, OpCloudModify, OpCloudDelete, OpCloudCreate)
	}
	if config.EnableCancel {
		kinds = append(kinds, OpCancel)
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

// fillOperationFields populates the kind-specific fields on the operation.
func fillOperationFields(t *rapid.T, op *Operation, config PropertyTestConfig) {
	// Create pool once for tree-aware resource generation.
	var pool *ResourcePool
	if config.ResourceCount%SlotsPerTree == 0 {
		pool = NewResourcePool(config.ResourceCount)
	}

	switch op.Kind {
	case OpApply:
		op.StackIndex = stackIndexGen(t, config)
		if pool != nil {
			op.ResourceIDs = resourceIDsGenWithPool(t, pool, 1)
			op.ChildProperties = childPropsGen(t)
		} else {
			op.ResourceIDs = resourceIDsGen(t, config.ResourceCount, 1)
		}
		op.ApplyMode = applyModeGen(t, config)
		op.Blocking = blockingGen(t, config)
		op.Properties = resourcePropsGen(t)

	case OpDestroy:
		op.StackIndex = stackIndexGen(t, config)
		if pool != nil {
			op.ResourceIDs = resourceIDsGenWithPool(t, pool, 1)
			op.OnDependents = onDependentsGen(t)
		} else {
			op.ResourceIDs = resourceIDsGen(t, config.ResourceCount, 1)
		}
		op.Blocking = blockingGen(t, config)

	case OpCancel:
		// CommandID is set during execution, nothing to generate

	case OpTriggerSync, OpTriggerDiscovery, OpVerifyState:
		// No additional fields needed

	case OpInjectError:
		op.TargetOperation = pluginOperationGen(t)
		op.ErrorMsg = errorMsgGen(t)
		op.ErrorCount = rapid.IntRange(0, 5).Draw(t, "errorCount")

	case OpInjectLatency:
		op.TargetOperation = pluginOperationGen(t)
		op.Latency = time.Duration(rapid.IntRange(50, 5000).Draw(t, "latencyMs")) * time.Millisecond

	case OpClearInjections:
		// No additional fields needed

	case OpCloudModify:
		op.NativeID = cloudNativeIDGen(t)
		op.Properties = cloudPropertiesGen(t)

	case OpCloudDelete:
		op.NativeID = cloudNativeIDGen(t)

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

// resourceIDsGenWithPool generates resource indices from the tree pool,
// ensuring full ancestry is included for any drawn child/grandchild.
func resourceIDsGenWithPool(t *rapid.T, pool *ResourcePool, minCount int) []int {
	count := rapid.IntRange(minCount, len(pool.Slots)).Draw(t, "resCount")

	// Draw from shuffled pool
	all := make([]int, len(pool.Slots))
	for i := range all {
		all[i] = i
	}
	for i := len(all) - 1; i > 0; i-- {
		j := rapid.IntRange(0, i).Draw(t, fmt.Sprintf("shuffle-%d", i))
		all[i], all[j] = all[j], all[i]
	}
	drawn := all[:count]

	// Expand to include full ancestry
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

func blockingGen(t *rapid.T, config PropertyTestConfig) bool {
	if !config.EnableConcurrency {
		return true
	}
	return rapid.Bool().Draw(t, "blocking")
}

func pluginOperationGen(t *rapid.T) string {
	ops := []string{"Create", "Read", "Update", "Delete"}
	return ops[rapid.IntRange(0, len(ops)-1).Draw(t, "pluginOp")]
}


func errorMsgGen(t *rapid.T) string {
	msgs := []string{
		"injected-transient-error",
		"injected-permanent-error",
		"injected-throttle-error",
		"injected-not-found-error",
	}
	return msgs[rapid.IntRange(0, len(msgs)-1).Draw(t, "errorMsg")]
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
