// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/querier"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func (m *Metastructure) ExtractResources(query string) (*pkgmodel.Forma, error) {
	q := querier.NewBlugeQuerier(m.Datastore)
	resources, err := q.QueryResources(query)
	if err != nil {
		slog.Debug("Cannot get resources from query", "error", err)
		return nil, err
	}

	generators, err := m.reverseTranslateKSUIDsToTriplets(resources)
	if err != nil {
		slog.Error("Failed to reverse translate KSUIDs to triplets", "error", err)
		return nil, err
	}

	targetNames := make([]string, 0)
	uniqueTargets := make(map[string]struct{})
	stackLabels := make([]string, 0)
	uniqueStacks := make(map[string]struct{})

	for _, resource := range resources {
		if resource.Target != "" {
			if _, exists := uniqueTargets[resource.Target]; !exists {
				uniqueTargets[resource.Target] = struct{}{}
				targetNames = append(targetNames, resource.Target)
			}
		}
		if resource.Stack != "" {
			if _, exists := uniqueStacks[resource.Stack]; !exists {
				uniqueStacks[resource.Stack] = struct{}{}
				stackLabels = append(stackLabels, resource.Stack)
			}
		}
	}

	// A generator belongs to one stack and is meant to be bound from others,
	// so the stack owning one the extracted resources reference need not hold
	// any resource the query matched. Its stack is emitted alongside theirs,
	// or the generator declaration names a stack the file does not declare.
	for _, generator := range generators {
		stack := generator.GetStack()
		if stack == "" {
			continue
		}
		if _, exists := uniqueStacks[stack]; !exists {
			uniqueStacks[stack] = struct{}{}
			stackLabels = append(stackLabels, stack)
		}
	}

	forma := pkgmodel.FormaFromResources(resources)

	// Extract copies live resource state into a forma declaration verbatim.
	// A co-owned collection field's live content legitimately holds entries
	// other writers put there (the platform, another forma), so emitting it
	// unfiltered would hand the caller a declaration that claims members it
	// never wrote — the next apply would then try to "own" them. Narrow each
	// such field down to the portion this forma may actually claim, and strip
	// the ownership bookkeeping itself: it is agent-internal and must never
	// appear in an emitted declaration.
	for i := range forma.Resources {
		forma.Resources[i] = filterCoOwnedResource(forma.Resources[i])
	}

	if len(targetNames) > 0 {
		targets, err := m.Datastore.LoadTargetsByLabels(targetNames)
		if err != nil {
			slog.Error("Failed to load targets by names", "error", err)
			return nil, err
		}

		forma.Targets = make([]pkgmodel.Target, 0, len(targets))
		for _, t := range targets {
			if t != nil {
				forma.Targets = append(forma.Targets, *t)
			}
		}
	}

	if len(stackLabels) > 0 {
		foundStacks, err := m.Datastore.LoadStacksByLabels(stackLabels)
		if err != nil {
			slog.Error("Failed to load stacks by labels", "error", err)
			return nil, err
		}

		// Build a lookup so we can synthesize entries for labels with no datastore row.
		stackByLabel := make(map[string]*pkgmodel.Stack, len(foundStacks))
		for _, s := range foundStacks {
			stackByLabel[s.Label] = s
		}

		forma.Stacks = make([]pkgmodel.Stack, 0, len(stackLabels))
		for _, label := range stackLabels {
			if s, ok := stackByLabel[label]; ok {
				forma.Stacks = append(forma.Stacks, *s)
			} else {
				// Stack not found in datastore (e.g. $unmanaged): synthesize a
				// minimal entry with no description. Description is optional, so we
				// no longer emit a placeholder ("Resources imported with formae
				// extract") that the user would then have to strip by hand.
				forma.Stacks = append(forma.Stacks, pkgmodel.Stack{Label: label})
			}
		}
	}

	// Collect referenced standalone policy labels from stacks
	uniquePolicyLabels := make(map[string]struct{})
	for _, stack := range forma.Stacks {
		for _, rawPolicy := range stack.Policies {
			if pkgmodel.IsPolicyReference(rawPolicy) {
				policyLabel, err := pkgmodel.ParsePolicyReference(rawPolicy)
				if err != nil {
					slog.Debug("Failed to parse policy reference", "error", err)
					continue
				}
				uniquePolicyLabels[policyLabel] = struct{}{}
			}
		}
	}

	// Load standalone policies and add to forma
	if len(uniquePolicyLabels) > 0 {
		policyLabels := make([]string, 0, len(uniquePolicyLabels))
		for label := range uniquePolicyLabels {
			policyLabels = append(policyLabels, label)
		}

		foundPolicies, err := m.Datastore.LoadStandalonePoliciesByLabels(policyLabels)
		if err != nil {
			slog.Error("Failed to load standalone policies by labels", "error", err)
			return nil, err
		}

		forma.Policies = make([]json.RawMessage, 0, len(foundPolicies))
		for _, policy := range foundPolicies {
			policyJSON, err := json.Marshal(policy)
			if err != nil {
				slog.Error("Failed to marshal standalone policy", "label", policy.GetLabel(), "error", err)
				continue
			}
			forma.Policies = append(forma.Policies, policyJSON)
		}
	}

	// A forma that references a generator has to declare it, or the file it is
	// written to cannot be applied on its own.
	if len(generators) > 0 {
		forma.Generators = make([]json.RawMessage, 0, len(generators))
		for _, generator := range generators {
			generatorJSON, err := json.Marshal(generator)
			if err != nil {
				slog.Error("Failed to marshal generator", "label", generator.GetLabel(), "error", err)
				continue
			}
			forma.Generators = append(forma.Generators, generatorJSON)
		}
	}

	return forma, nil
}

func (m *Metastructure) ExtractTargets(queryStr string) ([]*pkgmodel.Target, error) {
	slog.Debug("ExtractTargets called", "queryStr", queryStr)
	query := &datastore.TargetQuery{}

	if queryStr != "" {
		parts := strings.Fields(queryStr)
		for _, part := range parts {
			if strings.Contains(part, ":") {
				kv := strings.SplitN(part, ":", 2)
				key := strings.TrimSpace(kv[0])
				value := strings.TrimSpace(kv[1])

				switch key {
				case "label":
					query.Label = &datastore.QueryItem[string]{
						Item:       value,
						Constraint: datastore.Required,
					}
				case "namespace":
					query.Namespace = &datastore.QueryItem[string]{
						Item:       value,
						Constraint: datastore.Required,
					}
				case "discoverable":
					boolVal := value == "true"
					query.Discoverable = &datastore.QueryItem[bool]{
						Item:       boolVal,
						Constraint: datastore.Required,
					}
				}
			}
		}
	}

	slog.Debug("Calling QueryTargets", "query", query)
	targets, err := m.Datastore.QueryTargets(query)
	if err != nil {
		slog.Debug("Cannot get targets from query", "error", err)
		return nil, err
	}

	slog.Debug("ExtractTargets returning", "count", len(targets))
	return targets, nil
}

func (m *Metastructure) ExtractStacks() ([]*pkgmodel.Stack, error) {
	slog.Debug("ExtractStacks called")
	stacks, err := m.Datastore.ListAllStacks()
	if err != nil {
		slog.Debug("Cannot get stacks from datastore", "error", err)
		return nil, err
	}

	// Build a lookup of last reconcile times per stack
	reconcileInfos, err := m.Datastore.GetStacksWithAutoReconcilePolicy()
	lastReconcileByStack := make(map[string]time.Time)
	if err != nil {
		slog.Warn("Failed to get auto-reconcile info", "error", err)
	} else {
		for _, info := range reconcileInfos {
			lastReconcileByStack[info.StackLabel] = info.LastReconcileAt
		}
	}

	// Populate policies for each stack
	for _, stack := range stacks {
		policies, err := m.Datastore.GetPoliciesForStack(stack.ID)
		if err != nil {
			slog.Warn("Failed to get policies for stack", "stack", stack.Label, "error", err)
			continue
		}
		// Convert policies to json.RawMessage for the Stack.Policies field
		for _, policy := range policies {
			// Enrich auto-reconcile policies with last reconcile time
			if arPolicy, ok := policy.(*pkgmodel.AutoReconcilePolicy); ok {
				if lastRecon, found := lastReconcileByStack[stack.Label]; found {
					arPolicy.LastReconcileAt = lastRecon
				}
			}
			policyJSON, err := json.Marshal(policy)
			if err != nil {
				slog.Warn("Failed to marshal policy", "policy", policy.GetLabel(), "error", err)
				continue
			}
			stack.Policies = append(stack.Policies, json.RawMessage(policyJSON))
		}
	}

	slog.Debug("ExtractStacks returning", "count", len(stacks))
	return stacks, nil
}

// filterCoOwnedResource returns a copy of res with every CoOwned-hinted
// collection field narrowed to the members this forma may claim, and its
// ownership record cleared.
//
// res arrives as a shallow copy already (FormaFromResources built it by
// dereferencing the datastore's *Resource), which means its Properties field
// is still a slice header pointing at the very same backing array the stored
// resource uses. append(json.RawMessage(nil), res.Properties...) below always
// allocates a fresh backing array — append onto a nil slice can never reuse
// the source's capacity — so res.Properties is exclusively this clone's own
// before any filtering call touches it. That makes safety here structural,
// not a bet on how sjson's Set/Delete happen to manage capacity internally:
// whatever they do, it lands on a backing array the stored resource never
// had a slice header pointing at.
func filterCoOwnedResource(res pkgmodel.Resource) pkgmodel.Resource {
	res.Properties = append(json.RawMessage(nil), res.Properties...)

	filtered, err := filterCoOwnedProperties(res.Properties, res.Schema, res.OwnedMembers)
	if err != nil {
		// Filtering failed for this resource (malformed hint path, etc.): emit
		// its properties unfiltered rather than dropping the resource from the
		// extract entirely. OwnedMembers is still cleared below regardless, so
		// the bookkeeping never leaks even on this fallback path.
		slog.Warn("Failed to filter co-owned collection for extract; emitting unfiltered",
			"resource", res.Label, "error", err)
	} else {
		res.Properties = filtered
	}

	// OwnedMembers is agent bookkeeping — the set of member identities a prior
	// apply claimed. It must never reach an emitted declaration: a forma file
	// only ever declares state, not the agent's internal record of what it
	// last wrote.
	res.OwnedMembers = nil

	return res
}

// filterCoOwnedProperties narrows every CoOwned-hinted collection in
// properties down to the members this forma may emit:
//
//   - An interpretable ownership record (one whose Rule still matches the
//     field's current hint — see pkgmodel.IdentityRule) is authoritative:
//     keep exactly the members named in record.Members.
//   - With no interpretable record (never claimed, or the record is stale),
//     fall back to CoOwned.SystemPatterns: drop any member whose identity
//     matches at least one pattern, keep the rest. No patterns at all means
//     nothing is known to be platform-injected, so nothing is dropped — the
//     field is emitted exactly as extract emits it today.
//
// A field paired with Opaque is skipped entirely: its "members" would be
// secret values rather than names, the PKL schema already refuses to author
// that combination (see co_owned_field_opaque_test.pkl), and claimedMembers
// never records a claim for one either — so such a hint is never treated as
// co-owned here.
func filterCoOwnedProperties(properties json.RawMessage, schema pkgmodel.Schema, owned pkgmodel.OwnedMembers) (json.RawMessage, error) {
	if len(properties) == 0 {
		return properties, nil
	}

	out := properties
	for fieldPath, hint := range schema.Hints {
		if hint.CoOwned == nil || hint.Opaque {
			continue
		}

		val := gjson.GetBytes(out, fieldPath)
		if !val.Exists() {
			continue
		}

		keep, active := coOwnedKeepFunc(fieldPath, hint, owned)
		if !active {
			continue
		}

		var err error
		switch {
		case val.IsObject():
			out, err = filterObjectMembers(out, fieldPath, val, keep)
		case val.IsArray():
			out, err = filterArrayMembers(out, fieldPath, val, hint, keep)
		default:
			// A CoOwned field is a collection by definition; a scalar here
			// means the live value does not match its own schema. Nothing
			// sensible to filter — leave it exactly as extract found it.
			continue
		}
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

// coOwnedKeepFunc returns the membership test for one CoOwned-hinted field,
// and whether any filtering is actually called for. active is false only
// when there is neither an interpretable record nor any SystemPatterns to
// apply — the "emit everything" case, left as a no-op rather than a
// keep-everything predicate so callers can skip the field untouched.
func coOwnedKeepFunc(fieldPath string, hint pkgmodel.FieldHint, owned pkgmodel.OwnedMembers) (keep func(identity string) bool, active bool) {
	if record, ok := owned[fieldPath]; ok && record.Rule == pkgmodel.IdentityRule(hint) {
		members := make(map[string]struct{}, len(record.Members))
		for _, m := range record.Members {
			members[m] = struct{}{}
		}
		return func(identity string) bool {
			_, ok := members[identity]
			return ok
		}, true
	}

	patterns := hint.CoOwned.SystemPatterns
	if len(patterns) == 0 {
		return nil, false
	}

	return func(identity string) bool {
		// Member identities are pkgmodel.MemberIdentities' canonical form: a
		// Mapping key verbatim, but an EntitySet/Set element's json-marshaled
		// value — quoted for a string. A SystemPatterns author writes a plain
		// glob ("aws:*") without knowing which shape they're matching against,
		// so a JSON-string identity is unquoted before matching: it and only
		// it can carry a leading/trailing '"' as an artifact of marshaling
		// rather than as data. Any other JSON shape (object, array, number,
		// bool, or a Mapping key) matches in its literal form, unchanged. This
		// is purely a glob-matching convenience — record.Members comparison
		// above stays on the marshaled encoding on both sides, so it never
		// needs unquoting to agree with itself.
		target := unquoteJSONStringIdentity(identity)
		for _, pattern := range patterns {
			// path.Match's glob semantics: "*" matches any sequence of
			// non-separator ('/') characters, "?" matches any single
			// non-separator character. Member identities are provider names
			// (e.g. "aws:cloudformation:stack"), not paths, so this is only
			// ever exercised as a plain substring-style wildcard — "aws:*"
			// matches "aws:cloudformation:stack" but "/" in an identity would
			// not cross a "*" the way it might look like it should.
			if matched, _ := path.Match(pattern, target); matched {
				return false
			}
		}
		return true
	}, true
}

// unquoteJSONStringIdentity undoes JSON string quoting on identity if, and
// only if, identity is itself a JSON string literal (starts and ends with an
// unescaped '"'). Any other shape — including a Mapping key, which was never
// JSON-marshaled to begin with — is returned unchanged. Malformed input
// (identity looks quoted but isn't valid JSON) is also returned unchanged
// rather than erroring: this only ever feeds a best-effort glob match.
func unquoteJSONStringIdentity(identity string) string {
	if len(identity) < 2 || identity[0] != '"' || identity[len(identity)-1] != '"' {
		return identity
	}
	var s string
	if err := json.Unmarshal([]byte(identity), &s); err != nil {
		return identity
	}
	return s
}

// filterObjectMembers drops every key of the object at fieldPath in doc for
// which keep reports false, leaving the surviving keys' values byte-for-byte
// untouched. An object left with no keys serializes as "{}" — the same empty
// Mapping extract already emits for a field with no live content.
func filterObjectMembers(doc json.RawMessage, fieldPath string, val gjson.Result, keep func(string) bool) (json.RawMessage, error) {
	var toDelete []string
	val.ForEach(func(key, _ gjson.Result) bool {
		if !keep(key.String()) {
			toDelete = append(toDelete, key.String())
		}
		return true
	})

	out := doc
	for _, key := range toDelete {
		updated, err := sjson.DeleteBytes(out, fieldPath+"."+escapeGjsonPathKey(key))
		if err != nil {
			return nil, fmt.Errorf("failed to drop unowned member %q at %q: %w", key, fieldPath, err)
		}
		out = updated
	}
	return out, nil
}

// filterArrayMembers drops every element of the array at fieldPath in doc
// whose identity (per hint's UpdateMethod — EntitySet's IndexField, or the
// element's own canonical JSON otherwise) keep reports false for. An element
// whose identity cannot be determined (an unresolved reference envelope) is
// left in place: there is nothing to test it against. An array left with no
// elements serializes as "[]" — the same empty Listing extract already emits
// for a field with no live content.
func filterArrayMembers(doc json.RawMessage, fieldPath string, val gjson.Result, hint pkgmodel.FieldHint, keep func(string) bool) (json.RawMessage, error) {
	elems := val.Array()
	var toDelete []int
	for i, el := range elems {
		identity, ok := singleMemberIdentity(el, hint)
		if !ok {
			continue
		}
		if !keep(identity) {
			toDelete = append(toDelete, i)
		}
	}

	out := doc
	// Delete from the highest index down so earlier deletions never shift the
	// index of an element still queued for removal.
	for i := len(toDelete) - 1; i >= 0; i-- {
		idx := toDelete[i]
		updated, err := sjson.DeleteBytes(out, fmt.Sprintf("%s.%d", fieldPath, idx))
		if err != nil {
			return nil, fmt.Errorf("failed to drop unowned array member at %q[%d]: %w", fieldPath, idx, err)
		}
		out = updated
	}
	return out, nil
}

// singleMemberIdentity computes one array element's identity by reusing
// pkgmodel.MemberIdentities against a synthetic one-element array holding
// just that element — the same envelope-flattening and per-rule logic
// MemberIdentities already applies to a whole collection, without
// duplicating it here. Returns false if the element contributes no identity
// at all (an envelope missing $value), matching MemberIdentities' own
// "contributes nothing" handling of such elements.
func singleMemberIdentity(el gjson.Result, hint pkgmodel.FieldHint) (string, bool) {
	wrapped := gjson.Parse("[" + el.Raw + "]")
	ids := pkgmodel.MemberIdentities(wrapped, hint)
	if len(ids) != 1 {
		return "", false
	}
	return ids[0], true
}

// escapeGjsonPathKey escapes the gjson/sjson path metacharacters a map key
// might contain, so a key like "a.b" addresses itself as one literal key
// rather than being read as nested path syntax.
//
// The set is: '\' and '.', which sjson's path parser always treats specially
// (escape-mode trigger and path-segment separator, handled before any
// per-character check runs — see parsePath in sjson.go), plus every
// character sjson's own isSimpleChar predicate rejects — '|', '#', '@', '*',
// '?' (github.com/tidwall/sjson@v1.2.5/sjson.go:45-51). Deriving the set from
// that predicate, rather than hand-picking characters that "look" special,
// is what catches '|': a key containing it would otherwise fail DeleteBytes
// and fall back to emitting the field unfiltered.
const gjsonPathSpecialChars = `\.|#@*?`

func escapeGjsonPathKey(key string) string {
	if !strings.ContainsAny(key, gjsonPathSpecialChars) {
		return key
	}
	var escaped strings.Builder
	escaped.Grow(len(key) + 4)
	for _, r := range key {
		if strings.ContainsRune(gjsonPathSpecialChars, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
