// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/canonicalize"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
	"github.com/tidwall/gjson"
)

var defaultIgnoredFields = []jsonpatch.Path{}

// GeneratePatch returns the JSON-patch documents describing the diff between
// document (actual state) and patch (desired state):
//
//   - patchDocument holds the mutable-field ops; this is what gets sent to
//     the plugin for an in-place update.
//   - createOnlyPatch holds the ops that target createOnly (immutable)
//     fields. When non-empty, the caller must plan a destroy+create rather
//     than an update; the ops are used purely for CLI rendering ("because
//     these immutable properties changed: …") and are never sent to plugins.
//   - onlyForceResent reports whether patchDocument's ops (and any
//     createOnlyPatch ops) consist ENTIRELY of requiredOnUpdate fields
//     force-resent to guarantee the provider sees them, with no op reflecting
//     an actual change. patchDocument itself is never stripped of these ops —
//     content is never dropped here, only reported. A caller deciding whether
//     to PLAN an update at all should treat this the same as an empty patch; a
//     caller regenerating the payload for an update that is already happening
//     must ignore this and send patchDocument as returned, because the
//     provider still requires the force-resent field in that payload.
//
// The two slices are disjoint. Either can be nil.
func GeneratePatch(document []byte, patch []byte, storedEnvelopes []byte, desiredEnvelopes []byte, properties resolver.ResolvableProperties, schema pkgmodel.Schema, mode pkgmodel.FormaApplyMode) (json.RawMessage, json.RawMessage, bool, error) {
	return generatePatch(document, patch, storedEnvelopes, desiredEnvelopes, properties, schema, mode)
}

func collectionSemanticsFromFieldHints(hints map[string]pkgmodel.FieldHint) jsonpatch.Collections {
	collections := jsonpatch.Collections{
		EntitySets: jsonpatch.EntitySets{},
		Arrays:     []jsonpatch.Path{},
		Atomics:    []jsonpatch.Path{},
	}

	for field, hint := range hints {
		path := jsonpatch.Path(fmt.Sprintf("$.%s", field))
		switch hint.UpdateMethod {
		case pkgmodel.FieldUpdateMethodEntitySet:
			collections.EntitySets[path] = jsonpatch.Key(hint.IndexField)
		case pkgmodel.FieldUpdateMethodArray:
			collections.Arrays = append(collections.Arrays, path)
		case pkgmodel.FieldUpdateMethodAtomic:
			collections.Atomics = append(collections.Atomics, path)
		}
	}

	return collections
}

// topLevelConvergeFields collects the schema fields whose destination path was
// converge-marked by the provenance classification. Only whole top-level
// fields are relevant: the empty-value drop this exemption bypasses filters
// top-level fields alone, and nested occurrences survive it by construction.
func topLevelConvergeFields(schemaFields []string, properties resolver.ResolvableProperties) map[string]bool {
	fields := map[string]bool{}
	for _, field := range schemaFields {
		if properties.ConvergeMarkedAt(field) {
			fields[field] = true
		}
	}
	return fields
}

func entitySetProviderDefaultsFromHints(hints map[string]pkgmodel.FieldHint) map[string]string {
	result := map[string]string{}
	for field, hint := range hints {
		if hint.HasProviderDefault && hint.UpdateMethod == pkgmodel.FieldUpdateMethodEntitySet && hint.IndexField != "" {
			result[field] = hint.IndexField
		}
	}
	return result
}

func generatePatch(document []byte, patch []byte, storedEnvelopes []byte, desiredEnvelopes []byte, properties resolver.ResolvableProperties, schema pkgmodel.Schema, mode pkgmodel.FormaApplyMode) (json.RawMessage, json.RawMessage, bool, error) {
	flattenedDocument, flattenedPatch, err := flattenAndResolveRefs(document, patch, storedEnvelopes, desiredEnvelopes, properties)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to flatten and resolve refs: %w", err)
	}

	var strategy jsonpatch.PatchStrategy
	switch mode {
	case pkgmodel.FormaApplyModeReconcile:
		strategy = jsonpatch.PatchStrategyExactMatch
	case pkgmodel.FormaApplyModePatch:
		strategy = jsonpatch.PatchStrategyEnsureExists
	default:
		return nil, nil, false, fmt.Errorf("unable to generate patch document for apply mode: %s", mode)
	}

	flattenedPatch, err = StripFieldsWithoutBaseline(flattenedPatch, flattenedDocument, schema)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to strip writeOnly+createOnly fields from desired state: %w", err)
	}

	requiredOnUpdateFields := schema.RequiredOnUpdate()
	patchOps, err := createPatchDocument(flattenedDocument, flattenedPatch, schema.Fields, requiredOnUpdateFields, schema.HasProviderDefault(), entitySetProviderDefaultsFromHints(schema.Hints), collectionSemanticsFromFieldHints(schema.Hints), defaultIgnoredFields, strategy, topLevelConvergeFields(schema.Fields, properties))
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to create patch document: %w", err)
	}

	// Remove spurious patch operations that add empty arrays or maps.
	// The PKL schema renders unset nullable Listing/Mapping fields as
	// []/{}. An "add" of an empty collection to a field absent in the actual
	// state is always PKL rendering noise — a user clearing a field would
	// produce a "replace" (field exists in actual), not an "add".
	patchOps = filterSpuriousEmptyAdds(patchOps)

	// Strip empty collections from inside all patch operation values. This
	// cleans up phantom []/{}  values inside nested objects (e.g., empty
	// ResponseParameters inside an IntegrationResponse). Without this,
	// EntitySet array elements may not match their actual counterparts and
	// produce "array items are not unique" errors.
	patchOps = stripEmptyCollectionsFromOps(patchOps)

	// Drop serialization-only ops on Format-hinted fields before the createOnly
	// split, so a cosmetic diff on a (possibly createOnly) hinted field neither
	// reaches the plugin nor triggers a replacement.
	patchOps = dropCanonicallyEqualHintedOps(patchOps, flattenedDocument, flattenedPatch, schema)

	if len(patchOps) == 0 {
		return nil, nil, false, nil
	}

	// A requiredOnUpdate field is force-resent by stripping it from the document
	// side before the diff (see the documentForceResent step in
	// createPatchDocument), so its own "add" op reappears even when nothing
	// changed. That reappearance must never itself decide that an update is
	// sent — patch emptiness is never a decision input. onlyForceResent reports
	// whether every remaining op is such a no-op restatement of a field's own
	// stored value, so a caller PLANNING whether to send an update at all can
	// treat this the same as an empty patch. It must NOT be used to strip
	// content here: a caller regenerating the payload for an update that is
	// already happening still needs the force-resent op in what this function
	// returns, because the provider requires the field in every update payload
	// it actually receives.
	onlyForceResent := onlyForceResentNoops(patchOps, flattenedDocument, requiredOnUpdateFields)

	// Separate createOnly operations from mutable operations. CreateOnly
	// fields cannot be updated in-place via the cloud API — if they changed,
	// the resource needs a full replacement (destroy + create). The createOnly
	// ops are returned separately so the CLI can render which immutable
	// properties triggered the replacement; they are not sent to the plugin.
	createOnlyFields := schema.CreateOnly()
	createOnlyOps := extractCreateOnlyFields(patchOps, createOnlyFields)
	mutableOps := filterCreateOnlyFields(patchOps, createOnlyFields)

	// Member-level ops on an UNKEYED collection (whole-element remove/add
	// pairs from set-based comparison) deliberately stay mutable, even when
	// the element carries a createOnly-hinted subfield: without member
	// identity, a changed immutable value is byte-identical to one member
	// leaving and another arriving, and the provider-appropriate remedy for
	// such collections is member replacement (e.g. detach/re-attach), not a
	// whole-resource replacement. A collection whose members ARE identity-
	// bearing declares an EntitySet indexField; its diff pairs members and
	// emits subfield-level ops, which the index-transparent matching above
	// classifies as createOnly and escalates correctly.
	if len(mutableOps) == 0 && len(createOnlyOps) == 0 {
		return nil, nil, false, nil
	}

	patchJson, err := json.Marshal(mutableOps)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to serialize patch document: %w", err)
	}

	var createOnlyJson json.RawMessage
	if len(createOnlyOps) > 0 {
		createOnlyBytes, err := json.Marshal(createOnlyOps)
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to serialize createOnly patch: %w", err)
		}
		createOnlyJson = json.RawMessage(createOnlyBytes)
	}

	return json.RawMessage(patchJson), createOnlyJson, onlyForceResent, nil
}

func createPatchDocument(document []byte, patch []byte, schemaFields []string, requiredOnUpdateFields []string, hasProviderDefaultFields []string, entitySetProviderDefaults map[string]string, collections jsonpatch.Collections, ignoredFields []jsonpatch.Path, strategy jsonpatch.PatchStrategy, convergeFields map[string]bool) ([]jsonpatch.JsonPatchOperation, error) {
	patchWithSchemaFieldsOnly, err := removeNonSchemaFields(patch, schemaFields, convergeFields)
	if err != nil {
		return nil, err
	}

	// Force-resend requiredOnUpdate fields by stripping them from the document
	// (existing state) before the diff. These are fields the provider mandates
	// in every update payload (e.g. passwords) even when unchanged. Formae
	// stores them, so removing them from the document makes jsonpatch emit an
	// "add" op, guaranteeing they're in the patch sent to the provider.
	//
	// This is keyed off requiredOnUpdate, NOT writeOnly: a field can be writeOnly
	// (excluded from drift detection because Read never returns it) without
	// being requiredOnUpdate, in which case an unchanged value must produce no
	// op rather than a phantom re-send.
	documentForceResent, err := removeWriteOnlyFields(document, requiredOnUpdateFields)
	if err != nil {
		return nil, err
	}

	// Remove provider default fields. For top-level paths we only strip from the
	// document when the field is absent from the patch (preserves user
	// overrides). For paths that traverse a list — e.g. `ContainerDefinitions.Cpu`
	// — we strip the leaf key from BOTH sides in every array element, because
	// jsonpatch's default set-based array comparison cannot reliably pair a
	// document element that carries the provider-populated value with a patch
	// element that omits it. Symmetric stripping makes those sub-fields
	// invisible to the diff regardless of their value, which is the behavior we
	// want for a hasProviderDefault annotation on a sub-field of a list
	// element. See removeProviderDefaultFields for details.
	patchWithSchemaFieldsOnly, documentWithoutProviderDefaults, err := removeProviderDefaultFieldsBoth(documentForceResent, patchWithSchemaFieldsOnly, hasProviderDefaultFields)
	if err != nil {
		return nil, err
	}

	// For EntitySet fields with provider defaults, filter elements from the document (actual state)
	// whose key doesn't appear in the desired state. Cloud providers like AWS populate EntitySet
	// collections with many default elements (e.g., ~22 LoadBalancer attributes). Including all of
	// them in the patch can exceed API limits. By stripping unmatched elements before comparison,
	// only user-specified elements are included in the patch.
	documentFiltered, err := removeProviderDefaultEntitySetElements(documentWithoutProviderDefaults, patchWithSchemaFieldsOnly, entitySetProviderDefaults)
	if err != nil {
		return nil, err
	}

	// Strip empty arrays and maps from inside nested objects in both the
	// desired state and actual state. The PKL schema renders unset
	// nullable Listing/Mapping fields as []/{}. Without stripping, EntitySet
	// element matching fails because elements have different shapes (one has
	// phantom empty fields, the other doesn't), causing duplicate entries.
	cleanedDesired, err := StripNestedEmptyCollections(patchWithSchemaFieldsOnly)
	if err != nil {
		return nil, err
	}

	// Suppress spurious top-level remove ops where the provider Read returns
	// an empty collection for a field that PKL renders as absent. Runs on the
	// pre-StripNestedEmptyCollections document so we only suppress true []/{}
	// actuals (not structures that became empty under nested stripping).
	documentMinusTopEmpties, err := stripTopLevelEmptyCollectionsAbsentInPatch(documentFiltered, cleanedDesired)
	if err != nil {
		return nil, err
	}

	cleanedDocument, err := StripNestedEmptyCollections(documentMinusTopEmpties)
	if err != nil {
		return nil, err
	}

	// Create the actual patch document
	patchDoc, err := jsonpatch.CreatePatch(cleanedDocument, cleanedDesired, collections, ignoredFields, strategy)
	if err != nil {
		return nil, fmt.Errorf("failed to create JSON patch: %w", err)
	}

	return patchDoc, nil
}

// intersectFields returns fields present in both slices.
func intersectFields(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, f := range b {
		set[f] = struct{}{}
	}
	var result []string
	for _, f := range a {
		if _, ok := set[f]; ok {
			result = append(result, f)
		}
	}
	return result
}

// removeWriteOnlyFields removes writeOnly fields from the document.
// WriteOnly field paths can be nested (e.g., "LoginProfile.Password").
// StripFieldsWithoutBaseline removes from desired every field that is both
// writeOnly AND createOnly in the schema and for which storedDocument holds no
// baseline value. The provider's Read never returns writeOnly fields, so a
// document sourced from a Read alone (import, discovery) lacks them; keeping
// the declared value would land an "add" op on a createOnly path and trigger a
// resource replacement even though nothing changed. When the document does
// hold a last-applied value, the ordinary comparison is the truth: an
// unchanged value converges to no op, and a changed value is a genuine
// createOnly change that must plan a replacement rather than be dropped.
//
// Exported because the effective-desired computation must apply the SAME strip
// as patch generation: if the two decide differently, reference consumers are
// planned against values the producer's own patch never writes.
func StripFieldsWithoutBaseline(desired, storedDocument []byte, schema pkgmodel.Schema) ([]byte, error) {
	writeOnlyCreateOnly := intersectFields(schema.WriteOnly(), schema.CreateOnly())
	if len(writeOnlyCreateOnly) == 0 {
		return desired, nil
	}
	var withoutBaseline []string
	for _, field := range writeOnlyCreateOnly {
		if !hasStoredBaseline(storedDocument, field) {
			withoutBaseline = append(withoutBaseline, field)
		}
	}
	return removeWriteOnlyFields(desired, withoutBaseline)
}

// hasStoredBaseline reports whether the dot-separated fieldPath resolves to at
// least one value in document, traversing arrays the same way removeNestedField
// does: at an array, the remaining path is probed in every map element, and any
// hit counts. The predicate decides per FIELD, not per element — one member
// holding a baseline keeps the whole field, and mixed-baseline members are left
// to ordinary member comparison.
func hasStoredBaseline(document []byte, fieldPath string) bool {
	var deserialized map[string]any
	if err := json.Unmarshal(document, &deserialized); err != nil {
		return false
	}
	return nestedFieldExists(deserialized, strings.Split(fieldPath, "."))
}

// nestedFieldExists is hasStoredBaseline's traversal: the read-side mirror of
// removeNestedField.
func nestedFieldExists(obj map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}
	val, exists := obj[path[0]]
	if !exists {
		return false
	}
	if len(path) == 1 {
		return true
	}
	if nested, ok := val.(map[string]any); ok {
		return nestedFieldExists(nested, path[1:])
	}
	if arr, ok := val.([]any); ok {
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]any); ok {
				if nestedFieldExists(elemMap, path[1:]) {
					return true
				}
			}
		}
	}
	return false
}

func removeWriteOnlyFields(document []byte, writeOnlyFields []string) ([]byte, error) {
	if len(writeOnlyFields) == 0 {
		return document, nil
	}

	var deserialized map[string]any
	if err := json.Unmarshal(document, &deserialized); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	for _, fieldPath := range writeOnlyFields {
		removeNestedField(deserialized, strings.Split(fieldPath, "."))
	}

	serialized, err := json.Marshal(deserialized)
	if err != nil {
		return nil, err
	}

	return serialized, nil
}

// removeNestedField removes a field at the given path from a nested map structure.
// For example, path ["LoginProfile", "Password"] removes the Password key from LoginProfile.
// Handles array traversal: if a path segment resolves to an array, the remaining
// path is applied to every map element in that array.
func removeNestedField(obj map[string]any, path []string) {
	if len(path) == 0 {
		return
	}

	if len(path) == 1 {
		delete(obj, path[0])
		return
	}

	val, exists := obj[path[0]]
	if !exists {
		return
	}

	// Navigate to the nested object
	if nested, ok := val.(map[string]any); ok {
		removeNestedField(nested, path[1:])
		return
	}

	// Handle arrays: apply remaining path to each map element
	if arr, ok := val.([]any); ok {
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]any); ok {
				removeNestedField(elemMap, path[1:])
			}
		}
	}
}

// removeProviderDefaultFields removes fields with provider defaults from the
// document (actual state) — and, for fields nested inside array elements,
// symmetrically from the patch (desired state) too.
//
// Two regimes are at play:
//
//  1. Pure-object paths (e.g. "BucketEncryption" or "Config.Encryption"):
//     the field is removed from the document only when it is absent from the
//     patch. This preserves a user's explicit override of the provider
//     default — their desired value remains in the patch and diffs normally.
//
//  2. Array-traversing paths (e.g. "ContainerDefinitions.Cpu" or
//     "ContainerDefinitions.PortMappings.HostPort"): the leaf key is stripped
//     from BOTH sides, in every reachable array element. This is necessary
//     because jsonpatch compares array elements as opaque JSON blobs under
//     its default set semantics, so a document element that carries the
//     provider-populated value (e.g. Cpu:0) won't match a patch element that
//     omits it — even though the user-intended shape is identical. The mixed
//     case (one element sets the field, the other doesn't) cannot be fixed
//     by stripping the document alone, because set-comparison has no stable
//     pairing between elements. Symmetric stripping makes the provider-
//     populated sub-field invisible to the diff regardless of value, which
//     is the correct semantic for a hasProviderDefault annotation inside a
//     collection of heterogeneous sub-resources.
func removeProviderDefaultFields(document []byte, patch []byte, hasProviderDefaultFields []string) ([]byte, error) {
	_, stripped, err := removeProviderDefaultFieldsBoth(document, patch, hasProviderDefaultFields)
	return stripped, err
}

// removeProviderDefaultFieldsBoth is the two-sided counterpart used by the
// patch pipeline: it returns the stripped patch as well as the stripped
// document so that array-nested provider defaults are removed symmetrically.
// Callers that only need the document side can use removeProviderDefaultFields.
func removeProviderDefaultFieldsBoth(document []byte, patch []byte, hasProviderDefaultFields []string) ([]byte, []byte, error) {
	if len(hasProviderDefaultFields) == 0 {
		return patch, document, nil
	}

	var docMap map[string]any
	if err := json.Unmarshal(document, &docMap); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	var patchMap map[string]any
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal patch: %w", err)
	}

	for _, fieldPath := range hasProviderDefaultFields {
		pathParts := strings.Split(fieldPath, ".")
		stripProviderDefaultPath(docMap, patchMap, pathParts)
	}

	patchSerialized, err := json.Marshal(patchMap)
	if err != nil {
		return nil, nil, err
	}
	docSerialized, err := json.Marshal(docMap)
	if err != nil {
		return nil, nil, err
	}

	return patchSerialized, docSerialized, nil
}

// stripProviderDefaultPath walks a dotted field path through parallel document
// and patch maps. Whenever the walk descends through an array, it iterates the
// array on BOTH sides and applies the remaining path to every element,
// dropping the leaf key symmetrically (see the comment on
// removeProviderDefaultFields for the rationale). For walks that never enter
// an array, it falls back to the original conditional behavior: the leaf is
// stripped from the document only when it is absent in the patch.
func stripProviderDefaultPath(doc, patch map[string]any, path []string) {
	if len(path) == 0 || doc == nil {
		return
	}

	// Last segment — conditional strip on document only, to preserve user overrides.
	// fieldExistsInMap treats a nil patch value as absent: the reverted PKL
	// renderer emits unset nullable Listing/Mapping fields as null. In that case
	// we also drop the leaf from the patch so the diff doesn't see a spurious
	// `add /<field>: null`. An explicit empty Listing {} / Mapping {} renders as
	// []/{}, stays in the patch, and continues to mean "user-initiated clear".
	if len(path) == 1 {
		if !fieldExistsInMap(patch, path) {
			delete(doc, path[0])
			delete(patch, path[0])
		}
		return
	}

	head, tail := path[0], path[1:]

	docVal, docHas := doc[head]
	patchVal := any(nil)
	if patch != nil {
		patchVal = patch[head]
	}

	// Array on either side: walk into each element symmetrically.
	if docArr, ok := docVal.([]any); ok {
		patchArr, _ := patchVal.([]any)
		stripProviderDefaultInsideArray(docArr, patchArr, tail)
		return
	}
	if patchArr, ok := patchVal.([]any); ok {
		// Document doesn't have this key (or has it as a non-array).
		// Still strip from every patch element to keep both sides symmetric.
		stripProviderDefaultInsideArray(nil, patchArr, tail)
		return
	}

	// Pure object traversal — recurse.
	if !docHas {
		return
	}
	docNested, ok := docVal.(map[string]any)
	if !ok {
		return
	}
	var patchNested map[string]any
	if p, ok := patchVal.(map[string]any); ok {
		patchNested = p
	}
	stripProviderDefaultPath(docNested, patchNested, tail)
}

// stripProviderDefaultInsideArray walks the remaining path into each element
// of the doc and patch arrays in parallel (by position where available, else
// independently) and removes the leaf key from BOTH sides in every reachable
// element. Elements that aren't objects (or don't match the expected shape)
// are left alone.
func stripProviderDefaultInsideArray(docArr, patchArr []any, path []string) {
	if len(path) == 0 {
		return
	}

	for _, elem := range docArr {
		if elemMap, ok := elem.(map[string]any); ok {
			stripProviderDefaultInArrayElem(elemMap, path)
		}
	}
	for _, elem := range patchArr {
		if elemMap, ok := elem.(map[string]any); ok {
			stripProviderDefaultInArrayElem(elemMap, path)
		}
	}
}

// stripProviderDefaultInArrayElem handles the remaining path INSIDE an array
// element. Any further array traversal recurses via
// stripProviderDefaultInsideArray; object traversal continues into the
// nested map; the leaf key is deleted unconditionally, because once we are
// inside an array element the provider-populated value cannot be reliably
// matched to a counterpart on the other side (set semantics).
func stripProviderDefaultInArrayElem(elem map[string]any, path []string) {
	if len(path) == 0 || elem == nil {
		return
	}
	if len(path) == 1 {
		delete(elem, path[0])
		return
	}

	head, tail := path[0], path[1:]
	val, has := elem[head]
	if !has {
		return
	}
	switch v := val.(type) {
	case map[string]any:
		stripProviderDefaultInArrayElem(v, tail)
	case []any:
		stripProviderDefaultInsideArray(v, nil, tail)
	}
}

// removeProviderDefaultEntitySetElements filters EntitySet arrays in the document (actual state)
// to only retain elements whose key (by indexField) matches a key in the patch (desired state).
// This handles cloud providers that populate EntitySet collections with many default elements
// (e.g., AWS LoadBalancer returns ~22 default attributes). Without filtering, all defaults would
// be included in the patch, potentially exceeding API limits.
func removeProviderDefaultEntitySetElements(document []byte, patch []byte, entitySetProviderDefaults map[string]string) ([]byte, error) {
	if len(entitySetProviderDefaults) == 0 {
		return document, nil
	}

	var docMap map[string]any
	if err := json.Unmarshal(document, &docMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal document: %w", err)
	}

	var patchMap map[string]any
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patch: %w", err)
	}

	for field, indexKey := range entitySetProviderDefaults {
		docArr, ok := docMap[field].([]any)
		if !ok {
			continue
		}

		patchArr, ok := patchMap[field].([]any)
		if !ok {
			// Desired state doesn't have this field at all — remove entire array from document
			delete(docMap, field)
			continue
		}

		// Build a set of keys present in desired state
		desiredKeys := map[string]struct{}{}
		for _, elem := range patchArr {
			if elemMap, ok := elem.(map[string]any); ok {
				if keyVal, ok := elemMap[indexKey]; ok {
					desiredKeys[fmt.Sprintf("%v", keyVal)] = struct{}{}
				}
			}
		}

		// Filter document array to only keep elements with matching keys
		filtered := make([]any, 0, len(patchArr))
		for _, elem := range docArr {
			if elemMap, ok := elem.(map[string]any); ok {
				if keyVal, ok := elemMap[indexKey]; ok {
					if _, exists := desiredKeys[fmt.Sprintf("%v", keyVal)]; exists {
						filtered = append(filtered, elem)
					}
					continue
				}
			}
			// Keep elements we can't match on (no key field)
			filtered = append(filtered, elem)
		}
		docMap[field] = filtered
	}

	serialized, err := json.Marshal(docMap)
	if err != nil {
		return nil, err
	}

	return serialized, nil
}

// fieldExistsInMap checks if a field at the given path exists in a nested map structure.
// For example, path ["BucketEncryption", "Rules"] checks if obj["BucketEncryption"]["Rules"] exists.
// Handles array traversal: if a path segment resolves to an array, checks whether
// the remaining path exists in any map element of that array.
//
// A nil value is treated as absent: the reverted PKL renderer emits unset nullable
// Listing/Mapping fields as null, while explicit empty Listing {} / Mapping {}
// renders as []/{}. removeProviderDefaultFields uses this distinction to suppress
// drift only when the user omitted the field, not when they explicitly cleared it.
func fieldExistsInMap(obj map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}

	val, exists := obj[path[0]]
	if !exists || val == nil {
		return false
	}

	if len(path) == 1 {
		return true
	}

	// Navigate to the nested object
	if nested, ok := val.(map[string]any); ok {
		return fieldExistsInMap(nested, path[1:])
	}

	// Handle arrays: check if remaining path exists in any map element
	if arr, ok := val.([]any); ok {
		for _, elem := range arr {
			if elemMap, ok := elem.(map[string]any); ok {
				if fieldExistsInMap(elemMap, path[1:]) {
					return true
				}
			}
		}
	}

	return false
}

// removeNonSchemaFields keeps only schema fields that carry a value. The
// empty-string drop exists because PKL renders an unset nullable String field
// as "": an unresolved reference occurrence flattens to the same "", so a
// field in convergeFields (classified as requiring a converging update) is
// kept regardless of value — for it, the "" is a resolution placeholder, not
// rendering noise.
func removeNonSchemaFields(patch []byte, schemaFields []string, convergeFields map[string]bool) ([]byte, error) {
	var deserialized map[string]any
	if err := json.Unmarshal(patch, &deserialized); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resource properties: %w", err)
	}
	modified := make(map[string]any)
	for _, field := range schemaFields {
		if val, ok := deserialized[field]; ok && (hasValue(val) || convergeFields[field]) {
			modified[field] = val
		}
	}
	serialized, err := json.Marshal(modified)
	if err != nil {
		return nil, err
	}

	return serialized, err
}

// StripNestedEmptyCollections recursively removes empty arrays and maps from
// inside nested objects in a JSON document. Top-level empty collections are
// preserved (they may represent intentional "clear" operations).
// This is used both in the patch pipeline (before diff comparison) and in the
// resource updater (before sending Properties to plugins for Create/Update)
// to clean PKL rendering artifacts (null → []/{}  from nullable Listing/Mapping fields).
func StripNestedEmptyCollections(data []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("StripNestedEmptyCollections: invalid JSON: %w", err)
	}

	for k, v := range doc {
		doc[k] = stripEmptyCollectionsFromValue(v)
	}

	return json.Marshal(doc)
}

// filterSpuriousEmptyAdds removes "add" operations with empty array or map
// values. The PKL schema renders unset nullable Listing/Mapping fields
// as []/{}. An "add" means the field is absent in the actual state, so adding
// an empty collection is never user intent — it's PKL rendering noise. A user
// clearing an existing field produces a "replace" (field exists), not "add".
func filterSpuriousEmptyAdds(patchOps []jsonpatch.JsonPatchOperation) []jsonpatch.JsonPatchOperation {
	filtered := make([]jsonpatch.JsonPatchOperation, 0, len(patchOps))
	for _, op := range patchOps {
		if op.Operation == "add" && isEmptyCollection(op.Value) {
			continue
		}
		filtered = append(filtered, op)
	}
	return filtered
}

// isTopLevelPath reports whether a JSON Pointer addresses a single top-level
// field (e.g. "/configJson"), not a nested or array-index path.
func isTopLevelPath(p string) bool {
	trimmed := strings.TrimPrefix(p, "/")
	return trimmed != "" && !strings.Contains(trimmed, "/")
}

// dropCanonicallyEqualHintedOps removes patch ops targeting a top-level
// Format-hinted field whose old/new values are canonically equal (a
// serialization-only diff). On any canonicalizer error, a non-string value, or a
// nested/array path, the op is KEPT — suppression can only ever drop a cosmetic
// op, never a real change.
func dropCanonicallyEqualHintedOps(ops []jsonpatch.JsonPatchOperation, document, patch []byte, schema pkgmodel.Schema) []jsonpatch.JsonPatchOperation {
	formats := schema.FormatHints()
	if len(formats) == 0 {
		return ops
	}
	kept := make([]jsonpatch.JsonPatchOperation, 0, len(ops))
	for _, op := range ops {
		field := cleanPath(op.Path)
		format, hinted := formats[field]
		if hinted && isTopLevelPath(op.Path) {
			// The hint key is used as a gjson path (dot = nesting). v1 targets
			// top-level fields whose names contain no gjson-special chars
			// (`.`/`*`/`?`); the grafana `configJson` consumer satisfies this. A
			// top-level field literally named with a `.` would not be matched
			// (canonicalization silently skipped — safe-direction, never drops a
			// real change).
			oldVal := gjson.GetBytes(document, field)
			newVal := gjson.GetBytes(patch, field)
			if oldVal.Type == gjson.String && newVal.Type == gjson.String {
				oc, oerr := canonicalize.Canonicalize(format, oldVal.String())
				nc, nerr := canonicalize.Canonicalize(format, newVal.String())
				if oerr == nil && nerr == nil && oc == nc {
					continue // serialization-only diff → drop
				}
			}
		}
		kept = append(kept, op)
	}
	return kept
}

// stripEmptyCollectionsFromOps recursively removes empty arrays and maps from
// inside all patch operation values. This ensures that EntitySet element
// matching works correctly when elements contain phantom []/{}  values.
func stripEmptyCollectionsFromOps(patchOps []jsonpatch.JsonPatchOperation) []jsonpatch.JsonPatchOperation {
	for i := range patchOps {
		patchOps[i].Value = stripEmptyCollectionsFromValue(patchOps[i].Value)
	}
	return patchOps
}

func stripEmptyCollectionsFromValue(val any) any {
	switch v := val.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for k, elem := range v {
			if isEmptyCollection(elem) {
				continue
			}
			stripped := stripEmptyCollectionsFromValue(elem)
			// Re-check after recursive stripping — a map whose children
			// were all empty collections is itself now empty and should
			// be removed (e.g. DestinationConfig: {OnSuccess: {}, OnFailure: {}}).
			if isEmptyCollection(stripped) {
				continue
			}
			cleaned[k] = stripped
		}
		return cleaned
	case []any:
		cleaned := make([]any, 0, len(v))
		for _, elem := range v {
			cleaned = append(cleaned, stripEmptyCollectionsFromValue(elem))
		}
		return cleaned
	default:
		return val
	}
}

func isEmptyCollection(val any) bool {
	switch v := val.(type) {
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

// stripTopLevelEmptyCollectionsAbsentInPatch removes top-level keys from the
// document whose value is an empty array `[]` or empty object `{}` and whose
// key is absent from the patch. This suppresses spurious `op:remove` patch ops
// that the JSON-Patch comparator would otherwise emit when PKL renders a
// Property as absent (no key) and the provider's Read returns the field as
// an empty collection (e.g. AWS::ECS::TaskDefinition.Tags returns `[]` even
// when not set on Create).
//
// Mirror of filterSpuriousEmptyAdds on the input side: that one drops "add"
// ops with empty values; this one prevents the "remove" ops from being
// generated in the first place. Both are corrections for the PKL-render vs
// provider-Read asymmetry around empty collections.
//
// IMPORTANT: This helper must run BEFORE StripNestedEmptyCollections on the
// document side. Otherwise a top-level object whose contents were all
// recursively stripped to empty (e.g. {Outer: {Inner: []}} → {Outer: {}})
// would falsely match the empty-collection predicate and silently mask
// legitimate drift on a structurally non-trivial field.
func stripTopLevelEmptyCollectionsAbsentInPatch(document, patch []byte) ([]byte, error) {
	var docMap map[string]any
	if err := json.Unmarshal(document, &docMap); err != nil {
		return nil, fmt.Errorf("stripTopLevelEmptyCollectionsAbsentInPatch: invalid document: %w", err)
	}

	var patchMap map[string]any
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return nil, fmt.Errorf("stripTopLevelEmptyCollectionsAbsentInPatch: invalid patch: %w", err)
	}

	for k, v := range docMap {
		if _, presentInPatch := patchMap[k]; presentInPatch {
			continue
		}
		if isEmptyCollection(v) {
			delete(docMap, k)
		}
	}

	return json.Marshal(docMap)
}

// filterCreateOnlyFields removes patch operations that target createOnly fields.
// These operations cannot be sent to the cloud API — createOnly fields are
// immutable after creation. If they changed, the caller uses needsReplacement
// to trigger a full destroy+create cycle instead.
func filterCreateOnlyFields(patchOps []jsonpatch.JsonPatchOperation, createOnlyFields []string) []jsonpatch.JsonPatchOperation {
	if len(createOnlyFields) == 0 {
		return patchOps
	}
	filtered := make([]jsonpatch.JsonPatchOperation, 0, len(patchOps))
	for _, op := range patchOps {
		path := cleanPath(op.Path)
		if !isCreateOnlyPath(path, createOnlyFields) {
			filtered = append(filtered, op)
		}
	}
	return filtered
}

// extractCreateOnlyFields returns the subset of patch operations that target
// createOnly fields — the inverse of filterCreateOnlyFields. Used to preserve
// the triggering ops for CLI rendering when a replacement is required.
func extractCreateOnlyFields(patchOps []jsonpatch.JsonPatchOperation, createOnlyFields []string) []jsonpatch.JsonPatchOperation {
	if len(createOnlyFields) == 0 {
		return nil
	}
	var extracted []jsonpatch.JsonPatchOperation
	for _, op := range patchOps {
		path := cleanPath(op.Path)
		if isCreateOnlyPath(path, createOnlyFields) {
			extracted = append(extracted, op)
		}
	}
	return extracted
}

// isCreateOnlyPath checks if a patch path targets a createOnly field.
// Matches both the field itself ("/DomainName") and nested paths within
// it ("/ContainerDefinitions/0/Name").
//
// Schema Hints from `formae.fq.hints()` use dot-separated keys for
// nested fields ("Spec.Selector"), but jsonpatch operation paths use
// slash separators per RFC 6902 ("/Spec/Selector/MatchLabels/foo").
// Normalize the schema-side keys to slash form before comparison so
// nested createOnly fields on SubResources are detected. Without the
// normalization the check silently no-ops for any path deeper than a
// top-level field — which leaves createOnly violations on nested
// fields undetected until the cloud API rejects them at apply time.
// Matching is array-index-transparent for the same reason as the
// requiredOnUpdate matcher: a hint on a field inside an array element
// ("Items.Token") must catch the op at its indexed path ("/Items/0/Token").
func isCreateOnlyPath(path string, createOnlyFields []string) bool {
	return pathMatchesFieldThroughArrays(path, createOnlyFields)
}

// onlyForceResentNoops reports whether every op in ops is a force-resent
// no-op: an "add" on a requiredOnUpdate path whose value merely restates what
// originalDocument already held there before force-resend stripped it out (see
// createPatchDocument). An empty ops slice trivially satisfies this, matching
// the emptiness this check replaces.
func onlyForceResentNoops(ops []jsonpatch.JsonPatchOperation, originalDocument []byte, requiredOnUpdateFields []string) bool {
	for _, op := range ops {
		if !isForceResentNoop(op, originalDocument, requiredOnUpdateFields) {
			return false
		}
	}
	return true
}

// isForceResentNoop reports whether op is an "add" operation on a
// requiredOnUpdate path whose value equals what originalDocument already held
// at that path — i.e. the op exists solely because createPatchDocument strips
// requiredOnUpdate fields from the document side to guarantee they ride along
// in a genuine update, not because the field's value actually changed.
func isForceResentNoop(op jsonpatch.JsonPatchOperation, originalDocument []byte, requiredOnUpdateFields []string) bool {
	if op.Operation != "add" || len(requiredOnUpdateFields) == 0 {
		return false
	}
	path := cleanPath(op.Path)
	if !pathMatchesFieldThroughArrays(path, requiredOnUpdateFields) {
		return false
	}
	gjsonPath := strings.ReplaceAll(path, "/", ".")
	original := gjson.GetBytes(originalDocument, gjsonPath)
	if !original.Exists() {
		return false
	}
	return reflect.DeepEqual(original.Value(), op.Value)
}

// pathMatchesFieldThroughArrays is pathMatchesField's array-index-aware
// counterpart, used only for matching a force-resent op's path against
// requiredOnUpdate fields. A requiredOnUpdate hint on a field nested inside an
// array (e.g. "Items.Token") is stripped from every array element by
// removeNestedField, so its "add" op lands at an array-indexed path (e.g.
// "/Items/0/Token") that plain segment-by-segment comparison against
// "Items/Token" would never match. Mirrors removeNestedField's own traversal:
// a numeric path segment is transparent (an array index, not a field name)
// and is skipped rather than consumed against the field's segments.
func pathMatchesFieldThroughArrays(path string, fields []string) bool {
	pathSegments := strings.Split(path, "/")
	for _, field := range fields {
		if matchesFieldThroughArrays(pathSegments, strings.Split(field, ".")) {
			return true
		}
	}
	return false
}

// matchesFieldThroughArrays reports whether pathSegments targets fieldSegments
// (or a nested path within it), skipping any pathSegments entry that is a
// numeric array index rather than consuming it against a field segment.
func matchesFieldThroughArrays(pathSegments, fieldSegments []string) bool {
	fi := 0
	for _, segment := range pathSegments {
		if isArrayIndexSegment(segment) {
			continue
		}
		if fi >= len(fieldSegments) {
			// Every field segment already matched; what remains is a nested
			// path within the field, which still counts as a match.
			return true
		}
		if segment != fieldSegments[fi] {
			return false
		}
		fi++
	}
	return fi == len(fieldSegments)
}

// isArrayIndexSegment reports whether a JSON Pointer path segment is a
// non-negative integer (an array index per RFC 6901), the same shape
// removeNestedField's array traversal produces. A schema field literally
// named with all-digit keys is indistinguishable from an index here — the
// same ambiguity the resolver's own dotted-path convention already accepts.
func isArrayIndexSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasValue(val any) bool {
	v, ok := val.(string)
	return !ok || len(v) > 0
}

func cleanPath(path string) string {
	if len(path) > 0 && path[0] == '/' {
		return path[1:]
	}

	return path
}

// normalizeResolvedValue reconciles the representation of a resolved value with
// the JSON kind of the corresponding current (live) value at the same path.
//
// ResolvableProperties stores every value as a string (resolvable_properties.go),
// so a list- or object-valued resolvable arrives here as raw JSON text (e.g.
// `["host"]`), while the live side — already unwrapped to its native shape by
// resolver.ConvertToPluginFormat — is a []any/map[string]any. Left as a string
// the desired side would diff against the native value on every reconcile,
// producing a perpetual no-op update. Scalars never reach this path with that
// mismatch because their cached $value is unwrapped natively on both sides.
//
// The normalization is shape-driven, not syntax-driven: it only parses the
// resolved string when the current value is itself a native array or object and
// the string is valid JSON of that same kind. A String field whose legitimate
// value is JSON text (e.g. an IAM policy document) reads back as a string on the
// current side, so it is left untouched — a blind parse would regress it into
// perpetual drift. Aligned with resolver.extractResolvedValue's structured
// detection so the two resolution paths agree.
func normalizeResolvedValue(resolved string, current any) any {
	switch current.(type) {
	case []any:
		var parsed []any
		// parsed stays nil for the literal string "null", which unmarshals
		// without error; keep such a value a string rather than rewriting it
		// to JSON null.
		if json.Unmarshal([]byte(resolved), &parsed) == nil && parsed != nil {
			return parsed
		}
	case map[string]any:
		var parsed map[string]any
		if json.Unmarshal([]byte(resolved), &parsed) == nil && parsed != nil {
			return parsed
		}
	}
	return resolved
}

// storedRefCounterpart returns the stored envelope that corresponds to a
// desired-side reference node: a map carrying the same $ref URI, not marked
// Opaque on either side. Opaque envelopes are handled by the dedicated
// opaque-suppression path and never participate in provenance comparison.
func storedRefCounterpart(storedNode any, modVal map[string]any) map[string]any {
	storedMap, ok := storedNode.(map[string]any)
	if !ok {
		return nil
	}
	if storedMap["$ref"] != modVal["$ref"] {
		return nil
	}
	if storedMap["$visibility"] == pkgmodel.VisibilityOpaque || modVal["$visibility"] == pkgmodel.VisibilityOpaque {
		return nil
	}
	return storedMap
}

// appliedMatches reports whether a fresh reference resolution equals the
// value the last formae-originated write sent. The fresh value arrives as a
// string (ResolvableProperties stores raw JSON text for structured values);
// $applied holds the JSON-native form that was sent (native JSON scalars,
// arrays, or objects). Comparison parses the string into the same JSON shape,
// handling numeric, boolean, and structured values uniformly.
func appliedMatches(fresh string, applied any) bool {
	if s, ok := applied.(string); ok {
		return fresh == s
	}
	var parsed any
	if err := json.Unmarshal([]byte(fresh), &parsed); err != nil {
		return false
	}
	return reflect.DeepEqual(parsed, applied)
}

// storedAppliedEnvelope returns the stored node as a provenance-carrying
// envelope: a reference envelope that records what the last write applied and
// still holds the value the provider echoed for it. Opaque envelopes are
// excluded, matching the rest of the provenance rules.
//
// This is the counterpart lookup for a desired side that is no longer
// structured. The executor resolves references before calling a provider and
// re-derives the patch from the resolved properties, so on that path the
// desired value arrives as the bare resolved scalar with no envelope to match
// a reference URI against; the applied baseline is what identifies it.
func storedAppliedEnvelope(storedNode any) map[string]any {
	storedMap, ok := storedNode.(map[string]any)
	if !ok {
		return nil
	}
	if storedMap["$ref"] == nil && storedMap["$res"] == nil {
		return nil
	}
	if storedMap["$visibility"] == pkgmodel.VisibilityOpaque {
		return nil
	}
	if storedMap["$applied"] == nil || storedMap["$value"] == nil {
		return nil
	}
	return storedMap
}

// resolvedDesiredEnvelope returns the desired-side envelope for a value that has
// already been resolved: the converted desired document holds the resolved value
// alone, while the unconverted desired properties still hold the envelope it came
// from. That envelope is what establishes the value IS a resolved reference
// rather than a literal the user wrote in its place, which matching on the value
// alone cannot tell apart.
func resolvedDesiredEnvelope(desiredNode, modNode any) (map[string]any, bool) {
	desiredEnv, ok := desiredNode.(map[string]any)
	if !ok || desiredEnv["$visibility"] == pkgmodel.VisibilityOpaque {
		return nil, false
	}
	if ref, ok := desiredEnv["$ref"].(string); !ok || ref == "" {
		return nil, false
	}
	if value, resolved := desiredEnv["$value"]; !resolved || value == nil {
		return nil, false
	}
	// A desired side that still carries its envelope is handled by the
	// structured path, which resolves it fresh.
	if modEnv, isEnv := modNode.(map[string]any); isEnv {
		if _, hasRef := modEnv["$ref"]; hasRef {
			return nil, false
		}
	}
	return desiredEnv, true
}

// freshResolution returns what a reference resolves to now, both normalized to
// the live value's JSON shape and as the raw resolved text. A cached $value on
// the desired envelope is only a record of an earlier resolution, so whenever a
// current one is available it is the authority: trusting the cached value would
// let a reference that now points somewhere else compare as unchanged and drop
// the update, or miss a replacement on an immutable field.
func freshResolution(env map[string]any, current any, resolvableProperties resolver.ResolvableProperties) (any, string, bool, error) {
	ref, _ := env["$ref"].(string)
	uri := pkgmodel.FormaeURI(ref)
	val, found := resolvableProperties.Get(uri.KSUID(), uri.PropertyPath())
	if !found {
		return nil, "", false, nil
	}
	if jsonPath, ok := env["$json"].(string); ok && jsonPath != "" {
		extracted, err := resolver.ExtractJSONPath(val, jsonPath)
		if err != nil {
			// A source that resolves but cannot be read through its path is a
			// broken reference, not a missing one. Surfacing it matches the
			// structured path; falling back to the recorded value would leave
			// the resource on its old value with nothing reported.
			return nil, "", false, err
		}
		val = extracted
	}
	return normalizeResolvedValue(val, current), val, true, nil
}

// substituteResolvedRef decides what the desired side should carry for a
// reference that has already been resolved. It reports whether it handled the
// node at all.
//
// When the reference still resolves to what the last write applied, the desired
// side takes the stored echo so the comparison stays within the observed domain
// and the reference is not rewritten in the provider's own spelling. Otherwise
// the desired side carries the current resolution, so a genuine repoint is
// planned (and, on an immutable field, still forces a replacement).
func substituteResolvedRef(desiredNode, storedNode, modNode, currentNode any, resolvableProperties resolver.ResolvableProperties) (resolvedRefDecision, bool, error) {
	desiredEnv, ok := resolvedDesiredEnvelope(desiredNode, modNode)
	if !ok {
		return resolvedRefDecision{}, false, nil
	}
	storedEnv := storedAppliedEnvelope(storedNode)
	sameRef := storedEnv != nil && storedEnv["$ref"] == desiredEnv["$ref"]

	effective := desiredEnv["$value"]
	matchesApplied := sameRef && reflect.DeepEqual(storedEnv["$applied"], effective)
	native, raw, found, err := freshResolution(desiredEnv, currentNode, resolvableProperties)
	if err != nil {
		return resolvedRefDecision{}, false, err
	}
	if found {
		effective = native
		matchesApplied = sameRef && appliedMatches(raw, storedEnv["$applied"])
	}
	// The value formae would write is returned either way. When the reference is
	// unchanged the caller aligns the comparison side instead of rewriting this
	// one, so an operation covering this path still carries the written form.
	//
	// For an unchanged reference that value is the record itself, which keeps
	// the JSON type that was written: a resolution arrives as text, so a number
	// or boolean would otherwise be written back as a string.
	if matchesApplied {
		return resolvedRefDecision{Value: storedEnv["$applied"], Echo: storedEnv["$value"], Matched: true}, true, nil
	}
	return resolvedRefDecision{Value: effective, Echo: storedEnv["$value"], Matched: false}, true, nil
}

// resolvedRefDecision carries what the desired side should hold for an
// already-resolved reference, the provider spelling recorded with it, and
// whether it still resolves to what the last write applied.
type resolvedRefDecision struct {
	Value   any
	Echo    any
	Matched bool
}

// desiredEnvRef reports the reference a desired array element names, if any.
func desiredEnvRef(desiredElem any) any {
	env, ok := desiredElem.(map[string]any)
	if !ok {
		return nil
	}
	return env["$ref"]
}

// alignComparisonElementForRef aligns the actual-state element for one array
// reference, when that reference still resolves to what the last write applied.
func alignComparisonElementForRef(currentArr []any, storedElem map[string]any, desiredElem any) {
	if storedElem == nil || storedElem["$applied"] == nil || storedElem["$value"] == nil {
		return
	}
	// An element still carrying its envelope holds the resolution under $value;
	// an already-converted element IS the resolution, which may itself be an
	// object and must not be mistaken for an envelope.
	resolved := desiredElem
	if env, isEnv := desiredElem.(map[string]any); isEnv {
		if _, isEnvelope := env["$ref"]; isEnvelope {
			value, hasValue := env["$value"]
			if !hasValue {
				return
			}
			resolved = value
		}
	}
	if !reflect.DeepEqual(storedElem["$applied"], resolved) {
		return
	}
	alignComparisonElement(currentArr, storedElem["$value"], resolved)
}

// alignComparisonElement rewrites the actual-state array element the provider
// reported for an unchanged reference. The element is found by the echo recorded
// with the reference rather than by position; with no unique match the element is
// left alone and the comparison keeps its previous behavior.
func alignComparisonElement(currentArr []any, echo, value any) {
	if echo == nil {
		return
	}
	match := -1
	for i, elem := range currentArr {
		if !reflect.DeepEqual(elem, echo) {
			continue
		}
		if match >= 0 {
			return
		}
		match = i
	}
	if match >= 0 {
		currentArr[match] = value
	}
}

// alignComparisonValue records, on the actual-state side of the diff, the value
// an unchanged reference resolves to, so the two sides compare equal without
// the desired side being rewritten into the provider's spelling.
//
// It only rewrites a path the provider actually reported: inventing one would
// turn an addition into a no-op.
func alignComparisonValue(current map[string]any, key string, echo, value any) {
	if current == nil {
		return
	}
	reported, exists := current[key]
	if !exists {
		return
	}
	// Only align a value that still matches what the provider echoed when the
	// reference was written. A live value that has moved since is drift, and
	// overwriting it here would compare it away and leave it unrepaired.
	if !reflect.DeepEqual(reported, echo) {
		return
	}
	current[key] = value
}

// substituteResolvedRefInArray applies substituteResolvedRef to one array
// element, locating the stored counterpart by the reference the desired element
// names rather than by position: the provider may return elements in any order.
func substituteResolvedRefInArray(desiredElem any, storedArr []any, modElem, currentElem any, resolvableProperties resolver.ResolvableProperties) (resolvedRefDecision, bool, error) {
	desiredEnv, ok := desiredElem.(map[string]any)
	if !ok {
		return resolvedRefDecision{}, false, nil
	}
	var storedElem any
	if match := storedRefElementByURI(storedArr, desiredEnv["$ref"]); match != nil {
		storedElem = match
	}
	return substituteResolvedRef(desiredElem, storedElem, modElem, currentElem, resolvableProperties)
}

// storedRefElementByURI finds the stored array element whose $ref equals uri.
// Ambiguity (zero or multiple matches) returns nil: with no unique
// counterpart the element gets no provenance treatment.
func storedRefElementByURI(storedArr []any, uri any) map[string]any {
	var match map[string]any
	for _, elem := range storedArr {
		m, ok := elem.(map[string]any)
		if !ok || m["$ref"] != uri {
			continue
		}
		if match != nil {
			return nil
		}
		match = m
	}
	return match
}

// resolveRefs uses properties to resolve references in the patch document
func resolveRefs(current, mod, stored, desired map[string]any, resolvableProperties resolver.ResolvableProperties) error {
	for k, v := range mod {
		// A reference the executor already resolved: the desired side holds the
		// resolved value alone, and the envelope it came from lives in the
		// unconverted desired properties.
		decision, handled, err := substituteResolvedRef(desired[k], stored[k], v, current[k], resolvableProperties)
		if err != nil {
			return err
		}
		if handled {
			mod[k] = decision.Value
			if decision.Matched {
				alignComparisonValue(current, k, decision.Echo, decision.Value)
			}
			continue
		}
		switch modVal := v.(type) {
		case map[string]any:
			if ref, hasRef := modVal["$ref"]; hasRef {
				uri := pkgmodel.FormaeURI(ref.(string))
				ksuid := uri.KSUID()
				property := uri.PropertyPath()

				counterpart := storedRefCounterpart(stored[k], modVal)

				val, found := resolvableProperties.Get(ksuid, property)
				if found {
					resolved := val
					if jsonPath, ok := modVal["$json"].(string); ok && jsonPath != "" {
						extracted, err := resolver.ExtractJSONPath(val, jsonPath)
						if err != nil {
							return err
						}
						resolved = extracted
					}
					native := normalizeResolvedValue(resolved, current[k])
					modVal["$value"] = native
					if counterpart != nil {
						if applied, hasApplied := counterpart["$applied"]; hasApplied && applied != nil && appliedMatches(resolved, applied) && counterpart["$value"] != nil {
							// Carry the recorded value itself rather than the
							// freshly resolved text. Resolution yields text, while
							// the record keeps the JSON type that was written, so
							// a number or boolean would otherwise be written back
							// as a string.
							modVal["$value"] = applied
							// The reference still resolves to what the last write
							// sent, so it is unchanged. Align the comparison side
							// rather than the desired side: the desired side is
							// where operation values come from, and rewriting it
							// to the provider's spelling would send that spelling
							// back inside any operation covering this path.
							alignComparisonValue(current, k, counterpart["$value"], applied)
						}
					}
				} else if counterpart != nil {
					if applied, hasApplied := counterpart["$applied"]; hasApplied && applied != nil && counterpart["$value"] != nil {
						if _, hasVal := modVal["$value"]; !hasVal {
							// No resolution available, but a prior write attests
							// this exact reference, so the gap is transient rather
							// than a change. Carry the value that write sent: the
							// desired side is what operations are built from, and
							// the provider's spelling must never be written back.
							modVal["$value"] = applied
							alignComparisonValue(current, k, counterpart["$value"], applied)
						}
					}
				}
				// Otherwise keep the $ref as-is for late-binding resolution
				// at execution time (forward references to new resources).
			}
			var currNested map[string]any
			if c, ok := current[k].(map[string]any); ok {
				currNested = c
			} else {
				currNested = map[string]any{}
			}
			var storedNested map[string]any
			if s, ok := stored[k].(map[string]any); ok {
				storedNested = s
			} else {
				storedNested = map[string]any{}
			}
			var desiredNested map[string]any
			if d, ok := desired[k].(map[string]any); ok {
				desiredNested = d
			} else {
				desiredNested = map[string]any{}
			}
			if err := resolveRefs(currNested, modVal, storedNested, desiredNested, resolvableProperties); err != nil {
				return err
			}
		case []any:
			var currArr []any
			if c, ok := current[k].([]any); ok {
				currArr = c
			}
			var storedArr []any
			if stored != nil {
				if s, ok := stored[k].([]any); ok {
					storedArr = s
				}
			}
			// The unconverted desired properties are the same document in the
			// same order, so desired elements are matched by index; stored
			// elements are a different document and are matched by reference.
			var desiredArr []any
			if d, ok := desired[k].([]any); ok {
				desiredArr = d
			}
			for i, elem := range modVal {
				var currElem any
				if len(currArr) > i {
					currElem = currArr[i]
				}
				if elemMap, ok := elem.(map[string]any); ok {
					// Preserve the key to resolve references. Wrap the current
					// element under the same key so the recursion's current[k]
					// is the live element at this index — representation
					// normalization needs the live value's JSON kind, and an
					// array-element ref resolving to a list/object would
					// otherwise normalize against a nil current and diff forever.
					wrappedElem := map[string]any{k: elemMap}
					wrappedCurrent := map[string]any{k: currElem}
					var wrappedStored map[string]any

					// For ref-carrying elements, match the stored counterpart by URI;
					// otherwise thread by index.
					if ref, hasRef := elemMap["$ref"]; hasRef && len(storedArr) > 0 {
						storedElem := storedRefElementByURI(storedArr, ref)
						if storedElem != nil {
							wrappedStored = map[string]any{k: storedElem}
						}
					} else if len(storedArr) > i {
						storedElem := storedArr[i]
						wrappedStored = map[string]any{k: storedElem}
					}
					var wrappedDesired map[string]any
					if len(desiredArr) > i {
						wrappedDesired = map[string]any{k: desiredArr[i]}
					}
					if err := resolveRefs(wrappedCurrent, wrappedElem, wrappedStored, wrappedDesired, resolvableProperties); err != nil {
						return err
					}
					// Copy back whatever the recursion produced, not only a map:
					// a reference whose recorded value was an object can resolve
					// to a scalar or a list now, and dropping that would leave
					// the stale object in place.
					modVal[i] = wrappedElem[k]
					// Alignment for an element happens here rather than in the
					// recursion: the recursion only sees a wrapper keyed by
					// position, while the provider may report elements in any
					// order, so the element to align is found by the echo
					// recorded with the reference.
					//
					// A converted element carries no reference of its own, so
					// fall back to the one the desired element names.
					ref := elemMap["$ref"]
					if ref == nil && len(desiredArr) > i {
						ref = desiredEnvRef(desiredArr[i])
					}
					alignComparisonElementForRef(currArr, storedRefElementByURI(storedArr, ref), wrappedElem[k])
					continue
				}
				// An already-resolved element. The unconverted desired array is
				// the same document in the same order, so its element at this
				// index names the reference this value came from; the stored
				// counterpart is then found by that reference.
				var desiredElem any
				if len(desiredArr) > i {
					desiredElem = desiredArr[i]
				}
				decision, handled, err := substituteResolvedRefInArray(desiredElem, storedArr, elem, currElem, resolvableProperties)
				if err != nil {
					return err
				}
				if handled {
					modVal[i] = decision.Value
					if decision.Matched {
						// Locate the element the provider reported for this
						// reference by the echo recorded alongside it, since the
						// provider may return elements in any order.
						if storedElem := storedRefElementByURI(storedArr, desiredEnvRef(desiredElem)); storedElem != nil {
							alignComparisonElement(currArr, storedElem["$value"], decision.Value)
						}
					}
				}
			}
		}
	}
	return nil
}

// assembleEmbedTemplate replaces each framed span in tmpl with the $value
// from its envelope JSON. Spans without a $value are replaced with "".
func assembleEmbedTemplate(tmpl string) (string, error) {
	spans, err := pkgmodel.ScanEmbedSpans(tmpl)
	if err != nil {
		return "", err
	}
	// Replace spans in reverse order so earlier offsets stay valid.
	result := tmpl
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		replacement := ""
		var envelope map[string]any
		if json.Unmarshal([]byte(span.EnvelopeJSON), &envelope) == nil {
			if val, ok := envelope["$value"]; ok {
				if s, ok := val.(string); ok {
					replacement = s
				}
			}
		}
		result = result[:span.Start] + replacement + result[span.End:]
	}
	return result, nil
}

// flattenRefs recursively flattens $ref / $value pairs
func flattenRefs(m map[string]any) {
	for k, v := range m {
		switch vv := v.(type) {
		case map[string]any:
			if embedVal, hasEmbed := vv["$embed"]; hasEmbed {
				if embedBool, ok := embedVal.(bool); ok && embedBool {
					if tmpl, hasTmpl := vv["$template"]; hasTmpl {
						if tmplStr, ok := tmpl.(string); ok {
							if assembled, err := assembleEmbedTemplate(tmplStr); err == nil {
								m[k] = assembled
							}
							// on scan error: leave node as-is; flattenRefs has no error path (corrupt templates are rejected at plan time)
							continue
						}
					}
				}
			}
			if _, hasRef := vv["$ref"]; hasRef {
				if val, hasVal := vv["$value"]; hasVal {
					m[k] = val
					continue
				}
				m[k] = ""
				continue
			}
			flattenRefs(vv)
		case []any:
			for i, elem := range vv {
				if elemMap, ok := elem.(map[string]any); ok {
					// Check if this element has a $ref that needs flattening
					if _, hasRef := elemMap["$ref"]; hasRef {
						if val, hasVal := elemMap["$value"]; hasVal {
							vv[i] = val
							continue
						}
						// If no $value is found, something is wrong. Preserve for debug
						continue
					}
					flattenRefs(elemMap)
				}
			}
		}
	}
}

// normalizeMixedStructures removes nested structure, keeping only flattened (dot notation) keys
func normalizeMixedStructures(m map[string]any) {
	for _, v := range m {
		switch vv := v.(type) {
		case map[string]any:
			if hasMixedStructure(vv) {
				normalizeToFlattenedKeys(vv)
			}
			normalizeMixedStructures(vv)
		case []any:
			for _, elem := range vv {
				if elemMap, ok := elem.(map[string]any); ok {
					normalizeMixedStructures(elemMap)
				}
			}
		}
	}
}

// hasMixedStructure checks if a map has both nested and flattened keys
func hasMixedStructure(m map[string]any) bool {
	hasNested := false
	hasFlattened := false

	for k := range m {
		if strings.Contains(k, ".") {
			hasFlattened = true
		} else if _, ok := m[k].(map[string]any); ok {
			hasNested = true
		}
	}

	return hasNested && hasFlattened
}

// normalizeToFlattenedKeys removes nested structure, keeping only flattened (dot notation) keys
func normalizeToFlattenedKeys(m map[string]any) {
	for k, v := range m {
		if !strings.Contains(k, ".") {
			if _, ok := v.(map[string]any); ok {
				delete(m, k)
			}
		}
	}
}

// substituteStableOccurrences copies the document-side value over the
// desired-side value for every destination path marked provably stable,
// walking dotted paths (numeric segments index arrays).
func substituteStableOccurrences(document, desired map[string]any, resolvableProperties resolver.ResolvableProperties) {
	var walk func(prefix string, node any)
	walk = func(prefix string, node any) {
		switch t := node.(type) {
		case map[string]any:
			if _, hasRef := t["$ref"]; hasRef || t["$res"] == true {
				if resolvableProperties.StableSuppressedAt(prefix) {
					if docVal, ok := valueAtPath(document, prefix); ok {
						setAtPath(desired, prefix, docVal)
					}
				}
				return
			}
			for k, v := range t {
				child := k
				if prefix != "" {
					child = prefix + "." + k
				}
				walk(child, v)
			}
		case []any:
			for i, v := range t {
				walk(prefix+"."+strconv.Itoa(i), v)
			}
		}
	}
	for k, v := range desired {
		walk(k, v)
	}
}

// valueAtPath resolves a dotted path in a decoded document; numeric segments
// index arrays.
func valueAtPath(root map[string]any, path string) (any, bool) {
	var cur any = root
	for _, seg := range strings.Split(path, ".") {
		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			cur = t[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// setAtPath writes a value at a dotted path in a decoded document; numeric
// segments index arrays. Missing intermediate containers abort the write
// (the caller substitutes only where the desired side already holds an
// envelope).
func setAtPath(root map[string]any, path string, value any) {
	segs := strings.Split(path, ".")
	var cur any = root
	for i, seg := range segs {
		last := i == len(segs)-1
		switch t := cur.(type) {
		case map[string]any:
			if last {
				t[seg] = value
				return
			}
			next, ok := t[seg]
			if !ok {
				return
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(t) {
				return
			}
			if last {
				t[idx] = value
				return
			}
			cur = t[idx]
		default:
			return
		}
	}
}

func flattenAndResolveRefs(document []byte, patch []byte, storedEnvelopes []byte, desiredEnvelopes []byte, resolvableProperties resolver.ResolvableProperties) ([]byte, []byte, error) {
	var current, mod map[string]any
	if err := json.Unmarshal(document, &current); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(patch, &mod); err != nil {
		return nil, nil, err
	}
	var stored map[string]any
	if storedEnvelopes != nil {
		if err := json.Unmarshal(storedEnvelopes, &stored); err != nil {
			return nil, nil, err
		}
	}
	var desired map[string]any
	if len(desiredEnvelopes) > 0 {
		if err := json.Unmarshal(desiredEnvelopes, &desired); err != nil {
			return nil, nil, err
		}
	}
	// Provenance suppression: an occurrence classified provably stable
	// substitutes the DOCUMENT side's value onto the desired side before
	// resolution and flattening, so the diff sees no change and the churn op
	// is never minted. The classification is decided upstream (the update
	// generator) and travels on the resolvable properties; absence of a mark
	// always means "do not suppress".
	substituteStableOccurrences(current, mod, resolvableProperties)

	if err := resolveRefs(current, mod, stored, desired, resolvableProperties); err != nil {
		return nil, nil, err
	}
	flattenRefs(current)
	flattenRefs(mod)

	// handle mixed nested/flattened structures
	normalizeMixedStructures(current)
	normalizeMixedStructures(mod)

	currentRes, err := json.Marshal(current)
	if err != nil {
		return nil, nil, err
	}
	modRes, err := json.Marshal(mod)
	if err != nil {
		return nil, nil, err
	}

	return currentRes, modRes, nil
}
