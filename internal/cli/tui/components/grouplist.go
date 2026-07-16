// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

// GroupLine is a leaf row inside a group. Text is the fully rendered line
// (including any state glyph); Problem marks failed/skipped rows, which pin
// their ancestors expanded during auto-collapse.
type GroupLine struct {
	Text    string
	Problem bool
}

// GroupNode is a collapsible group of lines and/or subgroups, e.g.
// "Resources" or a nested "Deletes". Expansion is controlled by AutoLayout
// until the user toggles the node, after which the user's choice wins.
type GroupNode struct {
	Title    string
	Summary  string // right-aligned when collapsed, e.g. "140/150"
	Lines    []GroupLine
	Children []*GroupNode

	expanded bool
	userSet  bool
}

// Count returns the total number of leaf lines under the node.
func (n *GroupNode) Count() int {
	c := len(n.Lines)
	for _, child := range n.Children {
		c += child.Count()
	}
	return c
}

// HasProblem reports whether any leaf line under the node is a problem row.
func (n *GroupNode) HasProblem() bool {
	for _, l := range n.Lines {
		if l.Problem {
			return true
		}
	}
	for _, child := range n.Children {
		if child.HasProblem() {
			return true
		}
	}
	return false
}

// Expanded reports the node's current expansion state.
func (n *GroupNode) Expanded() bool { return n.expanded }

// Toggle flips the expansion state and marks it as a user override, which
// subsequent AutoLayout calls respect.
func (n *GroupNode) Toggle() {
	n.expanded = !n.expanded
	n.userSet = true
}

// ExpandedHeight returns the rendered line count of the tree with every
// node expanded: one header line per node plus its leaf lines. This is
// computed from the data so auto-collapse never needs a trial render.
func ExpandedHeight(roots []*GroupNode) int {
	h := 0
	for _, n := range roots {
		h++ // header
		h += len(n.Lines)
		h += ExpandedHeight(n.Children)
	}
	return h
}

// AutoLayout decides expansion for all nodes the user has not toggled: if
// the fully expanded tree fits in height lines everything expands,
// otherwise only branches containing problems expand.
func AutoLayout(roots []*GroupNode, height int) {
	fits := ExpandedHeight(roots) <= height
	autoLayout(roots, fits)
}

func autoLayout(nodes []*GroupNode, fits bool) {
	for _, n := range nodes {
		if !n.userSet {
			n.expanded = fits || n.HasProblem()
		}
		autoLayout(n.Children, fits)
	}
}
