// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ProjectDesiredForWrite returns the desired properties with each co-owned
// collection completed to its intended post-write value: the declared
// members plus the never-owned live members (a co-actor's content, which the
// drainable-restricted patch deliberately plans nothing for). Formerly-owned
// members stay absent — their absence is the drain.
//
// The agent hands a plugin two descriptions of one update, DesiredProperties
// and PatchDocument, and a plugin legitimately acts on either. Without this
// projection they disagree on a co-owned path by exactly the tolerated
// members: the patch preserves what the desired document omits, so a
// desired-state-driven plugin deletes what a patch-driven plugin keeps. With
// it, DesiredProperties equals PriorProperties with the patch applied, and
// both plugin styles produce the same cloud state.
//
// This is a projection for the plugin-facing copy of an update request ONLY.
// The durable DesiredState.Properties must stay declared-only: the record
// recompute at the write echo claims declared-intersect-echo, and a desired
// document that already contains tolerated members would get them claimed —
// and later drained — as this forma's own.
//
// Per co-owned path: an unset (absent or null) field is skipped — whole-field
// omit tolerance already keeps every plugin's hands off it. An uninterpretable
// record (rule mismatch) degrades to no prior, so unclassifiable live members
// merge in as never-owned — toward tolerance, never toward a deletion. Live
// members are copied in the stored document's own representation; the
// plugin-format conversion downstream flattens envelopes the same way it does
// for declared members.
func ProjectDesiredForWrite(desired, live json.RawMessage, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) (json.RawMessage, error) {
	out := desired
	for path, hint := range schema.Hints {
		if hint.CoOwned == nil || hint.Opaque {
			continue
		}
		// The patch layer ignores a CoOwned hint on an ineligible collection
		// shape (no identity rule, e.g. Array); the projection must ignore
		// exactly the same hints or the two representations diverge again.
		if pkgmodel.IdentityRule(hint) == "" {
			continue
		}

		desiredVal := gjson.GetBytes(out, path)
		if !desiredVal.Exists() || desiredVal.Type == gjson.Null {
			continue
		}
		liveVal := gjson.GetBytes(live, path)
		if !liveVal.Exists() {
			continue
		}

		declared := pkgmodel.MemberIdentities(desiredVal, hint)
		var prior []string
		if record, ok := priorOwned[path]; ok && record.Rule == pkgmodel.IdentityRule(hint) {
			prior = record.Members
		}
		liveIDs := pkgmodel.MemberIdentities(liveVal, hint)
		partition := pkgmodel.PartitionOwnership(declared, prior, liveIDs)
		if len(partition.NeverOwned) == 0 {
			continue
		}

		merged, changed, err := mergeNeverOwned(desiredVal, liveVal, hint, partition.NeverOwned)
		if err != nil {
			return nil, fmt.Errorf("failed to project co-owned path %q: %w", path, err)
		}
		if !changed {
			continue
		}
		out, err = sjson.SetRawBytes(out, path, merged)
		if err != nil {
			return nil, fmt.Errorf("failed to write projected co-owned path %q: %w", path, err)
		}
	}
	return out, nil
}

// mergeNeverOwned returns desiredVal with live's never-owned members merged
// in: for an object (Mapping), the never-owned keys with their live values;
// for an array (Set/EntitySet), the live elements whose identity is
// never-owned, appended after the declared ones. Members are matched by the
// same identity computation the partition used, so a live element that
// contributes no identity is left out (it cannot be classified, and adding
// it could duplicate a declared member).
func mergeNeverOwned(desiredVal, liveVal gjson.Result, hint pkgmodel.FieldHint, neverOwned map[string]struct{}) (json.RawMessage, bool, error) {
	switch {
	case desiredVal.IsObject() && liveVal.IsObject():
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(desiredVal.Raw), &obj); err != nil {
			return nil, false, err
		}
		if obj == nil {
			obj = map[string]json.RawMessage{}
		}
		changed := false
		liveVal.ForEach(func(key, val gjson.Result) bool {
			if _, ok := neverOwned[key.String()]; ok {
				obj[key.String()] = json.RawMessage(val.Raw)
				changed = true
			}
			return true
		})
		if !changed {
			return nil, false, nil
		}
		raw, err := json.Marshal(obj)
		return raw, true, err
	case desiredVal.IsArray() && liveVal.IsArray():
		elements := []json.RawMessage{}
		for _, el := range desiredVal.Array() {
			elements = append(elements, json.RawMessage(el.Raw))
		}
		changed := false
		liveVal.ForEach(func(_, el gjson.Result) bool {
			id, ok := singleElementIdentity(el, hint)
			if !ok {
				return true
			}
			if _, keep := neverOwned[id]; keep {
				elements = append(elements, json.RawMessage(el.Raw))
				changed = true
			}
			return true
		})
		if !changed {
			return nil, false, nil
		}
		raw, err := json.Marshal(elements)
		return raw, true, err
	default:
		// Shape mismatch (or a non-collection value): nothing safe to merge.
		return nil, false, nil
	}
}
