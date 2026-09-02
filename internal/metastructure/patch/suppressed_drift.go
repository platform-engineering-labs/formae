// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/tidwall/gjson"
)

// SuppressedFieldDiff describes out-of-band movement on provider-default
// content the patch pipeline's strip passes hide from the diff: a
// hasProviderDefault field the user did not declare (or, for a partially
// declared EntitySet field, the elements with undeclared keys) whose value
// differs between two observations of the resource.
//
// From and To carry the suppressed content on each side, nil when the side
// has no value for the path. For an opaque path both are nil: the diff
// records the path and the fact of movement only, never values or digests,
// because callers persist and display these records.
//
// A diff requires a WITNESSED value: the suppressed content was present (a
// real scalar; a non-empty collection or leaf set) in the witness document,
// the resource's state as recorded by formae's own last write (the
// create/update echo), which sync never refreshes. Values that only ever
// arrived through sync (late-populated defaults, runtime registrations,
// co-actor content) are never witnessed and never reported: their movement
// is the infrastructure's business and stays in the drift list. A witnessed
// value changing or disappearing out of band is reported. A nil witness
// document (formae never wrote the resource) witnesses nothing.
type SuppressedFieldDiff struct {
	Path   string
	From   json.RawMessage
	To     json.RawMessage
	Opaque bool
}

// SuppressedFieldDiffs compares two stored property documents restricted to
// content a diff against the given desired properties would never see, for
// two independent reasons, and reports one entry per such field whose
// content moved, sorted by path.
//
// The first reason is a hasProviderDefault field: the content is exactly
// what the strip passes suppress — the whole field when desired omits it
// (absent or null, the fieldExistsInMap semantics of
// removeProviderDefaultFieldsBoth), and, for an EntitySet field with one or
// more declared keys, the elements whose key is not among the declared keys
// (what removeProviderDefaultEntitySetElements filters). An explicit empty
// EntitySet declaration suppresses nothing: that is a user-initiated clear
// the diff plans. This content requires a WITNESSED value (see below) to be
// reported.
//
// The second reason is a CoOwned field's never-owned members (see
// pkgmodel.PartitionOwnership): a co-owned collection's live content
// legitimately has writers other than this forma, and the diff plans only
// the members this forma declares or once declared — a co-actor's member is
// invisible to it by design, not by a provider-default strip. Movement here
// is classified by ownership, not by witness: a declared or formerly-owned
// member's movement is real drift the plan itself surfaces, so it produces
// no note here regardless of the witness; a never-owned member's value
// change, appearance, or disappearance produces one, whether or not formae
// ever wrote the resource. An opaque CoOwned field is excluded from this
// regime entirely (its members are secret values, not identities meant for
// partitioning — see claimedMembers) and falls back to the hasProviderDefault
// treatment above.
//
// Path enumeration is the sorted, deduplicated union of
// schema.HasProviderDefault() and every CoOwned-hinted path: a CoOwned path
// need not carry hasProviderDefault to be considered.
//
// A diff requires a WITNESSED value: the suppressed content was present (a
// real scalar; a non-empty collection or leaf set) in the witness document,
// the resource's state as recorded by formae's own last write (the
// create/update echo), which sync never refreshes. Values that only ever
// arrived through sync (late-populated defaults, runtime registrations,
// co-actor content) are never witnessed and never reported: their movement
// is the infrastructure's business and stays in the drift list. A witnessed
// value changing or disappearing out of band is reported. A nil witness
// document (formae never wrote the resource) witnesses nothing. This gate
// applies ONLY to the hasProviderDefault regime; the CoOwned regime above
// never consults the witness.
//
// Comparison is schema-aware so representation churn is not movement:
// EntitySet content compares keyed by IndexField, collections without an
// index compare as unordered multisets, Array-hinted fields compare ordered,
// and dotted leaves compare as a multiset of leaf values collected with the
// strip's own regime rules: a leaf reached through an array is suppressed
// unconditionally (the ContainerDefinitions.Cpu shape, stripped from both
// sides regardless of declaration), while a pure-object leaf is suppressed
// only when desired omits it.
func SuppressedFieldDiffs(oldProps, newProps, desired, witness json.RawMessage, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) ([]SuppressedFieldDiff, error) {
	oldMap, err := unmarshalPropsObject(oldProps)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal old properties: %w", err)
	}
	newMap, err := unmarshalPropsObject(newProps)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal new properties: %w", err)
	}
	desiredMap, err := unmarshalPropsObject(desired)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal desired properties: %w", err)
	}
	witnessMap, err := unmarshalPropsObject(witness)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal witness properties: %w", err)
	}

	paths := suppressedDiffCandidatePaths(schema)

	var diffs []SuppressedFieldDiff
	for _, path := range paths {
		hint := schema.Hints[path]

		if hint.CoOwned != nil && !hint.Opaque {
			if diff, ok := coOwnedSuppressedDiff(oldProps, newProps, desired, path, hint, priorOwned, schema); ok {
				diffs = append(diffs, diff)
			}
			continue
		}

		parts := strings.Split(path, ".")

		var from, to json.RawMessage
		var moved bool
		// opacityProbe holds exactly the decoded content a note would carry,
		// so envelope opacity markers anywhere within it force sanitization.
		var opacityProbe []any

		if len(parts) > 1 {
			if !anyWitnessedLeaf(collectSuppressedLeaves(witnessMap, desiredMap, parts)) {
				continue
			}
			oldLeaves := collectSuppressedLeaves(oldMap, desiredMap, parts)
			newLeaves := collectSuppressedLeaves(newMap, desiredMap, parts)
			from, to, moved = compareLeafMultisets(oldLeaves, newLeaves)
			opacityProbe = append(append(opacityProbe, oldLeaves...), newLeaves...)
		} else if !fieldExistsInMap(desiredMap, parts) {
			wVal, wHas := witnessMap[path]
			if !witnessedValue(wVal, wHas) {
				continue
			}
			from, to, moved = compareWholeField(oldMap, newMap, path, hint)
			opacityProbe = []any{oldMap[path], newMap[path], wVal}
		} else if hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "" {
			declaredKeys := entitySetKeys(desiredMap[path], hint.IndexField)
			if len(declaredKeys) == 0 {
				// Explicit empty (or keyless) declaration: the diff plans
				// the drain; nothing is suppressed.
				continue
			}
			witnessArr, _ := witnessMap[path].([]any)
			if len(suppressedEntitySetElements(witnessArr, hint.IndexField, declaredKeys)) == 0 {
				continue
			}
			from, to, moved = compareEntitySetSubsets(oldMap[path], newMap[path], hint.IndexField, declaredKeys)
			opacityProbe = []any{oldMap[path], newMap[path], witnessMap[path]}
		} else {
			continue
		}

		if !moved {
			continue
		}

		opaque := pathOpaque(schema, path)
		for _, v := range opacityProbe {
			if valueHasOpaqueMarker(v) {
				opaque = true
				break
			}
		}
		if opaque {
			diffs = append(diffs, SuppressedFieldDiff{Path: path, Opaque: true})
			continue
		}
		diffs = append(diffs, SuppressedFieldDiff{Path: path, From: from, To: to})
	}

	return diffs, nil
}

// suppressedDiffCandidatePaths is the sorted, deduplicated union of
// schema.HasProviderDefault() and every CoOwned-hinted path — the full set
// of paths SuppressedFieldDiffs classifies, whichever regime applies to
// each.
func suppressedDiffCandidatePaths(schema pkgmodel.Schema) []string {
	set := make(map[string]struct{}, len(schema.Hints))
	for _, path := range schema.HasProviderDefault() {
		set[path] = struct{}{}
	}
	for path, hint := range schema.Hints {
		if hint.CoOwned != nil {
			set[path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// coOwnedSuppressedDiff classifies movement on a CoOwned field by ownership
// rather than by witness: it partitions the field's member identities into
// Declared/FormerlyOwned/NeverOwned (pkgmodel.PartitionOwnership) using
// desired's identities as Declared, the stored record's identities as Prior
// (discarded — see IdentityRule — when the record's Rule does not match this
// hint, so a stale record degrades to "nothing is prior" rather than a
// best-effort comparison), and the union of both sides' identities as Live.
// Only NeverOwned members are compared between oldProps and newProps; a
// Declared or FormerlyOwned member's movement is real drift the plan itself
// surfaces and produces no note here, regardless of how it moved.
func coOwnedSuppressedDiff(oldProps, newProps, desired json.RawMessage, path string, hint pkgmodel.FieldHint, priorOwned pkgmodel.OwnedMembers, schema pkgmodel.Schema) (SuppressedFieldDiff, bool) {
	oldVal := gjson.GetBytes(oldProps, path)
	newVal := gjson.GetBytes(newProps, path)
	desiredVal := gjson.GetBytes(desired, path)

	declared := pkgmodel.MemberIdentities(desiredVal, hint)
	live := unionStrings(pkgmodel.MemberIdentities(oldVal, hint), pkgmodel.MemberIdentities(newVal, hint))

	var prior []string
	if record, ok := priorOwned[path]; ok && record.Rule == pkgmodel.IdentityRule(hint) {
		prior = record.Members
	}

	partition := pkgmodel.PartitionOwnership(declared, prior, live)
	if len(partition.NeverOwned) == 0 {
		return SuppressedFieldDiff{}, false
	}

	oldRestricted := restrictToNeverOwned(oldVal, hint, partition.NeverOwned)
	newRestricted := restrictToNeverOwned(newVal, hint, partition.NeverOwned)
	if canonicalJSON(oldRestricted) == canonicalJSON(newRestricted) {
		return SuppressedFieldDiff{}, false
	}

	if pathOpaque(schema, path) || valueHasOpaqueMarker(oldRestricted) || valueHasOpaqueMarker(newRestricted) {
		return SuppressedFieldDiff{Path: path, Opaque: true}, true
	}
	return SuppressedFieldDiff{
		Path: path,
		From: renderRestrictedMembers(oldRestricted),
		To:   renderRestrictedMembers(newRestricted),
	}, true
}

// restrictToNeverOwned returns value's content narrowed to the members whose
// identity is in keep: for an object (Mapping), the entries whose key is
// kept; for an array (Set/EntitySet), the elements whose identity (computed
// exactly as pkgmodel.MemberIdentities computes it — see
// singleElementIdentity) is kept, sorted for a stable, order-independent
// comparison and rendering. Returns nil when nothing is kept, so two sides
// with no never-owned content compare equal regardless of shape.
func restrictToNeverOwned(value gjson.Result, hint pkgmodel.FieldHint, keep map[string]struct{}) any {
	switch {
	case value.IsObject():
		out := map[string]any{}
		value.ForEach(func(key, val gjson.Result) bool {
			if _, ok := keep[key.String()]; ok {
				out[key.String()] = val.Value()
			}
			return true
		})
		if len(out) == 0 {
			return nil
		}
		return out
	case value.IsArray():
		var out []any
		value.ForEach(func(_, el gjson.Result) bool {
			if id, ok := singleElementIdentity(el, hint); ok {
				if _, keepIt := keep[id]; keepIt {
					out = append(out, el.Value())
				}
			}
			return true
		})
		if len(out) == 0 {
			return nil
		}
		return sortedByKey(out, hint.IndexField)
	default:
		return nil
	}
}

// singleElementIdentity computes one array element's member identity by
// reusing pkgmodel.MemberIdentities itself — wrapping the element's raw JSON
// as a singleton array and asking for the identity set of that array — so a
// Set element's canonicalized-and-envelope-flattened identity, or an
// EntitySet element's IndexField-derived identity, is computed by the exact
// same logic that produced the identity sets fed to PartitionOwnership.
// Returns false when the element contributes no identity (e.g. an
// EntitySet element missing its IndexField, or an unresolvable reference
// envelope), matching MemberIdentities' own "contributes nothing" cases.
func singleElementIdentity(el gjson.Result, hint pkgmodel.FieldHint) (string, bool) {
	wrapped := gjson.Parse("[" + el.Raw + "]")
	ids := pkgmodel.MemberIdentities(wrapped, hint)
	if len(ids) != 1 {
		return "", false
	}
	return ids[0], true
}

// renderRestrictedMembers is compareWholeField's absence convention applied
// to restricted member content: nil (no value on this side) renders as no
// JSON at all, never a literal null.
func renderRestrictedMembers(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	return json.RawMessage(canonicalJSON(v))
}

// unionStrings returns the sorted, deduplicated union of a and b.
func unionStrings(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
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
	sort.Strings(out)
	return out
}

func unmarshalPropsObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// compareWholeField compares a fully suppressed field per its collection
// semantics. A side where the field is absent renders as nil.
func compareWholeField(oldMap, newMap map[string]any, path string, hint pkgmodel.FieldHint) (json.RawMessage, json.RawMessage, bool) {
	oldVal, oldHas := oldMap[path]
	newVal, newHas := newMap[path]
	if !oldHas && !newHas {
		return nil, nil, false
	}

	equal := false
	switch {
	case oldHas != newHas:
		equal = false
	case hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "":
		oldArr, _ := oldVal.([]any)
		newArr, _ := newVal.([]any)
		equal = entitySetContentEqual(oldArr, newArr, hint.IndexField)
	case hint.UpdateMethod == pkgmodel.FieldUpdateMethodArray:
		equal = canonicalJSON(oldVal) == canonicalJSON(newVal)
	default:
		oldArr, oldIsArr := oldVal.([]any)
		newArr, newIsArr := newVal.([]any)
		if oldIsArr && newIsArr {
			equal = multisetEqual(oldArr, newArr)
		} else {
			equal = canonicalJSON(oldVal) == canonicalJSON(newVal)
		}
	}
	if equal {
		return nil, nil, false
	}

	var from, to json.RawMessage
	if oldHas {
		from = json.RawMessage(canonicalJSON(oldVal))
	}
	if newHas {
		to = json.RawMessage(canonicalJSON(newVal))
	}
	return from, to, true
}

// compareEntitySetSubsets compares only the elements whose key is not among
// the declared keys: the portion removeProviderDefaultEntitySetElements
// filters out of the diff. Keyless elements stay visible to the diff (the
// filter keeps them), so they are not suppressed content here.
func compareEntitySetSubsets(oldVal, newVal any, indexField string, declaredKeys map[string]struct{}) (json.RawMessage, json.RawMessage, bool) {
	oldArr, _ := oldVal.([]any)
	newArr, _ := newVal.([]any)
	oldSub := suppressedEntitySetElements(oldArr, indexField, declaredKeys)
	newSub := suppressedEntitySetElements(newArr, indexField, declaredKeys)
	if entitySetContentEqual(oldSub, newSub, indexField) {
		return nil, nil, false
	}
	return json.RawMessage(canonicalJSON(sortedByKey(oldSub, indexField))),
		json.RawMessage(canonicalJSON(sortedByKey(newSub, indexField))),
		true
}

func suppressedEntitySetElements(arr []any, indexField string, declaredKeys map[string]struct{}) []any {
	var out []any
	for _, elem := range arr {
		elemMap, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		keyVal, ok := elemMap[indexField]
		if !ok {
			continue
		}
		if _, declared := declaredKeys[fmt.Sprintf("%v", keyVal)]; declared {
			continue
		}
		out = append(out, elem)
	}
	return out
}

func entitySetKeys(val any, indexField string) map[string]struct{} {
	keys := map[string]struct{}{}
	arr, _ := val.([]any)
	for _, elem := range arr {
		if elemMap, ok := elem.(map[string]any); ok {
			if keyVal, ok := elemMap[indexField]; ok {
				keys[fmt.Sprintf("%v", keyVal)] = struct{}{}
			}
		}
	}
	return keys
}

// entitySetContentEqual compares keyed elements by key and keyless elements
// as an unordered multiset, so element order is never movement.
func entitySetContentEqual(a, b []any, indexField string) bool {
	aKeyed, aKeyless := partitionByKey(a, indexField)
	bKeyed, bKeyless := partitionByKey(b, indexField)
	if len(aKeyed) != len(bKeyed) {
		return false
	}
	for k, v := range aKeyed {
		if bKeyed[k] != v {
			return false
		}
	}
	return multisetEqual(aKeyless, bKeyless)
}

func partitionByKey(arr []any, indexField string) (map[string]string, []any) {
	keyed := map[string]string{}
	var keyless []any
	for _, elem := range arr {
		if elemMap, ok := elem.(map[string]any); ok {
			if keyVal, ok := elemMap[indexField]; ok {
				keyed[fmt.Sprintf("%v", keyVal)] = canonicalJSON(elem)
				continue
			}
		}
		keyless = append(keyless, elem)
	}
	return keyed, keyless
}

func sortedByKey(arr []any, indexField string) []any {
	out := append([]any(nil), arr...)
	sort.Slice(out, func(i, j int) bool {
		ki, kj := "", ""
		if m, ok := out[i].(map[string]any); ok {
			ki = fmt.Sprintf("%v", m[indexField])
		}
		if m, ok := out[j].(map[string]any); ok {
			kj = fmt.Sprintf("%v", m[indexField])
		}
		if ki != kj {
			return ki < kj
		}
		return canonicalJSON(out[i]) < canonicalJSON(out[j])
	})
	if out == nil {
		out = []any{}
	}
	return out
}

// compareLeafMultisets compares two suppressed-leaf collections. Set-based
// array comparison has no stable element pairing, so the honest comparison
// is the multiset of leaf values on each side.
func compareLeafMultisets(oldLeaves, newLeaves []any) (json.RawMessage, json.RawMessage, bool) {
	if multisetEqual(oldLeaves, newLeaves) {
		return nil, nil, false
	}
	return renderLeafList(oldLeaves), renderLeafList(newLeaves), true
}

func renderLeafList(leaves []any) json.RawMessage {
	if len(leaves) == 0 {
		return nil
	}
	sorted := append([]any(nil), leaves...)
	sort.Slice(sorted, func(i, j int) bool { return canonicalJSON(sorted[i]) < canonicalJSON(sorted[j]) })
	return json.RawMessage(canonicalJSON(sorted))
}

// collectSuppressedLeaves walks a dotted path with the strip's own regime
// rules (see stripProviderDefaultPath): descending through an array switches
// to the unconditional regime, where every reachable leaf is suppressed
// content regardless of what desired declares; a walk that stays in objects
// keeps the conditional regime, where the leaf is suppressed only when
// desired omits the full path.
func collectSuppressedLeaves(root any, desired map[string]any, parts []string) []any {
	var out []any
	declared := fieldExistsInMap(desired, parts)
	var walk func(node any, rest []string, inArray bool)
	walk = func(node any, rest []string, inArray bool) {
		switch v := node.(type) {
		case []any:
			for _, elem := range v {
				walk(elem, rest, true)
			}
		case map[string]any:
			val, ok := v[rest[0]]
			if !ok {
				return
			}
			if len(rest) == 1 {
				if inArray || !declared {
					out = append(out, val)
				}
				return
			}
			walk(val, rest[1:], inArray)
		}
	}
	walk(root, parts, false)
	return out
}

func multisetEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, v := range a {
		counts[canonicalJSON(v)]++
	}
	for _, v := range b {
		key := canonicalJSON(v)
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	return true
}

// canonicalJSON renders a decoded value deterministically (encoding/json
// sorts map keys), so equality can be a string comparison.
func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

// pathOpaque reports whether an Opaque schema hint covers the path: the hint
// on the path itself, on an ancestor (an opaque container makes its content
// opaque), or on a descendant (an opaque leaf makes the container's content
// unsafe to render).
func pathOpaque(schema pkgmodel.Schema, path string) bool {
	for _, op := range schema.Opaque() {
		if op == path || strings.HasPrefix(path, op+".") || strings.HasPrefix(op, path+".") {
			return true
		}
	}
	return false
}

// valueHasOpaqueMarker reports whether a decoded value carries envelope
// opacity anywhere within: a $visibility: Opaque marker or $hashed material.
// Fails toward opaque so sanitization never depends on the schema alone.
func valueHasOpaqueMarker(v any) bool {
	switch node := v.(type) {
	case map[string]any:
		if vis, ok := node["$visibility"].(string); ok && vis == "Opaque" {
			return true
		}
		if _, ok := node["$hashed"]; ok {
			return true
		}
		for _, child := range node {
			if valueHasOpaqueMarker(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if valueHasOpaqueMarker(child) {
				return true
			}
		}
	}
	return false
}

// witnessedValue reports whether the witness document holds content a note
// can claim moved: a present scalar (false and zero are values), a
// non-empty object, or a non-empty collection. Absent fields, nulls, and
// empty collections witness nothing; whatever appears on them later is
// first-time population.
func witnessedValue(v any, has bool) bool {
	if !has {
		return false
	}
	switch t := v.(type) {
	case nil:
		return false
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// anyWitnessedLeaf reports whether at least one collected leaf is a real
// value (not null, not an empty collection).
func anyWitnessedLeaf(leaves []any) bool {
	for _, v := range leaves {
		if witnessedValue(v, true) {
			return true
		}
	}
	return false
}

// AssertWitnessedSuppressed returns the desired properties with formae's
// witnessed values asserted onto the provider-default fields the user
// omitted, so a forced reconcile diffs against them and reverts out-of-band
// movement the same way it overwrites drift on declared fields. Assertion
// follows the strip predicates exactly:
//
//   - an omitted (absent or null) top-level hasProviderDefault field takes
//     the witness value when the witness holds one;
//   - a partially declared EntitySet field keeps the declared entries and
//     gains the witness's undeclared (suppressed) entries;
//   - a declared field is intent and is never overridden; an explicit empty
//     EntitySet declaration is a drain and is never refilled;
//   - opaque fields are skipped (the stored witness holds hashes, which must
//     never be asserted as values), createOnly fields are skipped (a witness
//     assertion must never plan a replacement), and dotted array-nested
//     paths are skipped (set-based elements have no stable injection
//     target).
func AssertWitnessedSuppressed(desired, witness json.RawMessage, schema pkgmodel.Schema) (json.RawMessage, error) {
	if len(witness) == 0 {
		return desired, nil
	}
	desiredMap, err := unmarshalPropsObject(desired)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal desired properties: %w", err)
	}
	witnessMap, err := unmarshalPropsObject(witness)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal witness properties: %w", err)
	}

	createOnly := map[string]bool{}
	for _, f := range schema.CreateOnly() {
		createOnly[f] = true
	}

	changed := false
	for _, path := range schema.HasProviderDefault() {
		if strings.Contains(path, ".") || createOnly[path] || pathOpaque(schema, path) {
			continue
		}
		wVal, wHas := witnessMap[path]
		if !witnessedValue(wVal, wHas) || valueHasOpaqueMarker(wVal) {
			continue
		}
		hint := schema.Hints[path]
		parts := []string{path}
		if !fieldExistsInMap(desiredMap, parts) {
			desiredMap[path] = wVal
			changed = true
			continue
		}
		if hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "" {
			declaredKeys := entitySetKeys(desiredMap[path], hint.IndexField)
			if len(declaredKeys) == 0 {
				// Explicit empty declaration: a drain, never refilled.
				continue
			}
			wArr, _ := wVal.([]any)
			suppressed := suppressedEntitySetElements(wArr, hint.IndexField, declaredKeys)
			if len(suppressed) == 0 {
				continue
			}
			declared, _ := desiredMap[path].([]any)
			desiredMap[path] = append(append([]any{}, declared...), sortedByKey(suppressed, hint.IndexField)...)
			changed = true
		}
	}

	if !changed {
		return desired, nil
	}
	out, err := json.Marshal(desiredMap)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}
