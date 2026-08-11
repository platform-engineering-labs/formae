// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package transformations

import (
	"encoding/json"
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// knownOpaqueFields hard-codes the top-level secret fields per resource type so
// opaque hashing fires even when a plugin's runtime schema does NOT carry
// FieldHint.Opaque — e.g. a plugin built against a formae SDK that predates the
// Opaque field, whose model.Schema structurally drops it before the RPC. Keyed on
// resource Type. This is the agent-side guarantee: it does not depend on any plugin
// shipping an up-to-date schema. Keep in sync with the SecretValue-typed fields in
// the resource plugins. First cut: top-level scalar secret fields only.
var knownOpaqueFields = map[string][]string{
	"AWS::SecretsManager::Secret": {"SecretString"},
	"AWS::RDS::DBInstance":        {"MasterUserPassword", "TdeCredentialPassword"},
	"AWS::RDS::DBCluster":         {"MasterUserPassword"},
	"AWS::RDS::DatabaseRole":      {"Password"},
}

// opaqueFieldSet returns the union of the schema's opaque fields and the hard-coded
// known-opaque fields for the resource type — so hashing fires whether opacity comes
// from the plugin schema (FieldHint.Opaque) or the agent-side table.
func opaqueFieldSet(schema pkgmodel.Schema, resourceType string) map[string]bool {
	set := make(map[string]bool)
	for _, f := range schema.Opaque() {
		set[f] = true
	}
	for _, f := range knownOpaqueFields[resourceType] {
		set[f] = true
	}
	return set
}

// OpaqueFields is the exported view of opaqueFieldSet: the set of top-level property
// names that are opaque for a resource of the given schema and type (schema-declared
// UNION the hard-coded known-opaque table). Persistence and planning choke points in
// other packages use it to decide "is anything opaque here?" exactly the way the
// transformer decides what to hash — so a plugin whose schema drops FieldHint.Opaque
// can't cause those gates to skip hashing.
func OpaqueFields(schema pkgmodel.Schema, resourceType string) map[string]bool {
	return opaqueFieldSet(schema, resourceType)
}

type PersistValueTransformer struct{}

// Ensure PersistValueTransformer implements ResourceTransformer
var _ ResourceTransformer = (*PersistValueTransformer)(nil)

func NewPersistValueTransformer() *PersistValueTransformer {
	return &PersistValueTransformer{}
}

// ApplyToResource applies the transformation to hash all secret values in the resource
func (pv *PersistValueTransformer) ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, []Diagnostic, error) {
	if resource == nil {
		return nil, nil, fmt.Errorf("resource cannot be nil")
	}

	transformedResource := &pkgmodel.Resource{
		Label:    resource.Label,
		Type:     resource.Type,
		Stack:    resource.Stack,
		Target:   resource.Target,
		Schema:   resource.Schema,
		NativeID: resource.NativeID,
		Managed:  resource.Managed,
		Ksuid:    resource.Ksuid,
	}

	var diagnostics []Diagnostic

	if resource.Properties != nil {
		transformedProps, diags, err := pv.transformRawProps(resource.Properties, resource.Schema, resource.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to transform properties: %w", err)
		}
		transformedResource.Properties = transformedProps
		diagnostics = append(diagnostics, diags...)
	}

	if resource.ReadOnlyProperties != nil {
		transformedReadOnly, diags, err := pv.transformRawProps(resource.ReadOnlyProperties, resource.Schema, resource.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to transform read-only properties: %w", err)
		}
		transformedResource.ReadOnlyProperties = transformedReadOnly
		diagnostics = append(diagnostics, diags...)
	}

	if resource.PatchDocument != nil {
		transformedPatchDoc, diags, err := pv.transformPatchDocument(resource.PatchDocument, resource.Schema, resource.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to transform patch document: %w", err)
		}
		transformedResource.PatchDocument = transformedPatchDoc
		diagnostics = append(diagnostics, diags...)
	}

	return transformedResource, diagnostics, nil
}

func (pv *PersistValueTransformer) transformRawProps(properties json.RawMessage, schema pkgmodel.Schema, resourceType string) (json.RawMessage, []Diagnostic, error) {
	if len(properties) == 0 {
		return json.RawMessage("{}"), nil, nil
	}
	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal properties: %w", err)
	}

	walk := pv.newWalk(opaqueFieldSet(schema, resourceType))
	walk.WalkProperties(props)

	result, err := json.Marshal(props)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
	}
	return result, walk.Diagnostics(), nil
}

// newWalk builds the walk shared by the property and patch-document paths, so
// the two cannot drift on which names match. The set match hashes a named
// opaque field; on a miss the inline $visibility=="Opaque" envelope branch runs,
// which fires at any depth regardless of the hint set. Ordering matters: the
// name match is tested first, so a map-shaped secret that happens to carry a
// $value key is hashed whole rather than mistaken for an envelope.
func (pv *PersistValueTransformer) newWalk(opaqueFields map[string]bool) *OpaqueWalk {
	return &OpaqueWalk{
		Opaque: opaqueFields,
		Match:  func(v any) (any, bool) { return pv.hashOpaqueFieldValue(v) },
		OnMiss: pv.hashInlineEnvelope,
	}
}

// hashOpaqueFieldValue adapts hashOpaqueField to the walk's callback shape.
func (pv *PersistValueTransformer) hashOpaqueFieldValue(v any) (any, bool) {
	hashed, changed := pv.hashOpaqueField(v)
	if !changed {
		return v, false
	}
	return hashed, true
}

// hashInlineEnvelope hashes a value that is an inline opaque envelope and
// reports whether it claimed it. A claimed value is never descended into: its
// $value IS the secret.
func (pv *PersistValueTransformer) hashInlineEnvelope(v any) (any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if visibility, ok := m["$visibility"].(string); !ok || visibility != pkgmodel.VisibilityOpaque {
		return nil, false
	}
	hashed, _ := pv.hashEnvelope(m)
	return hashed, true
}

// hashOpaqueField hashes a schema-opaque property value. It accepts a bare scalar
// (wrapping it into a hashed opaque envelope) or an existing envelope map.
func (pv *PersistValueTransformer) hashOpaqueField(v any) (map[string]any, bool) {
	if v == nil {
		// null carries no secret material, so hashing it would fabricate a
		// digest for a value that is not there.
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		// A formae opaque envelope carries BOTH $value and $visibility — hash it in
		// place. A raw map is itself the secret value (a map-shaped secret field,
		// e.g. K8S decodedData), even when one of its keys happens to be named
		// "$value": without $visibility it is not an envelope. Fall through so the
		// whole map is hashed into one opaque envelope rather than being mistaken for
		// an envelope, which would hash only the "$value" key and leave sibling
		// plaintext keys at rest.
		_, hasValue := m["$value"]
		_, hasVisibility := m["$visibility"]
		if hasValue && hasVisibility {
			return pv.hashEnvelope(m)
		}
	}
	// Bare scalar (e.g. an opaque field supplied as a plain string literal, RDS
	// MasterUserPassword): wrap + hash into a CANONICAL formae.Value envelope. It
	// must carry $strategy — the same shape a `formae.value(x).opaque` literal
	// produces (PKL defaults the strategy to "Update"). Omitting it leaves a
	// non-canonical {$value,$visibility,$hashed} value that the extract PKL
	// generator does not recognize as an opaque value (its opaque-value branch
	// keys on $value+$visibility+$strategy); on a field whose type union includes
	// formae.Resolvable, union resolution then falls to the Resolvable arm and
	// emits a label-less {$res,$visibility:Opaque} that fails to evaluate.
	value := &pkgmodel.Value{Value: v, Visibility: pkgmodel.VisibilityOpaque, Strategy: pkgmodel.StrategyUpdate}
	hashed := value.Hash()
	return map[string]any{
		"$value":      hashed.Value,
		"$visibility": pkgmodel.VisibilityOpaque,
		"$strategy":   hashed.Strategy,
		"$hashed":     true,
	}, true
}

// hashEnvelope hashes an existing {$value,$visibility,...} map in place, unless already $hashed.
func (pv *PersistValueTransformer) hashEnvelope(val map[string]any) (map[string]any, bool) {
	if h, ok := val["$hashed"].(bool); ok && h {
		// Already hashed — never re-hash. But canonicalize a missing $strategy so a
		// legacy value persisted before the canonicalization fix (see hashOpaqueField)
		// still round-trips through extract as a formae.Value rather than a
		// label-less $res. Adding $strategy does not change the digest.
		if s, ok := val["$strategy"].(string); !ok || s == "" {
			val["$strategy"] = pkgmodel.StrategyUpdate
			return val, true
		}
		return val, false
	}
	original := val["$value"]
	value := &pkgmodel.Value{Value: original, Visibility: pkgmodel.VisibilityOpaque}
	if strategy, ok := val["$strategy"].(string); ok && strategy != "" {
		value.Strategy = strategy
	} else {
		// Default an absent strategy to "Update" so the persisted envelope is a
		// canonical formae.Value the extract generator recognizes (see
		// hashOpaqueField). Never override an explicit SetOnce.
		value.Strategy = pkgmodel.StrategyUpdate
	}
	hashed := value.Hash()
	val["$value"] = hashed.Value
	val["$strategy"] = hashed.Strategy
	val["$hashed"] = true
	return val, true
}

// transformPatchDocument hashes patch-op values purely structurally. A patch
// document is persisted, so a rotation that writes a new secret into one is the
// same leak as an unhashed property, at a different at-rest location.
//
// Each op's path is decoded as a JSON pointer into typed segments, from which
// every reading that could correspond to a hint name is generated. If one of
// those readings is itself an opaque name the value IS the secret and is hashed
// whole; otherwise the same node handler the property walk uses runs over the
// value, rooted at those readings — which is what reaches a secret nested
// inside a whole-container op, and what reaches an opaque envelope nested
// inside one.
//
// We deliberately do NOT substitute values by content match against other hashed properties:
// that both corrupted non-secret fields that happened to collide with a secret's plaintext and
// produced a bare (unmarked) digest, which hashOpaqueField treats as plaintext and re-hashes on
// the next boot backfill (hash-of-hash).
func (pv *PersistValueTransformer) transformPatchDocument(patchDoc json.RawMessage, schema pkgmodel.Schema, resourceType string) (json.RawMessage, []Diagnostic, error) {
	if len(patchDoc) == 0 {
		return patchDoc, nil, nil
	}

	var patchOps []map[string]any
	if err := json.Unmarshal(patchDoc, &patchOps); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal patch document: %w", err)
	}

	opaqueFields := opaqueFieldSet(schema, resourceType)
	walk := pv.newWalk(opaqueFields)
	var diagnostics []Diagnostic

	for i, op := range patchOps {
		// Keyed on the presence of a value, NOT on the op name: a "test" op
		// carries plaintext exactly as "add" and "replace" do and is persisted
		// the same way, while "copy" and "move" carry only a "from".
		value, hasValue := op["value"]
		if !hasValue {
			continue
		}

		path, _ := op["path"].(string)
		pointer, err := decodeJSONPointer(path)
		if err != nil {
			// An undecodable pointer is an internal defect, not untrusted input.
			// Skipping the op would leak and failing would abort persistence of
			// an already-completed command, so the value is processed in the
			// most conservative mode available instead.
			transformed, diags := pv.transformPatchValueConservatively(opaqueFields, value)
			patchOps[i]["value"] = transformed
			diagnostics = append(diagnostics, Diagnostic{
				Severity: DiagnosticError,
				Detail: fmt.Sprintf("patch path %q could not be decoded as a JSON pointer (%v); its value was processed conservatively, which may hash values that are not secrets",
					path, err),
			})
			diagnostics = append(diagnostics, diags...)
			continue
		}

		candidates, bounded := candidatePrefixes(pointer.Segments)
		if bounded {
			diagnostics = append(diagnostics, Diagnostic{
				Severity: DiagnosticError,
				Detail: fmt.Sprintf("patch path %q addresses more than %d collection positions; only the reading that elides all of them was tested",
					path, maxCandidateCollectionSegments),
			})
		}
		patchOps[i]["value"] = pv.transformPatchValue(walk, candidates, value)
	}

	transformedPatchDoc, err := json.Marshal(patchOps)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal transformed patch document: %w", err)
	}

	return json.RawMessage(transformedPatchDoc), append(diagnostics, walk.Diagnostics()...), nil
}

// transformPatchValue applies the same node handler the property walk uses to a
// patch op's value, rooted at the op's path.
//
// Running the full handler over the VALUE — rather than matching the op's path
// against leaf hint names — is what covers whole-container ops. The Atomic and
// EntitySet update methods emit ops that replace an entire object or array
// element ("replace /settings", "add /webhooks", "replace /webhooks/0"), and
// every one of those would keep its nested secret in cleartext under leaf
// matching. Walking the value also distinguishes a numeric object key from an
// array index structurally, because it inspects what is actually there.
func (pv *PersistValueTransformer) transformPatchValue(walk *OpaqueWalk, candidates []prefix, value any) any {
	// A candidate reading of the path that is itself an opaque name means the
	// op's value IS the secret — the exact-match/stop-descent rule.
	for _, p := range candidates {
		if walk.Opaque[p.name()] {
			walk.recordSegmentation(p.name(), p.steps)
			return pv.hashPatchValue(value)
		}
	}
	// Otherwise the value may itself be an inline opaque envelope, or carry the
	// secret somewhere inside it.
	if hashed, claimed := pv.hashInlineEnvelope(value); claimed {
		return hashed
	}
	return walk.walkValueAt(value, candidates)
}

// transformPatchValueConservatively processes an op whose path could not be
// decoded. Containers are walked with every hint name testable at any depth; a
// bare scalar has no keys to match, so the only way not to leak it is to hash
// it. Both over-hash on a path that should never occur.
func (pv *PersistValueTransformer) transformPatchValueConservatively(opaqueFields map[string]bool, value any) (any, []Diagnostic) {
	switch value.(type) {
	case map[string]any, []any:
		walk := pv.newWalk(opaqueFields)
		walk.MatchAtAnyDepth = true
		return walk.walkValueAt(value, []prefix{{}}), walk.Diagnostics()
	}
	if len(opaqueFields) == 0 {
		return value, nil
	}
	return pv.hashPatchValue(value), nil
}

// hashPatchValue hashes a patch value the op's path named as opaque. A string,
// number or bool is wrapped into a canonical opaque envelope; a map is hashed
// WHOLE, matching the map-shaped-secret rule on the property path (hashing it
// in place would digest one key and leave its siblings in cleartext); an
// already-hashed value is left alone, as is null.
func (pv *PersistValueTransformer) hashPatchValue(value any) any {
	if m, ok := value.(map[string]any); ok {
		if h, ok := m["$hashed"].(bool); ok && h {
			return value
		}
	}
	hashed, changed := pv.hashOpaqueField(value)
	if !changed {
		return value
	}
	return hashed
}
