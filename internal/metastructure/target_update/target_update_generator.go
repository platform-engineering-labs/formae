// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type TargetDatastore interface {
	LoadTarget(label string) (*pkgmodel.Target, error)
	CountResourcesInTarget(targetLabel string) (int, error)
	LoadResourceById(ksuid string) (*pkgmodel.Resource, error)
}

type TargetUpdateGenerator struct {
	datastore TargetDatastore
}

// NewTargetUpdateGenerator creates a new target update generator
func NewTargetUpdateGenerator(ds TargetDatastore) *TargetUpdateGenerator {
	return &TargetUpdateGenerator{datastore: ds}
}

// GenerateTargetUpdates determines what target changes are needed.
// formaHasResources indicates whether the forma also declares resources;
// on destroy, plain targets (no $ref) are only preserved when the forma
// has resources (i.e., it's an application forma). Target-only formae
// always delete their targets — this is how users explicitly remove targets.
func (tp *TargetUpdateGenerator) GenerateTargetUpdates(targets []pkgmodel.Target, command pkgmodel.Command, formaHasResources bool) ([]TargetUpdate, error) {
	var updates []TargetUpdate

	for _, target := range targets {
		update, hasUpdate, err := tp.determineTargetUpdate(target, command, formaHasResources)
		if err != nil {
			return nil, fmt.Errorf("failed to determine target update for %s: %w", target.Label, err)
		}

		if !hasUpdate {
			slog.Debug("No target update needed", "label", target.Label)
			continue
		}

		updates = append(updates, update)
		slog.Debug("Generated target update",
			"label", target.Label,
			"operation", update.Operation)
	}

	return updates, nil
}

func (tp *TargetUpdateGenerator) determineTargetUpdate(target pkgmodel.Target, command pkgmodel.Command, formaHasResources bool) (TargetUpdate, bool, error) {
	now := util.TimeNow()

	existing, err := tp.datastore.LoadTarget(target.Label)
	if err != nil {
		return TargetUpdate{}, false, fmt.Errorf("failed to load target: %w", err)
	}

	// Handle destroy command. When the forma also has resources, only delete
	// targets with $ref dependencies — plain targets (docker, us-east-1)
	// survive because they exist independently. When the forma is target-only,
	// always delete: the user is explicitly removing the target.
	if command == pkgmodel.CommandDestroy {
		if existing == nil {
			slog.Debug("Target does not exist, nothing to delete", "label", target.Label)
			return TargetUpdate{}, false, nil
		}

		if formaHasResources {
			resolvables := resolver.ExtractResolvableURIsFromJSON(existing.Config)
			if len(resolvables) == 0 {
				slog.Debug("Target has no $ref dependencies, skipping delete", "label", target.Label)
				return TargetUpdate{}, false, nil
			}
		}

		// Cross-target DAG dependencies are built from ExistingTarget.Config
		// by the DAG — not from RemainingResolvables, which would cause the
		// target updater to attempt resolution during delete.
		return TargetUpdate{
			Target:         target,
			ExistingTarget: existing,
			Operation:      TargetOperationDelete,
			State:          TargetUpdateStateNotStarted,
			StartTs:        now,
			ModifiedTs:     now,
		}, true, nil
	}

	// Extract resolvables from target config
	resolvables := resolver.ExtractResolvableURIsFromJSON(target.Config)
	slog.Debug("Target resolvables extracted", "label", target.Label, "count", len(resolvables), "uris", resolvables)

	// Handle apply command (create or update)
	var operation TargetOperation
	if existing == nil {
		operation = TargetOperationCreate
	} else {
		// Namespace change is always an error
		if existing.Namespace != target.Namespace {
			return TargetUpdate{}, false, model.TargetAlreadyExistsError{
				TargetLabel:       target.Label,
				ExistingNamespace: existing.Namespace,
				FormaNamespace:    target.Namespace,
				MismatchType:      "namespace",
			}
		}

		// Determine which configs to compare. When the new config contains
		// $ref resolvables, resolve them first so we compare actual values.
		existingResolved, newResolved, err := tp.resolvedConfigs(existing.Config, target.Config, resolvables)
		if err != nil {
			return TargetUpdate{}, false, fmt.Errorf("failed to resolve target configs: %w", err)
		}

		// Always prefer the incoming schema — it represents the current plugin
		// version and may have new or updated hints. Fall back to the existing
		// schema only when the incoming has none (e.g., plugin removed annotations).
		schema := target.ConfigSchema
		if len(schema.Hints) == 0 {
			schema = existing.ConfigSchema
		}
		configChange := ClassifyConfigChange(existingResolved, newResolved, schema)
		switch configChange {
		case ConfigImmutableChange:
			operation = TargetOperationReplace
		case ConfigMutableChange:
			operation = TargetOperationUpdate
		case ConfigNoChange:
			// Resolved values are identical, but we still need to update if:
			// - The raw config format changed (e.g., plain value ↔ $ref wrapper)
			// - The discoverable flag changed
			// - The ConfigSchema changed (e.g., plugin annotations updated)
			if !util.JsonEqualRaw(existing.Config, target.Config) {
				operation = TargetOperationUpdate
			} else if existing.Discoverable != target.Discoverable {
				operation = TargetOperationUpdate
			} else if len(target.ConfigSchema.Hints) > 0 && !configSchemasEqual(existing.ConfigSchema, target.ConfigSchema) {
				// Only update for schema changes when the incoming schema has hints.
				// An empty incoming schema means the plugin/agent doesn't emit one —
				// don't wipe existing metadata.
				operation = TargetOperationUpdate
			} else {
				return TargetUpdate{}, false, nil
			}
		}
	}

	// Preserve existing ConfigSchema when the incoming target doesn't provide one.
	// Without this, persistTargetUpdate would write an empty schema, silently
	// clearing mutability metadata for future applies.
	if len(target.ConfigSchema.Hints) == 0 && existing != nil && len(existing.ConfigSchema.Hints) > 0 {
		target.ConfigSchema = existing.ConfigSchema
	}

	return TargetUpdate{
		Target:               target,
		ExistingTarget:       existing,
		Operation:            operation,
		State:                TargetUpdateStateNotStarted,
		StartTs:              now,
		ModifiedTs:           now,
		RemainingResolvables: resolvables,
	}, true, nil
}

// configSchemasEqual returns true if two ConfigSchemas have identical hints.
func configSchemasEqual(a, b pkgmodel.ConfigSchema) bool {
	if len(a.Hints) != len(b.Hints) {
		return false
	}
	for k, hintA := range a.Hints {
		hintB, ok := b.Hints[k]
		if !ok || hintA != hintB {
			return false
		}
	}
	return true
}

// resolvedConfigs returns the existing and new configs in a form suitable for
// field-level comparison. Both configs are stripped of $ref metadata so that
// ClassifyConfigChange compares plain values. When the new config contains
// $ref resolvables, they are resolved from the DB first.
func (tp *TargetUpdateGenerator) resolvedConfigs(existingConfig, newConfig json.RawMessage, resolvables []pkgmodel.FormaeURI) (json.RawMessage, json.RawMessage, error) {
	if len(resolvables) == 0 {
		// No resolvables in new config, but existing config may still have
		// $ref metadata from a previous apply. Strip it for comparison.
		existingValues, err := resolver.ConvertToPluginFormat(existingConfig)
		if err != nil {
			return existingConfig, newConfig, nil
		}
		return existingValues, newConfig, nil
	}

	// Resolve $ref values from DB for comparison
	resolvedConfig := make([]byte, len(newConfig))
	copy(resolvedConfig, newConfig)

	for _, uri := range resolvables {
		ksuid := uri.KSUID()
		propertyPath := uri.PropertyPath()

		resource, err := tp.datastore.LoadResourceById(ksuid)
		if err != nil || resource == nil {
			slog.Debug("Cannot resolve $ref from DB, treating as config change",
				"uri", uri, "error", err)
			return existingConfig, newConfig, nil
		}

		value := gjson.GetBytes(resource.Properties, propertyPath)
		if !value.Exists() {
			slog.Debug("Referenced property not found in resource, treating as config change",
				"uri", uri, "propertyPath", propertyPath)
			return existingConfig, newConfig, nil
		}

		resolvedConfig, err = resolver.ResolvePropertyReferences(uri, resolvedConfig, value.String())
		if err != nil {
			return existingConfig, newConfig, nil
		}
	}

	// Strip $ref metadata from both sides so ClassifyConfigChange sees plain values
	existingValues, err := resolver.ConvertToPluginFormat(existingConfig)
	if err != nil {
		return existingConfig, json.RawMessage(resolvedConfig), nil
	}
	newValues, err := resolver.ConvertToPluginFormat(resolvedConfig)
	if err != nil {
		return existingConfig, json.RawMessage(resolvedConfig), nil
	}

	return existingValues, newValues, nil
}
