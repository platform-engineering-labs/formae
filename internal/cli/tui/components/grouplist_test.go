// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockup view 4 shape: Targets/Stacks collapse, Resources expands because
// Deletes contains failures, Creates/Updates stay collapsed.
func mockupTree() []*GroupNode {
	lines := func(n int) []GroupLine {
		out := make([]GroupLine, n)
		for i := range out {
			out[i] = GroupLine{Text: "line"}
		}
		return out
	}
	return []*GroupNode{
		{Title: "Targets", Summary: "2/2 Done", Lines: lines(2)},
		{Title: "Stacks", Summary: "3/3 Done", Lines: lines(3)},
		{Title: "Resources", Children: []*GroupNode{
			{Title: "Creates", Summary: "140/150", Lines: lines(150)},
			{Title: "Updates", Summary: "0/35", Lines: lines(35)},
			{Title: "Deletes", Lines: append(lines(5),
				GroupLine{Text: "✗ delete old-data", Problem: true},
				GroupLine{Text: "⊘ delete legacy-3", Problem: true},
			)},
		}},
		{Title: "Policies", Summary: "2/3 Done", Lines: lines(3)},
	}
}

func TestGroupNode_Count(t *testing.T) {
	roots := mockupTree()
	assert.Equal(t, 2, roots[0].Count())
	assert.Equal(t, 192, roots[2].Count()) // 150 + 35 + 7
}

func TestGroupNode_HasProblem(t *testing.T) {
	roots := mockupTree()
	assert.False(t, roots[0].HasProblem())
	assert.True(t, roots[2].HasProblem())
	assert.False(t, roots[2].Children[0].HasProblem())
	assert.True(t, roots[2].Children[2].HasProblem())
}

func TestExpandedHeight(t *testing.T) {
	// each node contributes 1 header line + its leaf lines when expanded
	roots := []*GroupNode{
		{Title: "A", Lines: []GroupLine{{Text: "x"}, {Text: "y"}}},
		{Title: "B", Children: []*GroupNode{{Title: "C", Lines: []GroupLine{{Text: "z"}}}}},
	}
	assert.Equal(t, 3+3, ExpandedHeight(roots))
}

func TestAutoLayout_EverythingFitsExpandsAll(t *testing.T) {
	roots := mockupTree()
	AutoLayout(roots, 10_000)
	assert.True(t, roots[0].Expanded())
	assert.True(t, roots[2].Expanded())
	assert.True(t, roots[2].Children[0].Expanded())
}

func TestAutoLayout_OverflowCollapsesToProblemBranches(t *testing.T) {
	roots := mockupTree()
	AutoLayout(roots, 24)
	assert.False(t, roots[0].Expanded(), "Targets")
	assert.False(t, roots[1].Expanded(), "Stacks")
	assert.True(t, roots[2].Expanded(), "Resources contains failures")
	assert.False(t, roots[2].Children[0].Expanded(), "Creates")
	assert.False(t, roots[2].Children[1].Expanded(), "Updates")
	assert.True(t, roots[2].Children[2].Expanded(), "Deletes contains failures")
	assert.False(t, roots[3].Expanded(), "Policies")
}

func TestAutoLayout_UserOverrideWins(t *testing.T) {
	roots := mockupTree()
	AutoLayout(roots, 24)
	roots[0].Toggle()             // user expands Targets
	roots[2].Children[2].Toggle() // user collapses Deletes
	AutoLayout(roots, 24)         // e.g. after a data refresh at same size
	assert.True(t, roots[0].Expanded(), "user-expanded stays expanded")
	assert.False(t, roots[2].Children[2].Expanded(), "user-collapsed stays collapsed")
}
