// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"sort"

	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// cascadeDeletePlan keeps the submitted roots separate from the operations the
// agent expands from them. The same plan drives injection and model prediction.
type cascadeDeletePlan struct {
	affected   []ResourceSlotRef
	attempted  []ResourceSlotRef
	successful []ResourceSlotRef
}

func planCascadeDeletes(op *Operation, model *StateModel, roots []int) cascadeDeletePlan {
	return planDeletes(op, model, roots, true)
}

func planDeletes(op *Operation, model *StateModel, roots []int, cascade bool) cascadeDeletePlan {
	var plan cascadeDeletePlan
	selected := make(map[ResourceSlotRef]bool)
	for _, root := range roots {
		selected[ResourceSlotRef{op.StackIndex, root}] = true
	}
	visited := make(map[ResourceSlotRef]bool)
	succeeded := make(map[ResourceSlotRef]bool)
	var visit func(ResourceSlotRef) bool
	visit = func(ref ResourceSlotRef) bool {
		if visited[ref] {
			return succeeded[ref]
		}
		visited[ref] = true
		dependenciesOK := true
		if model.Pool != nil {
			for _, child := range model.Pool.Slots[ref.SlotIndex].ChildIndices {
				if !visit(ResourceSlotRef{ref.StackIndex, child}) {
					dependenciesOK = false
				}
			}
			if ref.StackIndex == 0 && model.ProviderStackLabel != "" {
				for si := 1; si < len(model.Stacks); si++ {
					for _, child := range model.Pool.CrossStackDependents(ref.SlotIndex) {
						if !visit(ResourceSlotRef{si, child}) {
							dependenciesOK = false
						}
					}
				}
			}
		}
		if !cascade && !selected[ref] {
			succeeded[ref] = dependenciesOK
			return dependenciesOK
		}
		res := model.Resource(ref.StackIndex, ref.SlotIndex)
		if res == nil || res.State != StateExists {
			succeeded[ref] = dependenciesOK
			return dependenciesOK
		}
		plan.affected = append(plan.affected, ref)
		if !dependenciesOK {
			return false
		}
		plan.attempted = append(plan.attempted, ref)
		outcome := op.DrawnOutcomes[outcomeKey(ref.StackIndex, ref.SlotIndex)]
		succeeded[ref] = willOperationSucceed(outcome.ReadSteps) && willOperationSucceed(outcome.CRUDSteps)
		if succeeded[ref] {
			plan.successful = append(plan.successful, ref)
		}
		return succeeded[ref]
	}
	for _, root := range roots {
		visit(ResourceSlotRef{op.StackIndex, root})
	}
	return plan
}

func (plan cascadeDeletePlan) sequences(op *Operation, model *StateModel) []testcontrol.PluginOpSequence {
	var sequences []testcontrol.PluginOpSequence
	for _, ref := range plan.attempted {
		sequences = append(sequences, buildPluginOpSequences(op.DrawnOutcomes, ref.StackIndex, model.Stack(ref.StackIndex).Label, []int{ref.SlotIndex}, model, true, model.Pool)...)
	}
	return sequences
}

func (plan cascadeDeletePlan) apply(model *StateModel) {
	for _, ref := range plan.successful {
		model.ApplyDestroyed(ref.StackIndex, []int{ref.SlotIndex})
	}
}

func omittedResourceIDs(model *StateModel, stackIndex int, kept []int) []int {
	keep := make(map[int]bool, len(kept))
	for _, id := range kept {
		keep[id] = true
	}
	var omitted []int
	for id, res := range model.Stack(stackIndex).Resources {
		if !keep[id] && res.State == StateExists {
			omitted = append(omitted, id)
		}
	}
	sort.Ints(omitted)
	return omitted
}
