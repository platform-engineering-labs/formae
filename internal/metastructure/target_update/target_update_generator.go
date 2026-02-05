// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type TargetDatastore interface {
	LoadTarget(label string) (*pkgmodel.Target, error)
	CountResourcesInTarget(targetLabel string) (int, error)
}

type TargetUpdateGenerator struct {
	datastore TargetDatastore
}

// NewTargetUpdateGenerator creates a new target update generator
func NewTargetUpdateGenerator(ds TargetDatastore) *TargetUpdateGenerator {
	return &TargetUpdateGenerator{datastore: ds}
}

// GenerateTargetUpdates determines what target changes are needed.
// hasResources indicates whether the forma has resources - if true and command is destroy,
// we skip target deletion since the target will still have resources after this command.
func (tp *TargetUpdateGenerator) GenerateTargetUpdates(targets []pkgmodel.Target, command pkgmodel.Command, hasResources bool) ([]TargetUpdate, error) {
	var updates []TargetUpdate

	for _, target := range targets {
		update, hasUpdate, err := tp.determineTargetUpdate(target, command, hasResources)
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

func (tp *TargetUpdateGenerator) determineTargetUpdate(target pkgmodel.Target, command pkgmodel.Command, hasResources bool) (TargetUpdate, bool, error) {
	now := util.TimeNow()

	existing, err := tp.datastore.LoadTarget(target.Label)
	if err != nil {
		return TargetUpdate{}, false, fmt.Errorf("failed to load target: %w", err)
	}

	// Handle destroy command
	if command == pkgmodel.CommandDestroy {
		// If the forma has resources, don't try to delete the target
		// (the resources need to be destroyed first)
		if hasResources {
			slog.Debug("Destroy command has resources, skipping target deletion", "label", target.Label)
			return TargetUpdate{}, false, nil
		}

		if existing == nil {
			// Target doesn't exist, nothing to delete
			slog.Debug("Target does not exist, nothing to delete", "label", target.Label)
			return TargetUpdate{}, false, nil
		}

		// Check if target has resources in the database
		count, err := tp.datastore.CountResourcesInTarget(target.Label)
		if err != nil {
			return TargetUpdate{}, false, fmt.Errorf("failed to count resources in target: %w", err)
		}

		if count > 0 {
			return TargetUpdate{}, false, model.TargetHasResourcesError{
				TargetLabel:   target.Label,
				ResourceCount: count,
			}
		}

		return TargetUpdate{
			Target:         target,
			ExistingTarget: existing,
			Operation:      TargetOperationDelete,
			State:          TargetUpdateStateNotStarted,
			StartTs:        now,
			ModifiedTs:     now,
		}, true, nil
	}

	// Handle apply command (create or update)
	var operation TargetOperation
	if existing == nil {
		operation = TargetOperationCreate
	} else {
		if err := ValidateImmutableFields(existing, &target); err != nil {
			return TargetUpdate{}, false, err
		}

		if existing.Discoverable == target.Discoverable {
			return TargetUpdate{}, false, nil
		}

		operation = TargetOperationUpdate
	}

	return TargetUpdate{
		Target:         target,
		ExistingTarget: existing,
		Operation:      operation,
		State:          TargetUpdateStateNotStarted,
		StartTs:        now,
		ModifiedTs:     now,
	}, true, nil
}
