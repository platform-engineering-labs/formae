// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

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

// groupItem is one visible row: either a group header (node != nil) or a
// leaf line.
type groupItem struct {
	node  *GroupNode
	line  string
	depth int
}

func flattenVisible(nodes []*GroupNode, depth int, out []groupItem) []groupItem {
	for _, n := range nodes {
		out = append(out, groupItem{node: n, depth: depth})
		if !n.expanded {
			continue
		}
		for _, l := range n.Lines {
			out = append(out, groupItem{line: l.Text, depth: depth + 1})
		}
		out = flattenVisible(n.Children, depth+1, out)
	}
	return out
}

// GroupList is a scrollable, cursor-navigable view over a collapsible group
// tree — the core building block for status/watch and simulation preview.
// The caller owns the GroupNode instances; reuse them across data refreshes
// to preserve user expansion overrides.
type GroupList struct {
	theme  *theme.Theme
	keys   tui.KeyMap
	roots  []*GroupNode
	vp     viewport.Model
	items  []groupItem
	cursor int
	width  int
	height int
}

// NewGroupList creates an empty group list.
func NewGroupList(th *theme.Theme, keys tui.KeyMap) GroupList {
	return GroupList{
		theme:  th,
		keys:   keys,
		vp:     viewport.New(80, 24),
		width:  80,
		height: 24,
	}
}

// SetNodes replaces the tree, runs auto-collapse layout for the current
// size and rebuilds the view.
func (g GroupList) SetNodes(roots []*GroupNode) GroupList {
	g.roots = roots
	AutoLayout(g.roots, g.height)
	return g.refresh()
}

// SetSize resizes the list and re-runs auto-collapse layout.
func (g GroupList) SetSize(width, height int) GroupList {
	g.width, g.height = width, height
	g.vp.Width = width
	g.vp.Height = height
	if g.roots != nil {
		AutoLayout(g.roots, g.height)
	}
	return g.refresh()
}

// CursorNode returns the group node under the cursor, or nil when the
// cursor is on a leaf line.
func (g GroupList) CursorNode() *GroupNode {
	if g.cursor < 0 || g.cursor >= len(g.items) {
		return nil
	}
	return g.items[g.cursor].node
}

// Update handles cursor movement, expansion toggling and paging.
func (g GroupList) Update(msg tea.Msg) (GroupList, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return g, nil
	}
	switch {
	case key.Matches(keyMsg, g.keys.Down):
		if g.cursor < len(g.items)-1 {
			g.cursor++
		}
	case key.Matches(keyMsg, g.keys.Up):
		if g.cursor > 0 {
			g.cursor--
		}
	case key.Matches(keyMsg, g.keys.Enter):
		if n := g.CursorNode(); n != nil {
			n.Toggle()
			return g.refresh(), nil
		}
		return g, nil
	case key.Matches(keyMsg, g.keys.PageDown):
		g.cursor = min(g.cursor+g.vp.Height/2, len(g.items)-1)
	case key.Matches(keyMsg, g.keys.PageUp):
		g.cursor = max(g.cursor-g.vp.Height/2, 0)
	default:
		return g, nil
	}
	return g.syncViewport(), nil
}

// View renders the visible window of the list.
func (g GroupList) View() string { return g.vp.View() }

// refresh re-flattens the tree, clamps the cursor and re-renders.
func (g GroupList) refresh() GroupList {
	g.items = flattenVisible(g.roots, 0, nil)
	g.cursor = max(min(g.cursor, len(g.items)-1), 0)
	return g.syncViewport()
}

// syncViewport re-renders content and scrolls so the cursor stays visible.
func (g GroupList) syncViewport() GroupList {
	g.vp.SetContent(g.render())
	if g.cursor < g.vp.YOffset {
		g.vp.SetYOffset(g.cursor)
	} else if g.cursor >= g.vp.YOffset+g.vp.Height {
		g.vp.SetYOffset(g.cursor - g.vp.Height + 1)
	}
	return g
}

func (g GroupList) render() string {
	p := g.theme.Palette
	titleSt := lipgloss.NewStyle().Foreground(p.TextPrimary)
	countSt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	summarySt := lipgloss.NewStyle().Foreground(p.TextSecondary)
	cursorSt := lipgloss.NewStyle().Background(p.Selection)

	lines := make([]string, 0, len(g.items))
	for i, it := range g.items {
		indent := strings.Repeat("  ", it.depth+1)
		var line string
		if it.node != nil {
			marker := "▸"
			if it.node.expanded {
				marker = "▾"
			}
			line = fmt.Sprintf("%s%s %s %s", indent, marker,
				titleSt.Render(it.node.Title),
				countSt.Render(fmt.Sprintf("(%d)", it.node.Count())))
			if !it.node.expanded && it.node.Summary != "" {
				right := summarySt.Render(it.node.Summary) + "  "
				line += PadBetween(g.width, line, right) + right
			}
		} else {
			line = indent + it.line
		}
		if i == g.cursor {
			if pad := g.width - lipgloss.Width(line); pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			line = cursorSt.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
