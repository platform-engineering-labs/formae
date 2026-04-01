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

		if len(resolvables) > 0 {
			// Configs with $ref resolvables: resolve and compare.
			// Any change to a resolvable field is treated as immutable (replace).
			configChanged, err := tp.configChanged(existing.Config, target.Config, resolvables)
			if err != nil {
				return TargetUpdate{}, false, fmt.Errorf("failed to compare target configs: %w", err)
			}
			if configChanged {
				operation = TargetOperationReplace
			} else if existing.Discoverable != target.Discoverable {
				operation = TargetOperationUpdate
			} else {
				return TargetUpdate{}, false, nil
			}
		} else {
			// No resolvables: use per-field config mutability classification.
			configChange := ClassifyConfigChange(existing.Config, target.Config, existing.ConfigSchema)
			switch configChange {
			case ConfigImmutableChange:
				operation = TargetOperationReplace
			case ConfigMutableChange:
				operation = TargetOperationUpdate
			case ConfigNoChange:
				if existing.Discoverable != target.Discoverable {
					operation = TargetOperationUpdate
				} else {
					return TargetUpdate{}, false, nil
				}
			}
		}
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

// configChanged compares the existing stored config with the new config.
// When the new config contains $ref objects, we resolve them from the DB
// to get actual values for comparison.
func (tp *TargetUpdateGenerator) configChanged(existingConfig, newConfig json.RawMessage, resolvables []pkgmodel.FormaeURI) (bool, error) {
	if len(resolvables) == 0 {
		return !util.JsonEqualRaw(existingConfig, newConfig), nil
	}

	// Resolve $ref values from DB for comparison
	resolvedConfig := make([]byte, len(newConfig))
	copy(resolvedConfig, newConfig)

	for _, uri := range resolvables {
		ksuid := uri.KSUID()
		propertyPath := uri.PropertyPath()

		resource, err := tp.datastore.LoadResourceById(ksuid)
		if err != nil || resource == nil {
			// Can't resolve — treat as a config change
			slog.Debug("Cannot resolve $ref from DB, treating as config change",
				"uri", uri, "error", err)
			return true, nil
		}

		// Extract the property value from the resource
		value := gjson.GetBytes(resource.Properties, propertyPath)
		if !value.Exists() {
			slog.Debug("Referenced property not found in resource, treating as config change",
				"uri", uri, "propertyPath", propertyPath)
			return true, nil
		}

		// Substitute the resolved value into the config
		resolvedConfig, err = resolver.ResolvePropertyReferences(uri, resolvedConfig, value.String())
		if err != nil {
			return true, nil
		}
	}

	// Compare resolved values: strip $ref metadata from both sides
	existingValues, err := resolver.ConvertToPluginFormat(existingConfig)
	if err != nil {
		return !util.JsonEqualRaw(existingConfig, json.RawMessage(resolvedConfig)), nil
	}
	newValues, err := resolver.ConvertToPluginFormat(resolvedConfig)
	if err != nil {
		return !util.JsonEqualRaw(existingConfig, json.RawMessage(resolvedConfig)), nil
	}

	return !util.JsonEqualRaw(existingValues, newValues), nil
}
