// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package drift

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// AddDeclaredAcceptances records declarations which normal planning proved
// match observed state. Call only after the complete drift gate succeeds.
// Ordinary updates already contribute their resulting declaration themselves.
func AddDeclaredAcceptances(ds datastore.Datastore, modifications map[string][]datastore.ResourceModification, forma *pkgmodel.Forma, command *forma_command.FormaCommand) error {
	contributed := make(map[string]bool)
	for _, update := range command.ResourceUpdates {
		contributed[update.DesiredState.Ksuid] = true
	}
	for stack, mods := range modifications {
		baseline, err := ds.GetResourcesAtLastReconcile(stack)
		if err != nil {
			return err
		}
		desiredByID := make(map[string]datastore.ResourceSnapshot)
		for _, prior := range baseline {
			desiredByID[prior.KSUID] = prior
		}

		reader, ok := ds.(datastore.ResourceObservationReader)
		if !ok {
			return fmt.Errorf("acceptance requires protected resource observations")
		}

		for _, mod := range mods {
			if contributed[mod.Ksuid] || mod.Operation != "update" {
				continue
			}
			for _, declared := range forma.Resources {
				if declared.Stack != mod.Stack || declared.Type != mod.Type || declared.Label != mod.Label {
					continue
				}
				observation, err := reader.GetResourceObservation(mod.Ksuid)
				if err != nil {
					return err
				}
				if observation == nil || (observation.Operation != "create" && observation.Operation != "update") {
					return fmt.Errorf("acceptance has no live observation for %s", mod.Ksuid)
				}
				observed := observation.Resource
				if observed != nil && (observed.Stack != declared.Stack || observed.Type != declared.Type || observed.Label != declared.Label) {
					return fmt.Errorf("acceptance observation identity changed for %s", mod.Ksuid)
				}
				if observation.StackID != "" {
					current, err := ds.GetStackByLabel(stack)
					if err != nil {
						return err
					}
					if current == nil || current.ID != observation.StackID {
						return fmt.Errorf("acceptance observation belongs to another stack incarnation")
					}
				}
				if observed == nil || observed.Version == "" {
					return fmt.Errorf("acceptance observation missing for %s", mod.Ksuid)
				}
				declared.Ksuid = observed.Ksuid
				declared.NativeID = observed.NativeID
				declared.OwnedMembers = observed.OwnedMembers
				if prior, ok := desiredByID[declared.Ksuid]; ok && prior.Declaration != nil {
					declared.OwnedMembers = prior.Declaration.OwnedMembers
				}
				effective, err := resource_update.ComputeEffectiveDesired(
					&pkgmodel.Forma{Resources: []pkgmodel.Resource{declared}},
					map[string][]*pkgmodel.Resource{stack: {observed}},
				)
				if err != nil {
					return err
				}
				declared.Properties = effective[declared.Ksuid]
				if prior, ok := desiredByID[declared.Ksuid]; ok {
					unchanged, err := sameAcceptedDeclaration(prior, declared)
					if err != nil {
						return err
					}
					if unchanged {
						continue
					}
				}

				command.ResourceUpdates = append(command.ResourceUpdates, resource_update.ResourceUpdate{
					DesiredState: declared, PriorState: *observed,
					Operation: resource_update.OperationAccept, State: resource_update.ResourceUpdateStateSuccess,
					Version: observation.Version, StackLabel: mod.Stack, Source: resource_update.FormaCommandSourceUser,
				})
				contributed[mod.Ksuid] = true
				break
			}
		}
	}
	return nil
}

// Reference execution metadata is an observation, not a declaration edit.
// Keep the reference identity, transforms, visibility and update strategy.
func declarationProperties(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid declaration JSON")
	}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var clean func(any)
	clean = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			_, ref := node["$ref"]
			gen := node["$gen"] == true
			if ref || gen {
				if strategy, ok := node["$strategy"]; !ok || strategy == "" {
					node["$strategy"] = pkgmodel.StrategyUpdate
				}

				delete(node, "$value")
				delete(node, "$hashed")
				delete(node, "$applied")
				delete(node, "$resolvedFrom")
			}
			for _, child := range node {
				clean(child)
			}
		case []any:
			for _, child := range node {
				clean(child)
			}
		}
	}
	clean(value)
	return json.Marshal(value)
}

func sameAcceptedDeclaration(prior datastore.ResourceSnapshot, declared pkgmodel.Resource) (bool, error) {
	// Stored schemas have passed through FieldHint.UnmarshalJSON's defaults
	// and deprecated-alias normalization; compare the incoming schema in the
	// same representation rather than treating those defaults as an edit.
	schemaJSON, err := json.Marshal(declared.Schema)
	if err != nil {
		return false, err
	}
	var normalizedSchema pkgmodel.Schema
	if err := json.Unmarshal(schemaJSON, &normalizedSchema); err != nil {
		return false, err
	}

	if prior.Label != declared.Label || prior.Type != declared.Type || prior.Target != declared.Target || !reflect.DeepEqual(prior.Schema, normalizedSchema) {
		return false, nil
	}
	before, err := declarationProperties(prior.Properties)
	if err != nil {
		return false, err
	}
	after, err := declarationProperties(declared.Properties)
	if err != nil {
		return false, err
	}
	// Numeric spellings can differ while the cleaned declaration is the
	// same. Check that equivalence before asymmetric opaque hashing adds
	// execution metadata to value-less generator envelopes. Keep the
	// exact structure here; array order and empty-value decisions must still
	// pass through the subsequent schema-aware comparison.
	equal, err := util.JsonEqualExactNumbers(before, after)
	if err != nil {
		return false, err
	}
	if equal {
		return true, nil
	}
	existing := pkgmodel.Resource{Properties: before}
	changed, err := resource_update.CompareFilteredResourceForUpdateExactNumbers(&existing, &declared, declared.Schema, after)
	return !changed, err
}
