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
// resourceTargetLabels is the set of target labels that have resources in the forma.
// On destroy, a target is only deleted if it has no resources in the forma (i.e., the
// user is not managing individual resources in it). The DAG handles cascade-deleting
// all resources in the target before the target itself.
func (tp *TargetUpdateGenerator) GenerateTargetUpdates(targets []pkgmodel.Target, command pkgmodel.Command, resourceTargetLabels map[string]bool) ([]TargetUpdate, error) {
	var updates []TargetUpdate

	for _, target := range targets {
		update, hasUpdate, err := tp.determineTargetUpdate(target, command, resourceTargetLabels[target.Label])
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

func (tp *TargetUpdateGenerator) determineTargetUpdate(target pkgmodel.Target, command pkgmodel.Command, hasResourcesInTarget bool) (TargetUpdate, bool, error) {
	now := util.TimeNow()

	existing, err := tp.datastore.LoadTarget(target.Label)
	if err != nil {
		return TargetUpdate{}, false, fmt.Errorf("failed to load target: %w", err)
	}

	// Handle destroy command
	if command == pkgmodel.CommandDestroy {
		// If the forma has resources in this target, only those resources are
		// destroyed — the target itself is preserved.
		if hasResourcesInTarget {
			slog.Debug("Destroy command has resources in target, skipping target deletion", "label", target.Label)
			return TargetUpdate{}, false, nil
		}

		if existing == nil {
			slog.Debug("Target does not exist, nothing to delete", "label", target.Label)
			return TargetUpdate{}, false, nil
		}

		// Target is being destroyed without explicit resources in the forma.
		// The DAG will cascade-delete all resources in this target before
		// deleting the target itself.
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
	slog.Info("Target resolvables extracted", "label", target.Label, "count", len(resolvables), "uris", resolvables, "config", string(target.Config))

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
