// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package migration

import (
	"strings"

	"github.com/tidwall/gjson"
)

// HasDottedKeyCorruption reports whether a properties document carries the shape
// an older build's dot-expansion left behind.
//
// That build joined literal map keys into gjson paths without escaping them, so
// writing back a stored key containing dots created a nested object tree instead
// of addressing the key. The result is a document holding BOTH: the literal key
// the plugin read, and an exploded tree beside it reproducing the same values.
//
// Exact reproduction is the test. A document may legitimately hold a dotted key
// and a same-named object at once — nothing in the data model makes them
// exclusive — so equal shapes alone would be evidence of ambiguity rather than
// of provenance. Requiring the nested tree to flatten to exactly the dotted
// siblings, no more and no less, is what distinguishes the two.
//
// Over-matching is not dangerous here: what follows a match is tombstoning the
// row so discovery re-ingests it cleanly, so the cost of a false positive is one
// redundant re-discovery. Missing a real match leaves corruption in place, so
// the search runs at every depth, through arrays as well as objects.
func HasDottedKeyCorruption(props []byte) bool {
	if len(props) == 0 {
		return false
	}
	return nodeHasCorruption(gjson.ParseBytes(props))
}

func nodeHasCorruption(node gjson.Result) bool {
	switch {
	case node.IsObject():
		if objectHasCorruption(node) {
			return true
		}
		corrupted := false
		node.ForEach(func(_, value gjson.Result) bool {
			if nodeHasCorruption(value) {
				corrupted = true
				return false
			}
			return true
		})
		return corrupted
	case node.IsArray():
		corrupted := false
		node.ForEach(func(_, value gjson.Result) bool {
			if nodeHasCorruption(value) {
				corrupted = true
				return false
			}
			return true
		})
		return corrupted
	default:
		return false
	}
}

// objectHasCorruption tests one object for the shape, without descending.
func objectHasCorruption(object gjson.Result) bool {
	members := object.Map()

	// Group the dotted keys by the segment before their first dot, which is the
	// key a nested duplicate of them would occupy.
	dottedByHead := map[string]map[string]gjson.Result{}
	for key, value := range members {
		head, _, found := strings.Cut(key, ".")
		if !found || head == "" {
			continue
		}
		if dottedByHead[head] == nil {
			dottedByHead[head] = map[string]gjson.Result{}
		}
		dottedByHead[head][key] = value
	}

	for head, literals := range dottedByHead {
		nested, present := members[head]
		if !present || !nested.IsObject() {
			continue
		}
		expected, ok := explode(head, literals)
		if !ok {
			continue
		}
		if treeMatches(expected, nested) {
			return true
		}
	}
	return false
}

// expectedTree is the tree a set of dotted keys would produce if each of their
// dots were read as nesting: an interior node carries children, a leaf carries
// the value the dotted key held.
type expectedTree struct {
	children map[string]*expectedTree
	leaf     gjson.Result
	isLeaf   bool
}

// explode builds the tree the given dotted keys would have exploded into. It
// reports false when the keys cannot all coexist in one tree — when one names a
// path through another's leaf — since no single nested sibling could then be
// their duplicate.
func explode(head string, literals map[string]gjson.Result) (*expectedTree, bool) {
	root := &expectedTree{children: map[string]*expectedTree{}}
	for key, value := range literals {
		segments := strings.Split(strings.TrimPrefix(key, head+"."), ".")
		node := root
		for i, segment := range segments {
			if node.isLeaf {
				return nil, false
			}
			if i == len(segments)-1 {
				if _, taken := node.children[segment]; taken {
					return nil, false
				}
				node.children[segment] = &expectedTree{leaf: value, isLeaf: true}
				break
			}
			child, present := node.children[segment]
			if !present {
				child = &expectedTree{children: map[string]*expectedTree{}}
				node.children[segment] = child
			}
			node = child
		}
	}
	return root, true
}

// treeMatches reports whether actual is exactly the tree expected describes:
// the same keys at every level, and equal values at the leaves. Anything extra
// on either side means the nested sibling is not a duplicate of the dotted keys
// and the document is left alone.
func treeMatches(expected *expectedTree, actual gjson.Result) bool {
	if expected.isLeaf {
		return leafEqual(expected.leaf, actual)
	}
	if !actual.IsObject() {
		return false
	}
	members := actual.Map()
	if len(members) != len(expected.children) {
		return false
	}
	for key, child := range expected.children {
		value, present := members[key]
		if !present || !treeMatches(child, value) {
			return false
		}
	}
	return true
}

func leafEqual(a, b gjson.Result) bool {
	if a.Type != b.Type {
		return false
	}
	if a.IsObject() || a.IsArray() {
		// Compare the parsed forms so incidental whitespace does not decide it.
		return gjson.Parse(a.Raw).Raw == gjson.Parse(b.Raw).Raw
	}
	return a.String() == b.String()
}
