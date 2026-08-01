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
func (pv *PersistValueTransformer) ApplyToResource(resource *pkgmodel.Resource) (*pkgmodel.Resource, error) {
	if resource == nil {
		return nil, fmt.Errorf("resource cannot be nil")
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

	if resource.Properties != nil {
		transformedProps, err := pv.transformRawProps(resource.Properties, resource.Schema, resource.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to transform properties: %w", err)
		}
		transformedResource.Properties = transformedProps
	}

	if resource.ReadOnlyProperties != nil {
		transformedReadOnly, err := pv.transformRawProps(resource.ReadOnlyProperties, resource.Schema, resource.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to transform read-only properties: %w", err)
		}
		transformedResource.ReadOnlyProperties = transformedReadOnly
	}

	if resource.PatchDocument != nil {
		transformedPatchDoc, err := pv.transformPatchDocument(resource.PatchDocument, resource.Schema, resource.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to transform patch document: %w", err)
		}
		transformedResource.PatchDocument = transformedPatchDoc
	}

	return transformedResource, nil
}

func (pv *PersistValueTransformer) transformRawProps(properties json.RawMessage, schema pkgmodel.Schema, resourceType string) (json.RawMessage, error) {
	if len(properties) == 0 {
		return json.RawMessage("{}"), nil
	}
	var props map[string]any
	if err := json.Unmarshal(properties, &props); err != nil {
		return nil, fmt.Errorf("failed to unmarshal properties: %w", err)
	}

	opaqueFields := opaqueFieldSet(schema, resourceType)

	if err := pv.processProps(props, opaqueFields); err != nil {
		return nil, fmt.Errorf("failed to process properties: %w", err)
	}
	result, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed properties: %w", err)
	}
	return result, nil
}

// processProps hashes (a) any top-level property named in opaqueFields (schema-keyed,
// first cut = top-level scalars) and (b) any nested map carrying a $visibility=="Opaque"
// envelope. Idempotent: values already marked $hashed are skipped.
func (pv *PersistValueTransformer) processProps(m map[string]any, opaqueFields map[string]bool) error {
	for key, v := range m {
		if opaqueFields[key] {
			hashed, ok := pv.hashOpaqueField(v)
			if ok {
				m[key] = hashed
				continue
			}
		}
		switch val := v.(type) {
		case map[string]any:
			if visibility, ok := val["$visibility"].(string); ok && visibility == "Opaque" {
				if hashed, done := pv.hashEnvelope(val); done {
					m[key] = hashed
				}
			} else {
				if err := pv.processProps(val, nil); err != nil {
					return err
				}
			}
		case []any:
			for _, elem := range val {
				if elemMap, ok := elem.(map[string]any); ok {
					if err := pv.processProps(elemMap, nil); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// hashOpaqueField hashes a schema-opaque property value. It accepts a bare scalar
// (wrapping it into a hashed opaque envelope) or an existing envelope map.
func (pv *PersistValueTransformer) hashOpaqueField(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		// An existing opaque envelope carries $value — hash it in place. A raw map
		// WITHOUT $value is itself the secret value (a map-shaped secret field, e.g.
		// K8S decodedData); fall through so the whole map is hashed into one opaque
		// envelope rather than being mistaken for an envelope, which would leave the
		// plaintext keys beside a nil $value.
		if _, isEnvelope := m["$value"]; isEnvelope {
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

// transformPatchDocument hashes patch-op values purely structurally:
//   - if the op's path names a schema-opaque field, hash a bare scalar value (or leave an
//     already-$hashed envelope alone);
//   - if the op's value is itself an opaque envelope ({"$visibility":"Opaque",...}), hash it
//     in place unless it already carries $hashed:true.
//
// We deliberately do NOT substitute values by content match against other hashed properties:
// that both corrupted non-secret fields that happened to collide with a secret's plaintext and
// produced a bare (unmarked) digest, which hashOpaqueField treats as plaintext and re-hashes on
// the next boot backfill (hash-of-hash).
func (pv *PersistValueTransformer) transformPatchDocument(patchDoc json.RawMessage, schema pkgmodel.Schema, resourceType string) (json.RawMessage, error) {
	if len(patchDoc) == 0 {
		return patchDoc, nil
	}

	var patchOps []map[string]any
	if err := json.Unmarshal(patchDoc, &patchOps); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patch document: %w", err)
	}

	opaqueFields := make(map[string]bool)
	for f := range opaqueFieldSet(schema, resourceType) {
		opaqueFields["/"+f] = true
	}
	for i, op := range patchOps {
		value, hasValue := op["value"]
		if !hasValue {
			continue
		}
		if path, _ := op["path"].(string); opaqueFields[path] {
			if m, ok := value.(map[string]any); ok {
				if h, ok := m["$hashed"].(bool); ok && h {
					// Already a hashed envelope — idempotent, leave as-is.
					continue
				}
			}
			if s, ok := value.(string); ok {
				hashed := (&pkgmodel.Value{Value: s, Visibility: pkgmodel.VisibilityOpaque, Strategy: pkgmodel.StrategyUpdate}).Hash()
				patchOps[i]["value"] = map[string]any{
					"$value":      hashed.Value,
					"$visibility": pkgmodel.VisibilityOpaque,
					"$strategy":   hashed.Strategy,
					"$hashed":     true,
				}
				continue
			}
		}
		// Not a schema-opaque path, but the value itself may be an explicit opaque
		// envelope (e.g. a patch op targeting a non-top-level opaque field).
		if m, ok := value.(map[string]any); ok {
			if visibility, ok := m["$visibility"].(string); ok && visibility == "Opaque" {
				if hashed, done := pv.hashEnvelope(m); done {
					patchOps[i]["value"] = hashed
				}
			}
		}
	}

	transformedPatchDoc, err := json.Marshal(patchOps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transformed patch document: %w", err)
	}

	return json.RawMessage(transformedPatchDoc), nil
}
