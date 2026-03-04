// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"fmt"
	"slices"
)

// ResourceSlot represents a single resource in the tree pool.
type ResourceSlot struct {
	Index        int
	Type         string
	Label        string // suffix only (e.g. "a", "a-0"); LabelForStack composes the full label
	ParentIndex  int    // -1 if no parent
	ChildIndices []int
}

// TreeConfig defines the fan-out of the resource tree.
// With ChildrenPerParent=2 and GrandchildrenPerChild=1, one tree = 5 slots.
const (
	ChildrenPerParent     = 2
	GrandchildrenPerChild = 1
	SlotsPerTree          = 1 + ChildrenPerParent + ChildrenPerParent*GrandchildrenPerChild // 5
)

// ResourcePool is a fixed-size pool of resource slots organized as trees.
type ResourcePool struct {
	Slots     []ResourceSlot
	TreeCount int
}

// NewResourcePool creates a resource pool with the given total size.
// totalSize must be a multiple of SlotsPerTree (5).
func NewResourcePool(totalSize int) *ResourcePool {
	if totalSize%SlotsPerTree != 0 {
		panic(fmt.Sprintf("resource pool size %d must be a multiple of %d", totalSize, SlotsPerTree))
	}
	treeCount := totalSize / SlotsPerTree

	slots := make([]ResourceSlot, totalSize)
	for tree := 0; tree < treeCount; tree++ {
		base := tree * SlotsPerTree
		parentIdx := base
		parentSuffix := string(rune('a' + tree))

		// Parent
		slots[parentIdx] = ResourceSlot{
			Index:       parentIdx,
			Type:        "Test::Generic::Resource",
			Label:       parentSuffix,
			ParentIndex: -1,
		}

		for c := 0; c < ChildrenPerParent; c++ {
			childIdx := base + 1 + c*(1+GrandchildrenPerChild)
			childSuffix := fmt.Sprintf("%s-%d", parentSuffix, c)

			slots[childIdx] = ResourceSlot{
				Index:       childIdx,
				Type:        "Test::Generic::ChildResource",
				Label:       childSuffix,
				ParentIndex: parentIdx,
			}
			slots[parentIdx].ChildIndices = append(slots[parentIdx].ChildIndices, childIdx)

			for g := 0; g < GrandchildrenPerChild; g++ {
				gcIdx := childIdx + 1 + g
				gcSuffix := fmt.Sprintf("%s-%d", childSuffix, g)

				slots[gcIdx] = ResourceSlot{
					Index:       gcIdx,
					Type:        "Test::Generic::GrandchildResource",
					Label:       gcSuffix,
					ParentIndex: childIdx,
				}
				slots[childIdx].ChildIndices = append(slots[childIdx].ChildIndices, gcIdx)
			}
		}
	}

	return &ResourcePool{Slots: slots, TreeCount: treeCount}
}

// IsParent returns true if the slot is a top-level parent resource.
func (p *ResourcePool) IsParent(idx int) bool {
	return p.Slots[idx].ParentIndex == -1
}

// IsChild returns true if the slot is a child resource.
func (p *ResourcePool) IsChild(idx int) bool {
	return p.Slots[idx].Type == "Test::Generic::ChildResource"
}

// IsGrandchild returns true if the slot is a grandchild resource.
func (p *ResourcePool) IsGrandchild(idx int) bool {
	return p.Slots[idx].Type == "Test::Generic::GrandchildResource"
}

// AncestryChain returns the full ancestry for a slot (including itself),
// ordered from root parent to the slot itself.
func (p *ResourcePool) AncestryChain(idx int) []int {
	var chain []int
	current := idx
	for current != -1 {
		chain = append(chain, current)
		current = p.Slots[current].ParentIndex
	}
	slices.Reverse(chain)
	return chain
}

// AllDescendants returns all descendants of a slot (not including itself).
func (p *ResourcePool) AllDescendants(idx int) []int {
	var desc []int
	for _, childIdx := range p.Slots[idx].ChildIndices {
		desc = append(desc, childIdx)
		desc = append(desc, p.AllDescendants(childIdx)...)
	}
	return desc
}

// HasDescendants returns true if the slot has any children or grandchildren.
func (p *ResourcePool) HasDescendants(idx int) bool {
	return len(p.Slots[idx].ChildIndices) > 0
}

// LabelForStack returns the full label for a resource on a specific stack.
// The prefix is derived from the resource type to match the naming convention:
//   - Parent:      "res-{stack}-{suffix}"       e.g. "res-stack-0-a"
//   - Child:       "child-{stack}-{suffix}"      e.g. "child-stack-0-a-0"
//   - Grandchild:  "grandchild-{stack}-{suffix}" e.g. "grandchild-stack-0-a-0-0"
func (p *ResourcePool) LabelForStack(stackLabel string, idx int) string {
	slot := p.Slots[idx]
	switch slot.Type {
	case "Test::Generic::Resource":
		return fmt.Sprintf("res-%s-%s", stackLabel, slot.Label)
	case "Test::Generic::ChildResource":
		return fmt.Sprintf("child-%s-%s", stackLabel, slot.Label)
	case "Test::Generic::GrandchildResource":
		return fmt.Sprintf("grandchild-%s-%s", stackLabel, slot.Label)
	default:
		return fmt.Sprintf("%s-%s", stackLabel, slot.Label)
	}
}

// ParentLabelForStack returns the label of the parent resource on the stack.
// Panics if the slot has no parent.
func (p *ResourcePool) ParentLabelForStack(stackLabel string, idx int) string {
	parentIdx := p.Slots[idx].ParentIndex
	if parentIdx == -1 {
		panic(fmt.Sprintf("slot %d has no parent", idx))
	}
	return p.LabelForStack(stackLabel, parentIdx)
}

// ParentType returns the resource type of the parent.
func (p *ResourcePool) ParentType(idx int) string {
	parentIdx := p.Slots[idx].ParentIndex
	if parentIdx == -1 {
		panic(fmt.Sprintf("slot %d has no parent", idx))
	}
	return p.Slots[parentIdx].Type
}
