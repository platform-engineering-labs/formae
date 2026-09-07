// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

// ReferencedTargetLabels returns the distinct target labels referenced by the
// given resource ops (a resource's DesiredState.Target and its ResourceTarget.Label),
// skipping empty labels, in first-seen order for a stable result.
//
// This is the input the resolve-op synthesis needs to decide which unchanged
// targets a command touches and may therefore have to resolve before dispatch.
func ReferencedTargetLabels(resourceUpdates []ResourceUpdate) []string {
	seen := make(map[string]bool)
	var labels []string
	consider := func(label string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		labels = append(labels, label)
	}
	for i := range resourceUpdates {
		ru := &resourceUpdates[i]
		consider(ru.DesiredState.Target)
		consider(ru.ResourceTarget.Label)
	}
	return labels
}

// SourceTargetByKsuid indexes this command's resource ops by KSUID to their
// target label, so a secret source being created in the same command resolves to
// its target without a persisted row. Resources with an empty KSUID are skipped.
func SourceTargetByKsuid(resourceUpdates []ResourceUpdate) map[string]string {
	byKsuid := make(map[string]string, len(resourceUpdates))
	for i := range resourceUpdates {
		ru := &resourceUpdates[i]
		if k := ru.DesiredState.Ksuid; k != "" {
			byKsuid[k] = ru.DesiredState.Target
		}
	}
	return byKsuid
}
