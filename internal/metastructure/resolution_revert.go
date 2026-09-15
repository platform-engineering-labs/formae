// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/patch"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Revert protects the observed changed paths, while the ordinary three-way
// merge can still carry independent edits. Derive movement through the same
// schema/ownership patch machinery as provider planning, not a display diff.
func rejectRevertEditConflict(ds datastore.Datastore, prior, requested pkgmodel.Resource, observation *datastore.ResourceObservation) error {
	before, err := resolutionEditDocument(ds, prior)
	if err != nil {
		return err
	}
	after, err := resolutionEditDocument(ds, requested)
	if err != nil {
		return err
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	edits, err := resolutionChangedPaths(beforeJSON, afterJSON, beforeJSON, afterJSON, prior.Schema, prior.OwnedMembers)
	if err != nil {
		return err
	}
	// The write patch deliberately suppresses unresolved reference envelopes;
	// their normalized authored identity is still a user edit to this path.
	edits = append(edits, resolutionSymbolicEditPaths(before, after, "")...)
	if len(edits) == 0 {
		return nil
	}
	if observation == nil || observation.Operation == "delete" || observation.Resource == nil {
		return fmt.Errorf("resource edit conflicts with reverting its deletion")
	}
	oldProps, err := filterCoOwnedProperties(prior.Properties, prior.Schema, prior.OwnedMembers)
	if err != nil {
		return err
	}
	liveProps, err := filterCoOwnedProperties(observation.Resource.Properties, prior.Schema, prior.OwnedMembers)
	if err != nil {
		return err
	}
	oldComparison, err := resolver.ConvertExistingStateForComparison(oldProps)
	if err != nil {
		return err
	}
	liveComparison, err := resolver.ConvertExistingStateForComparison(liveProps)
	if err != nil {
		return err
	}
	drift, err := resolutionChangedPaths(oldComparison, liveComparison, oldProps, liveProps, prior.Schema, prior.OwnedMembers)
	if err != nil {
		return err
	}
	for _, path := range drift {
		for _, edit := range edits {
			if edit == path || strings.HasPrefix(edit, path+"/") || strings.HasPrefix(path, edit+"/") {
				return fmt.Errorf("resource edit at %s conflicts with reverting observed drift at %s", edit, path)
			}
		}
	}
	return nil
}

func resolutionChangedPaths(before, after, stored, desired json.RawMessage, schema pkgmodel.Schema, owned pkgmodel.OwnedMembers) ([]string, error) {
	ordinary, immutable, _, err := patch.GeneratePatch(before, after, stored, desired, resolver.ResolvableProperties{}, schema, owned, pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		return nil, err
	}
	previous, err := decodeResolutionJSON(before)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, raw := range []json.RawMessage{ordinary, immutable} {
		if len(raw) == 0 {
			continue
		}
		var ops []struct {
			Path  string
			Op    string
			Value json.RawMessage
		}
		if err := json.Unmarshal(raw, &ops); err != nil {
			return nil, err
		}
		for _, op := range ops {
			// requiredOnUpdate can resend an unchanged value alongside a real
			// change. A provider payload requirement is not a changed path.
			if op.Op != "remove" {
				value, err := decodeResolutionJSON(op.Value)
				if err != nil {
					return nil, err
				}
				if existing, found := resolutionPointerValue(previous, op.Path); found && resolutionJSONEqual(existing, value) {
					continue
				}
			}
			paths = append(paths, op.Path)
		}
	}
	return paths, nil
}

// Comparison retains authored expression identity and selectors, but ignores
// provider echoes and provenance attached to a persisted expression.
func resolutionEditDocument(ds datastore.Datastore, resource pkgmodel.Resource) (any, error) {
	value, err := decodeResolutionJSON(resource.Properties)
	if err != nil {
		return nil, err
	}
	var walk func(any) error
	walk = func(v any) error {
		switch n := v.(type) {
		case []any:
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		case map[string]any:
			if n["$res"] == true {
				stack, _ := n["$stack"].(string)
				label, _ := n["$label"].(string)
				typ, _ := n["$type"].(string)
				property, _ := n["$property"].(string)
				id, err := ds.GetKSUIDByTriplet(stack, label, typ)
				if err != nil {
					return err
				}
				// A new source declared in this same request has no stored ID yet.
				// Retain its authored identity; ordinary planning resolves it.
				if id != "" {
					n["$ref"] = "formae://" + id + "#/" + property
					for _, key := range []string{"$res", "$stack", "$label", "$type", "$property"} {
						delete(n, key)
					}
				}
			}
			if n["$gen"] == true {
				if _, ok := n["$generator"].(string); !ok {
					stack, _ := n["$stack"].(string)
					label, _ := n["$label"].(string)
					identity, err := ds.GetGeneratorIdentity(label, stack)
					if err != nil {
						return err
					}
					if identity.ID != "" {
						n["$generator"] = identity.ID
					}
				}
				if id, _ := n["$generator"].(string); id != "" {
					delete(n, "$stack")
					delete(n, "$label")
				}
			}
			if resolutionExpressionNode(n) {
				for _, key := range []string{"$value", "$hashed", "$applied", "$resolvedFrom"} {
					delete(n, key)
				}
			}
			for _, child := range n {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Literal opaque values retain their authoritative digest. Hash only this
	// comparison copy, using the persistence transformer's value semantics and
	// schema path walker; the supplied declaration still carries write input.
	// Expression envelopes retain authored identity and are never hashed here.
	var hashErr error
	hashLiteral := func(v any) (any, bool) {
		if resolutionExpressionNode(v) || hashErr != nil {
			return v, true
		}
		raw, err := json.Marshal(map[string]any{"literal": v})
		if err != nil {
			hashErr = err
			return v, true
		}
		hashed, _, err := transformations.NewPersistValueTransformerWithExactNumbers().ApplyToResource(&pkgmodel.Resource{
			Properties: raw, Schema: pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{"literal": {Opaque: true}}},
		})
		if err != nil {
			hashErr = err
			return v, true
		}
		result, err := decodeResolutionJSON(hashed.Properties)
		if err != nil {
			hashErr = err
			return v, true
		}
		return result.(map[string]any)["literal"], true
	}
	opaque := &transformations.OpaqueWalk{
		Opaque: transformations.OpaqueFields(resource.Schema, resource.Type),
		Match:  hashLiteral,
		OnMiss: func(v any) (any, bool) {
			if resolutionExpressionNode(v) {
				return v, true
			}
			if n, ok := v.(map[string]any); ok && n["$visibility"] == pkgmodel.VisibilityOpaque {
				return hashLiteral(v)
			}
			return v, false
		},
	}
	if properties, ok := value.(map[string]any); ok {
		opaque.WalkProperties(properties)
	}
	if hashErr != nil {
		return nil, hashErr
	}
	// Hash literals before walking expressions so a map-shaped secret's
	// contents cannot be mistaken for an authored reference envelope.
	if err := walk(value); err != nil {
		return nil, err
	}
	return value, nil
}

func resolutionExpressionNode(v any) bool {
	n, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ref := n["$ref"]
	return ref || n["$res"] == true || n["$gen"] == true
}

func resolutionPointerValue(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	for _, encoded := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			var found bool
			value, found = node[part]
			if !found {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			value = node[i]
		default:
			return nil, false
		}
	}
	return value, true
}

func resolutionSymbolicEditPaths(before, after any, path string) []string {
	if resolutionJSONEqual(before, after) {
		return nil
	}
	if symbolicNode(before) || symbolicNode(after) {
		return []string{path}
	}
	a, aok := before.(map[string]any)
	b, bok := after.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for key := range a {
			keys[key] = true
		}
		for key := range b {
			keys[key] = true
		}
		var paths []string
		for key := range keys {
			child := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			paths = append(paths, resolutionSymbolicEditPaths(a[key], b[key], child)...)
		}
		return paths
	}
	// Symbolic collections are atomic; a provider position cannot establish
	// declaration identity when elements move or disappear.
	if containsSymbolicNode(before) || containsSymbolicNode(after) {
		return []string{path}
	}
	return nil
}
