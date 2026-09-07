// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ModificationConfrontable reports whether a resource's out-of-band
// modification (its stored state at the last reconcile, oldProps, versus its
// current stored state, newProps) carries movement a reconcile must confront
// before proceeding. Movement confined to a co-owned collection's never-owned
// members — a co-actor's content, the very thing the ownership partition
// exists to tolerate — is not confrontable; everything else is (a plain-field
// change, a provider-default move, a declared/formerly-owned co-owned member
// move).
//
// It is the gate's answer to "does this resource carry drift the user has not
// seen", asked independently of whether the forma also plans a change to the
// resource. The prior gate used "the forma plans a change" as a proxy for
// "there is unconfronted drift", which rejected every edit of a resource that
// merely carried a co-actor's tolerated content.
//
// The comparison keeps everything except never-owned co-owned members, so it
// errs toward confront: only that one tolerated class is removed, and anything
// it cannot classify as a co-owned member (an opaque or malformed co-owned
// value, a provider-default field, a plain field) stays and confronts. A false
// "confront" is a spurious rejection recoverable with --force, never a silent
// overwrite of drift. Provider-default and witness handling for resources the
// forma does NOT edit stays in the earlier drift stages (FilterUnabsorbed /
// WitnessedMoved); this predicate only re-examines what those already flagged.
func ModificationConfrontable(oldProps, newProps, desired json.RawMessage, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) (bool, error) {
	oldRem, oldEmptied, err := confrontableRemainder(oldProps, desired, priorOwned, schema)
	if err != nil {
		return false, err
	}
	newRem, newEmptied, err := confrontableRemainder(newProps, desired, priorOwned, schema)
	if err != nil {
		return false, err
	}

	// An ancestor object emptied by neutralizing a co-owned child on EITHER
	// side is pruned from BOTH, so the comparison neither turns on which side
	// carried the tolerated content (symmetry) nor conflates a container that
	// neutralization emptied with one the resource holds explicitly empty (only
	// the former is ever collected here).
	emptied := unionSorted(oldEmptied, newEmptied)
	oldRem = pruneAncestors(oldRem, emptied)
	newRem = pruneAncestors(newRem, emptied)

	oldCanon, err := canonicalizeRemainder(oldRem)
	if err != nil {
		return false, err
	}
	newCanon, err := canonicalizeRemainder(newRem)
	if err != nil {
		return false, err
	}
	return oldCanon != newCanon, nil
}

// confrontableRemainder returns props with never-owned co-owned members
// removed, plus the ancestor objects that neutralizing a nested co-owned path
// left empty (so the caller can prune them symmetrically across both sides).
// A co-owned (non-opaque) path's value is restricted to the members this forma
// declares or has on record (declared ∪ prior); an unclassifiable co-owned
// value and every non-co-owned field are left intact.
func confrontableRemainder(props, desired json.RawMessage, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) (json.RawMessage, []string, error) {
	out := props
	if len(out) == 0 {
		out = json.RawMessage(`{}`)
	}

	var deletedNested []string

	for path, hint := range schema.Hints {
		if hint.CoOwned == nil || hint.Opaque {
			continue
		}
		val := gjson.GetBytes(out, path)
		if !val.Exists() {
			continue
		}
		// An unclassifiable shape (a scalar where a collection is expected, or
		// an array element without its identity field) has no members to
		// partition; leaving it in the remainder confronts its change rather
		// than deleting it and reading distinct values as equal.
		if !coOwnedValueClassifiable(val, hint) {
			continue
		}
		keep := map[string]struct{}{}
		for _, id := range pkgmodel.MemberIdentities(gjson.GetBytes(desired, path), hint) {
			keep[id] = struct{}{}
		}
		if rec, ok := priorOwned[path]; ok && rec.Rule == pkgmodel.IdentityRule(hint) {
			for _, id := range rec.Members {
				keep[id] = struct{}{}
			}
		}
		// restrictRawToKeep keeps exactly the members whose identity is in the
		// supplied set — here declared ∪ prior — so never-owned members are
		// dropped, preserving each kept member's raw JSON (so a large-integer
		// value is not reconstructed through a lossy float64).
		restricted, kept := restrictRawToKeep(val, hint, keep)
		var err error
		if !kept {
			out, err = sjson.DeleteBytes(out, path)
			if strings.Contains(path, ".") {
				deletedNested = append(deletedNested, path)
			}
		} else {
			out, err = sjson.SetRawBytes(out, path, restricted)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("failed to reduce co-owned path %q: %w", path, err)
		}
	}

	return out, emptiedAncestors(out, deletedNested), nil
}

// emptiedAncestors returns the ancestor object paths that are now empty as a
// result of deleting the given nested paths — walking each deleted path's
// ancestors from the immediate parent upward, collecting empty objects and
// stopping at the first non-empty one. An ancestor the document held
// explicitly empty (no co-owned child was deleted under it) is never collected,
// because only actually-deleted paths are walked.
func emptiedAncestors(doc json.RawMessage, deletedPaths []string) []string {
	if len(deletedPaths) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, path := range deletedPaths {
		segments := strings.Split(path, ".")
		for i := len(segments) - 1; i >= 1; i-- {
			ancestor := strings.Join(segments[:i], ".")
			val := gjson.GetBytes(doc, ancestor)
			if !val.IsObject() || len(val.Map()) != 0 {
				break
			}
			if _, ok := seen[ancestor]; !ok {
				seen[ancestor] = struct{}{}
				out = append(out, ancestor)
			}
		}
	}
	return out
}

// pruneAncestors deletes each of the given ancestor paths from the document
// when it is present and empty, deepest first so a parent emptied only by
// pruning its children is removed too.
func pruneAncestors(doc json.RawMessage, ancestors []string) json.RawMessage {
	for _, ancestor := range ancestors {
		val := gjson.GetBytes(doc, ancestor)
		if !val.IsObject() || len(val.Map()) != 0 {
			continue
		}
		next, err := sjson.DeleteBytes(doc, ancestor)
		if err != nil {
			continue
		}
		doc = next
	}
	return doc
}

// unionSorted returns the union of two path sets, sorted by descending depth
// (most "." first) then lexically, so pruneAncestors removes a child before its
// parent.
func unionSorted(a, b []string) []string {
	set := map[string]struct{}{}
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "."), strings.Count(out[j], ".")
		if di != dj {
			return di > dj
		}
		return out[i] < out[j]
	})
	return out
}

// canonicalizeRemainder renders a properties document deterministically
// (sorted object keys) so two remainders compare as strings. It decodes with
// UseNumber so a large integer keeps its exact literal instead of collapsing
// to a float64, which would let two distinct out-of-range integers compare
// equal and silently pass real drift.
func canonicalizeRemainder(doc json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return "", err
	}
	return canonicalJSON(decoded), nil
}

// restrictRawToKeep returns val reduced to the members whose identity is in
// keep, as raw JSON, and whether anything was kept. For an object (Mapping) it
// keeps the entries whose key is in keep; for an array (Set/EntitySet) it keeps
// the elements whose identity is in keep, sorted by their raw form for a
// deterministic comparison. Each kept member is carried as its verbatim raw
// JSON, so numeric precision survives. Returns (nil, false) when nothing is
// kept, so the caller drops the whole path exactly as before.
func restrictRawToKeep(val gjson.Result, hint pkgmodel.FieldHint, keep map[string]struct{}) (json.RawMessage, bool) {
	switch {
	case val.IsObject():
		out := map[string]json.RawMessage{}
		val.ForEach(func(key, v gjson.Result) bool {
			if _, ok := keep[key.String()]; ok {
				out[key.String()] = json.RawMessage(v.Raw)
			}
			return true
		})
		if len(out) == 0 {
			return nil, false
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return nil, false
		}
		return raw, true
	case val.IsArray():
		var elems []string
		val.ForEach(func(_, el gjson.Result) bool {
			if id, ok := singleElementIdentity(el, hint); ok {
				if _, keepIt := keep[id]; keepIt {
					elems = append(elems, el.Raw)
				}
			}
			return true
		})
		if len(elems) == 0 {
			return nil, false
		}
		sort.Strings(elems)
		return json.RawMessage("[" + strings.Join(elems, ",") + "]"), true
	default:
		return nil, false
	}
}

// coOwnedValueClassifiable reports whether a co-owned field's value can be
// partitioned into members, and its SHAPE matches the hint's identity rule: a
// Mapping's value must be an object (its keys are the identities), and a
// Set/EntitySet's value must be an array every element of which yields an
// identity. A shape that mismatches the rule (an object where an array is
// expected, or the reverse), a scalar, or an EntitySet element missing its
// index field is unclassifiable — restrictToNeverOwned would return nil for it
// exactly as it does for a legitimately drained collection, so the caller must
// not neutralize it.
func coOwnedValueClassifiable(val gjson.Result, hint pkgmodel.FieldHint) bool {
	mapShaped := pkgmodel.IdentityRule(hint) == "Mapping"
	switch {
	case val.IsObject():
		return mapShaped
	case val.IsArray():
		if mapShaped {
			return false
		}
		classifiable := true
		val.ForEach(func(_, el gjson.Result) bool {
			if _, ok := singleElementIdentity(el, hint); !ok {
				classifiable = false
				return false
			}
			return true
		})
		return classifiable
	default:
		return false
	}
}
