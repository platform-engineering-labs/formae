// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type TargetDatastore interface {
	LoadTarget(label string) (*pkgmodel.Target, error)
}

type TargetUpdateGenerator struct {
	datastore TargetDatastore
}

// NewTargetUpdateGenerator creates a new target update generator
func NewTargetUpdateGenerator(ds TargetDatastore) *TargetUpdateGenerator {
	return &TargetUpdateGenerator{datastore: ds}
}

// GenerateTargetUpdates determines what target changes are needed
func (tp *TargetUpdateGenerator) GenerateTargetUpdates(targets []pkgmodel.Target, command pkgmodel.Command) ([]TargetUpdate, error) {
	// NOTE: Destroy command support for targets is intentionally deferred for future implementation.
	// Currently, destroy commands only operate on resources, not targets.
	if command == pkgmodel.CommandDestroy {
		return nil, nil
	}

	var updates []TargetUpdate

	for _, target := range targets {
		update, hasUpdate, err := tp.determineTargetUpdate(target)
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

func (tp *TargetUpdateGenerator) determineTargetUpdate(target pkgmodel.Target) (TargetUpdate, bool, error) {
	now := util.TimeNow()

	existing, err := tp.datastore.LoadTarget(target.Label)
	if err != nil {
		return TargetUpdate{}, false, fmt.Errorf("failed to load target: %w", err)
	}

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
