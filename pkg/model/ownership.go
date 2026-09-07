// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/tidwall/gjson"
)

// OwnedMembers records, per co-owned collection field, the set of member
// identities a forma last declared. The key is the field path as it appears
// in Schema.Hints.
type OwnedMembers map[string]OwnedPathRecord

// OwnedPathRecord is the declared ownership record for one co-owned
// collection field.
type OwnedPathRecord struct {
	// Rule is the record-compatibility rule the members were computed under
	// (see IdentityRule). A stored record whose Rule no longer matches the
	// field's current hint is stale and must not be trusted for comparison.
	Rule string `json:"Rule"`
	// Members are member identities, sorted, in provider spelling.
	Members []string `json:"Members"`
}

// IdentityRule derives the record-compatibility rule for a field from its
// hint alone. The rule names the scheme MemberIdentities used (or would use)
// to compute identities for that field, so a stored record can be checked
// for staleness without re-deriving it from the collection value.
func IdentityRule(hint FieldHint) string {
	switch {
	case hint.UpdateMethod == FieldUpdateMethodEntitySet:
		return "EntitySet/" + hint.IndexField
	case hint.UpdateMethod == FieldUpdateMethodSet:
		return "Set"
	case hint.CoOwned != nil && hint.UpdateMethod == FieldUpdateMethodNone:
		return "Mapping"
	default:
		return ""
	}
}

// MemberIdentities extracts the identity set of a collection value:
//   - object => its keys;
//   - array with an EntitySet hint => the json-marshaled IndexField value of
//     each element;
//   - array otherwise => the canonical JSON of each element.
//
// Reference envelopes ($ref/$res/$gen objects) are flattened to their $value
// first, recursively — a whole-field envelope as much as a per-element one;
// an envelope lacking $value contributes nothing to the result. The result
// is sorted and deduplicated.
func MemberIdentities(value gjson.Result, hint FieldHint) []string {
	if inner, ok := wholeFieldEnvelopeValue(value); ok {
		if !inner.Exists() || inner.Type == gjson.Null {
			return []string{}
		}
		return MemberIdentities(inner, hint)
	}
	switch {
	case value.IsObject():
		var out []string
		value.ForEach(func(key, _ gjson.Result) bool {
			out = append(out, key.String())
			return true
		})
		return sortedUniqueStrings(out)
	case value.IsArray():
		if hint.UpdateMethod == FieldUpdateMethodEntitySet {
			return entitySetIdentities(value, hint.IndexField)
		}
		return elementIdentities(value)
	default:
		return []string{}
	}
}

// MemberIdentitiesIncomplete reports whether the collection holds content
// that contributes no identity to MemberIdentities: an element (or the whole
// field value) carrying an unresolved reference envelope, or an EntitySet
// element without its IndexField. Identities computed from such a value
// understate the declaration — the members are declared, their identities
// are just not knowable yet — so a record recompute based on them must not
// drop existing claims.
func MemberIdentitiesIncomplete(value gjson.Result, hint FieldHint) bool {
	if inner, ok := wholeFieldEnvelopeValue(value); ok {
		// A resolved whole-field envelope exposes its members through
		// $value; only an unresolved one hides them.
		if !inner.Exists() || inner.Type == gjson.Null {
			return true
		}
		return MemberIdentitiesIncomplete(inner, hint)
	}
	switch {
	case value.IsObject():
		// A plain object's identities are its keys, which are always present.
		return false
	case value.IsArray():
		incomplete := false
		value.ForEach(func(_, el gjson.Result) bool {
			flat, ok := flattenEnvelope(el)
			if !ok {
				incomplete = true
				return false
			}
			if hint.UpdateMethod == FieldUpdateMethodEntitySet {
				fields, isMap := flat.(map[string]any)
				if !isMap {
					incomplete = true
					return false
				}
				if _, present := fields[hint.IndexField]; !present {
					incomplete = true
					return false
				}
			}
			return true
		})
		return incomplete
	default:
		return false
	}
}

// wholeFieldEnvelopeValue reports whether value is a reference envelope at
// the whole-field position ($ref/$res/$gen object) and, if so, returns its
// $value (which may not exist, or be null, for an unresolved envelope).
func wholeFieldEnvelopeValue(value gjson.Result) (gjson.Result, bool) {
	if !value.IsObject() {
		return gjson.Result{}, false
	}
	if IsResolvedReference(value) || IsResolvableObject(value) || IsGenObject(value) {
		return value.Get("$value"), true
	}
	return gjson.Result{}, false
}

// entitySetIdentities extracts the json-marshaled IndexField value of every
// element, after flattening each element's own reference envelope (if any).
func entitySetIdentities(value gjson.Result, indexField string) []string {
	var out []string
	value.ForEach(func(_, el gjson.Result) bool {
		flat, ok := flattenEnvelope(el)
		if !ok {
			return true
		}
		fields, ok := flat.(map[string]any)
		if !ok {
			return true
		}
		id, present := fields[indexField]
		if !present {
			return true
		}
		if b, err := json.Marshal(id); err == nil {
			out = append(out, string(b))
		}
		return true
	})
	return sortedUniqueStrings(out)
}

// elementIdentities canonicalizes each array element to JSON, after
// flattening its reference envelope (if any).
func elementIdentities(value gjson.Result) []string {
	var out []string
	value.ForEach(func(_, el gjson.Result) bool {
		flat, ok := flattenEnvelope(el)
		if !ok {
			return true
		}
		if b, err := json.Marshal(flat); err == nil {
			out = append(out, string(b))
		}
		return true
	})
	return sortedUniqueStrings(out)
}

// flattenEnvelope decodes value to a native Go value ([]any / map[string]any
// / scalar), replacing every $ref/$res/$gen envelope found anywhere in it
// with its $value (recursively, since a $value may itself be an envelope).
// Returns false if any envelope encountered — at any depth — lacks $value
// (missing or explicit null), which fails the whole (sub)value it lives in.
func flattenEnvelope(value gjson.Result) (any, bool) {
	switch {
	case value.IsObject():
		if IsResolvedReference(value) || IsResolvableObject(value) || IsGenObject(value) {
			inner := value.Get("$value")
			if !inner.Exists() || inner.Type == gjson.Null {
				return nil, false
			}
			return flattenEnvelope(inner)
		}

		out := map[string]any{}
		ok := true
		value.ForEach(func(key, val gjson.Result) bool {
			flat, flatOK := flattenEnvelope(val)
			if !flatOK {
				ok = false
				return false
			}
			out[key.String()] = flat
			return true
		})
		if !ok {
			return nil, false
		}
		return out, true
	case value.IsArray():
		var out []any
		ok := true
		value.ForEach(func(_, val gjson.Result) bool {
			flat, flatOK := flattenEnvelope(val)
			if !flatOK {
				ok = false
				return false
			}
			out = append(out, flat)
			return true
		})
		if !ok {
			return nil, false
		}
		return out, true
	default:
		return value.Value(), true
	}
}

// sortedUniqueStrings returns a sorted, deduplicated, non-nil copy of in.
func sortedUniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// OwnershipPartition is the three-way split of a co-owned field's member
// identities relative to what this forma declares.
type OwnershipPartition struct {
	// Declared is the set this forma's current source declares.
	Declared map[string]struct{}
	// FormerlyOwned is Declared on a prior apply but not this one — members
	// this forma is relinquishing.
	FormerlyOwned map[string]struct{}
	// NeverOwned is live but neither declared now nor previously — members
	// this forma has never claimed (another writer's, or the platform's).
	NeverOwned map[string]struct{}
}

// PartitionOwnership splits live into FormerlyOwned and NeverOwned relative
// to declared and prior:
//
//	FormerlyOwned = prior − declared
//	NeverOwned    = live − declared − prior
//
// A nil prior (no ownership record on the resource) yields an empty
// FormerlyOwned, since there is nothing to relinquish.
func PartitionOwnership(declared, prior, live []string) OwnershipPartition {
	declaredSet := toStringSet(declared)
	priorSet := toStringSet(prior)
	liveSet := toStringSet(live)

	formerlyOwned := map[string]struct{}{}
	for member := range priorSet {
		if _, ok := declaredSet[member]; !ok {
			formerlyOwned[member] = struct{}{}
		}
	}

	neverOwned := map[string]struct{}{}
	for member := range liveSet {
		if _, ok := declaredSet[member]; ok {
			continue
		}
		if _, ok := priorSet[member]; ok {
			continue
		}
		neverOwned[member] = struct{}{}
	}

	return OwnershipPartition{
		Declared:      declaredSet,
		FormerlyOwned: formerlyOwned,
		NeverOwned:    neverOwned,
	}
}

func toStringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

// NormalizeOwnedMembers drops entries whose path's current hint is no longer
// CoOwned, drops entries with no Members, and sorts+dedups each remaining
// entry's Members. Returns nil when nothing remains, so a fully-stale or
// empty record serializes the same as no record at all.
func NormalizeOwnedMembers(om OwnedMembers, hints map[string]FieldHint) OwnedMembers {
	filtered := make(OwnedMembers, len(om))
	for path, record := range om {
		if hints[path].CoOwned == nil {
			continue
		}
		filtered[path] = record
	}
	return normalizeMembers(filtered)
}

// normalizeMembers applies the Members-level part of NormalizeOwnedMembers
// (drop empty, sort+dedup) without consulting hints. It is the shared core
// used by NormalizeOwnedMembers and OwnedMembersEqual, so equality does not
// depend on having a hints map at hand.
func normalizeMembers(om OwnedMembers) OwnedMembers {
	if len(om) == 0 {
		return nil
	}

	out := make(OwnedMembers, len(om))
	for path, record := range om {
		if len(record.Members) == 0 {
			continue
		}
		members := slices.Clone(record.Members)
		slices.Sort(members)
		members = slices.Compact(members)
		out[path] = OwnedPathRecord{Rule: record.Rule, Members: members}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// OwnedMembersEqual reports whether a and b are equal once normalized: nil
// and an empty map are equal, member order and duplicates are ignored, and
// entries with no Members are ignored. It does not consult hints, so it
// cannot detect a path that has since lost CoOwned — callers comparing
// against live schema should normalize with NormalizeOwnedMembers first.
func OwnedMembersEqual(a, b OwnedMembers) bool {
	return reflect.DeepEqual(normalizeMembers(a), normalizeMembers(b))
}
