// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// DiagnosticSeverity classifies a Diagnostic for the caller that logs it.
const (
	// DiagnosticWarn reports a payload the walk resolved, but not unambiguously.
	DiagnosticWarn = "warn"
	// DiagnosticError reports an input the walk could not interpret at all and
	// fell back to its most conservative mode for. It indicates an internal
	// defect, not bad user input.
	DiagnosticError = "error"
)

// Diagnostic reports a condition the opaque-path walk could not resolve
// cleanly. The walk itself never logs and never fails on one: its main caller
// runs after a command has reached a final state, where aborting would discard
// completed work. Callers attach resource identity and surface it.
type Diagnostic struct {
	Severity string
	// Hint is the opaque hint name the diagnostic concerns, empty when it is
	// not specific to one hint.
	Hint   string
	Detail string
}

func (d Diagnostic) String() string {
	if d.Hint == "" {
		return d.Detail
	}
	return fmt.Sprintf("opaque hint %q: %s", d.Hint, d.Detail)
}

// prefix is one accumulated reading of the path from the walk root to the
// current node. path is what a child key is concatenated onto (empty, or ending
// in "."); steps is the same reading as its individual keys, kept so a match can
// report WHICH segmentation produced it.
type prefix struct {
	path  string
	steps []string
}

// OpaqueWalk matches opaque hint names against a decoded JSON tree and replaces
// the values they select, mutating the tree in place as it walks.
//
// A hint name for a property nested in a SubResource is emitted dotted (e.g.
// "settings.password"), and a dotted name is ambiguous: it may describe a
// structural path, or a single key that genuinely contains a dot (a Grafana
// provisioning response returns "hmacConfig.secret" as a flat key). Rather than
// pick a reading, the walk matches by plain prefix CONCATENATION — at a node
// reached with prefix P, key K matches when P+K is in Opaque — which matches
// every reading actually present in the payload without enumerating any:
// {"a":{"b":{"c":…}}}, {"a":{"b.c":…}}, {"a.b":{"c":…}} and {"a.b.c":…} all
// concatenate to "a.b.c". Confidentiality is therefore fail-safe by
// construction. This is deliberately NOT gjson/sjson path syntax, which reads a
// literal dot-containing key as a nested path, and it is deliberately not gated
// on a parent name also being declared: any gate can only remove matches, and
// so can only reintroduce the cleartext leak for a plugin whose schema declares
// the nested name alone.
//
// The cost is over-matching — a non-secret key can be selected when it collides
// with a hint under a different segmentation — which is why every match records
// its segmentation and a hint matched under two or more of them raises a
// Diagnostic.
//
// Mutation happens during the walk rather than in a collect-then-replay pass:
// replaying would need typed path segments and would have to cope with a parent
// replacement invalidating already-collected descendant sites. Match and OnMiss
// are total by contract; the walk is not transactional, so a future fallible
// callback would leave the tree partially mutated.
type OpaqueWalk struct {
	// Opaque is the set of opaque hint names. The walk is set-driven ONLY:
	// discovering an inline $visibility envelope is not its job, so the
	// transformer and the redactor cannot drift on which NAMES match while
	// keeping their different value semantics.
	Opaque map[string]bool

	// Match is invoked on a value whose name matched, and returns the value to
	// store plus whether it changed. It is handed the matched value WHOLE and
	// the walk does not descend into it: hashing only part of a map-shaped
	// secret would leave its sibling keys at rest in cleartext, and for
	// redaction the matched name is equally the declared secret. Descent stops
	// even when the callback declines to change the value (an already-hashed
	// envelope).
	Match func(value any) (any, bool)

	// OnMiss, when set, is invoked on a value NO hint name matched, and reports
	// whether it claimed the value (in which case descent also stops). It is
	// the transformer's inline $visibility=Opaque envelope branch. Ordering is
	// security-critical and part of this contract: the name match is tested
	// FIRST, so a raw map-shaped secret that happens to carry a $value key is
	// never mistaken for an envelope. The redactor sets no OnMiss.
	OnMiss func(value any) (any, bool)

	// MatchAtAnyDepth additionally tests every hint name from every node, not
	// only from the accumulated prefix. It is the conservative fallback for an
	// input whose position could not be determined — it over-matches by
	// construction and leaks nothing.
	MatchAtAnyDepth bool

	// segmentations maps a hint name to the set of distinct readings that
	// matched it. Only diagnostics accumulate during the walk; values are
	// replaced in place.
	segmentations map[string]map[string]bool
}

// WalkProperties walks a decoded property map from the root prefix.
func (w *OpaqueWalk) WalkProperties(m map[string]any) {
	w.walkMap(m, []prefix{{}})
}

// walkValueAt walks an unnamed value — an array element, or a patch op's value
// rooted at its path — against the given candidate prefixes, and returns the
// value to store in its place. Several candidate prefixes are walked in ONE
// traversal so a concrete node is visited and mutated exactly once.
func (w *OpaqueWalk) walkValueAt(v any, prefixes []prefix) any {
	switch val := v.(type) {
	case map[string]any:
		w.walkMap(val, prefixes)
	case []any:
		// pkl emits index-free hint names for a Listing<SubResource>, so array
		// elements descend with the SAME prefix as the field itself — including
		// nested arrays, which is broader than that guarantee but errs in the
		// confidentiality-safe direction. An element that is neither object nor
		// array is skipped: a hint names a property, never a bare element, so
		// no hint can select that position.
		for i, elem := range val {
			val[i] = w.walkValueAt(elem, prefixes)
		}
	}
	return v
}

func (w *OpaqueWalk) walkMap(m map[string]any, prefixes []prefix) {
	for key, v := range m {
		if replacement, matched := w.matchKey(key, v, prefixes); matched {
			m[key] = replacement
			continue
		}
		if w.OnMiss != nil {
			if replacement, claimed := w.OnMiss(v); claimed {
				m[key] = replacement
				continue
			}
		}
		m[key] = w.walkValueAt(v, childPrefixes(key, prefixes, w.MatchAtAnyDepth))
	}
}

// matchKey tests key against every candidate prefix and, on the first hit,
// records the segmentation and hands the value to Match. At most one hint can
// match a given key under a given prefix, and two distinct prefixes yielding
// the same name is impossible, so a concrete node is never matched twice.
func (w *OpaqueWalk) matchKey(key string, v any, prefixes []prefix) (any, bool) {
	for _, p := range prefixes {
		name := p.path + key
		if !w.Opaque[name] {
			continue
		}
		w.recordSegmentation(name, append(slices.Clone(p.steps), key))
		if replacement, changed := w.Match(v); changed {
			return replacement, true
		}
		return v, true
	}
	return nil, false
}

func childPrefixes(key string, prefixes []prefix, matchAtAnyDepth bool) []prefix {
	out := make([]prefix, 0, len(prefixes)+1)
	for _, p := range prefixes {
		out = append(out, prefix{
			path:  p.path + key + ".",
			steps: append(slices.Clone(p.steps), key),
		})
	}
	if matchAtAnyDepth {
		out = append(out, prefix{})
	}
	return out
}

func (w *OpaqueWalk) recordSegmentation(hint string, steps []string) {
	if w.segmentations == nil {
		w.segmentations = make(map[string]map[string]bool)
	}
	if w.segmentations[hint] == nil {
		w.segmentations[hint] = make(map[string]bool)
	}
	w.segmentations[hint][describeSegmentation(steps)] = true
}

// describeSegmentation renders a reading's key boundaries unambiguously, so a
// diagnostic distinguishes "a"/"b.c" from "a.b"/"c".
func describeSegmentation(steps []string) string {
	quoted := make([]string, len(steps))
	for i, s := range steps {
		quoted[i] = strconv.Quote(s)
	}
	return strings.Join(quoted, "/")
}

// Diagnostics returns every hint that matched under two or more distinct
// segmentations. Consuming them is mandatory at every production caller —
// over-matching is only observable if someone surfaces it. A hint matched at
// many concrete paths under ONE segmentation (an ordinary Listing<SubResource>)
// is not ambiguous and is not reported.
//
// Conditions found outside the walk — an undecodable patch pointer, an exceeded
// candidate bound — are raised by the caller that detects them, since the walk
// never sees the input that caused them.
func (w *OpaqueWalk) Diagnostics() []Diagnostic {
	var out []Diagnostic
	for hint, segs := range w.segmentations {
		if len(segs) < 2 {
			continue
		}
		readings := slices.Sorted(maps.Keys(segs))
		out = append(out, Diagnostic{
			Severity: DiagnosticWarn,
			Hint:     hint,
			Detail: fmt.Sprintf("matched under %d distinct segmentations (%s) — a value that is not a secret may have been treated as one",
				len(readings), strings.Join(readings, ", ")),
		})
	}
	slices.SortFunc(out, func(a, b Diagnostic) int {
		if c := strings.Compare(a.Hint, b.Hint); c != 0 {
			return c
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	return out
}
