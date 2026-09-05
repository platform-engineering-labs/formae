package model

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/theory/jsonpath"
)

// Excludes reports whether a resource with these properties is excluded by this
// filter: it is of a type the filter applies to, and every one of the filter's
// conditions matches.
//
// This lives beside MatchFilter rather than in the agent so that whoever
// declares a filter can test it. Plugins announce their own filters, and a
// filter whose JSONPath is subtly wrong excludes nothing while looking correct,
// which is indistinguishable from a resource simply not being there.
func (f MatchFilter) Excludes(properties json.RawMessage) bool {
	// A filter naming no conditions excludes nothing. Reading it as a vacuous
	// AND would make it exclude everything it is scoped to, so the emptiest
	// filter anyone can write would be the most destructive one.
	if len(f.Conditions) == 0 {
		return false
	}

	for _, cond := range f.Conditions {
		if !cond.matches(properties) {
			return false
		}
	}

	return true
}

// matches evaluates a single condition using JSONPath. PropertyPath is a
// JSONPath expression to query properties. An empty PropertyValue is an
// existence check; a non-empty one is an exact string match.
func (c FilterCondition) matches(properties json.RawMessage) bool {
	var data any
	if err := json.Unmarshal(properties, &data); err != nil {
		return false
	}

	path, err := jsonpathParser.Parse(c.PropertyPath)
	if err != nil {
		// Invalid JSONPath expression - no match
		return false
	}

	nodes, ok := selectNodes(path, c.PropertyPath, data)
	if !ok || len(nodes) == 0 {
		// No value found
		return false
	}

	// Empty PropertyValue = existence check (path returned something)
	if c.PropertyValue == "" {
		return true
	}

	for _, node := range nodes {
		if matchValue(node, c.PropertyValue) {
			return true
		}
	}
	return false
}

// selectNodes runs a parsed JSONPath against the document, reporting whether
// the evaluation completed.
//
// Parsing cleanly is not enough: a function extension applied to a member the
// document does not carry reaches the extension with nothing to read, and the
// evaluator dereferences it. Filters run against every discovered resource, so
// letting that escape would take discovery down over one badly written filter
// expression. A failed evaluation is reported as a miss, the same answer an
// unparseable expression already gets. Failing towards "matches nothing" is the
// safe direction, because a match evicts the resource's inventory row; it is
// also why the failure is logged rather than swallowed, since a filter that
// quietly stopped working would leave substrate exposed with no signal.
func selectNodes(path *jsonpath.Path, expression string, data any) (nodes []any, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("Discovery filter expression could not be evaluated",
				"expression", expression,
				"panic", r,
			)
			nodes, ok = nil, false
		}
	}()

	return path.Select(data), true
}

// matchValue compares a JSONPath result against an expected string value.
// Handles various result types including arrays and nested structures.
func matchValue(val any, expected string) bool {
	switch v := val.(type) {
	case string:
		return v == expected
	case []any:
		// JSONPath filter expressions can return arrays
		for _, item := range v {
			if matchValue(item, expected) {
				return true
			}
		}
		return false
	case map[string]any:
		// Check if it's a tag-like structure with Value field
		if value, ok := v["Value"]; ok {
			return matchValue(value, expected)
		}
		return false
	default:
		// Convert other types to string for comparison
		return fmt.Sprintf("%v", v) == expected
	}
}
