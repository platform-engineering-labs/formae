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

		if !util.JsonEqualRaw(existing.Config, target.Config) {
			operation = TargetOperationReplace
		} else if existing.Discoverable != target.Discoverable {
			operation = TargetOperationUpdate
		} else {
			return TargetUpdate{}, false, nil
		}
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
