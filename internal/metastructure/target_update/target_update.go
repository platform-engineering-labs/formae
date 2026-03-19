// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"fmt"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type (
	TargetOperation   = types.OperationType
	TargetUpdateState = types.TargetUpdateState
)

const (
	TargetOperationCreate  = types.OperationCreate
	TargetOperationUpdate  = types.OperationUpdate
	TargetOperationDelete  = types.OperationDelete
	TargetOperationReplace = types.OperationReplace

	TargetUpdateStateNotStarted = types.TargetUpdateStateNotStarted
	TargetUpdateStateInProgress = types.TargetUpdateStateInProgress
	TargetUpdateStateSuccess    = types.TargetUpdateStateSuccess
	TargetUpdateStateFailed     = types.TargetUpdateStateFailed
)

// TargetUpdate represents an update to a target in the system
type TargetUpdate struct {
	Target               pkgmodel.Target      `json:"Target"`
	ExistingTarget       *pkgmodel.Target     `json:"ExistingTarget,omitempty"`
	Operation            TargetOperation      `json:"Operation"`
	State                TargetUpdateState    `json:"State"`
	StartTs              time.Time            `json:"StartTs"`
	ModifiedTs           time.Time            `json:"ModifiedTs"`
	Version              string               `json:"Version"`
	ErrorMessage         string               `json:"ErrorMessage,omitempty"`
	RemainingResolvables []pkgmodel.FormaeURI `json:"RemainingResolvables,omitempty"`
}

// HasChange returns true if the discoverable field changed
func (tu *TargetUpdate) HasChange() bool {
	if tu.ExistingTarget == nil {
		return true
	}
	return tu.ExistingTarget.Discoverable != tu.Target.Discoverable
}

// NodeURI returns a synthetic URI for the target update, used as a DAG node key.
func (tu *TargetUpdate) NodeURI() pkgmodel.FormaeURI {
	return pkgmodel.FormaeURI("target://" + tu.Target.Label + "/" + string(tu.Operation))
}

// Resolvables returns the remaining resolvable URIs for this target update.
func (tu *TargetUpdate) Resolvables() []pkgmodel.FormaeURI { return tu.RemainingResolvables }

// ResolveValue substitutes a resolved value into the target's Config for the given URI.
func (tu *TargetUpdate) ResolveValue(formaeUri pkgmodel.FormaeURI, value string) error {
	config, err := resolver.ResolvePropertyReferences(formaeUri, tu.Target.Config, value)
	if err != nil {
		return fmt.Errorf("failed to resolve target config: %w", err)
	}
	tu.Target.Config = config
	return nil
}

// Namespace returns the target's namespace.
func (tu *TargetUpdate) Namespace() string { return tu.Target.Namespace }

// IsRateLimited returns false because target updates are local datastore operations.
func (tu *TargetUpdate) IsRateLimited() bool { return false }

// IsReady returns true if the target update has not yet started.
func (tu *TargetUpdate) IsReady() bool { return tu.State == TargetUpdateStateNotStarted }

// IsRunning returns true if the target update is in progress.
func (tu *TargetUpdate) IsRunning() bool { return tu.State == TargetUpdateStateInProgress }

// IsSuccess returns true if the target update completed successfully.
func (tu *TargetUpdate) IsSuccess() bool { return tu.State == TargetUpdateStateSuccess }

// IsFailed returns true if the target update failed.
func (tu *TargetUpdate) IsFailed() bool { return tu.State == TargetUpdateStateFailed }

// MarkInProgress transitions the target update to the InProgress state.
func (tu *TargetUpdate) MarkInProgress() { tu.State = TargetUpdateStateInProgress }

// MarkFailed transitions the target update to the Failed state.
func (tu *TargetUpdate) MarkFailed() { tu.State = TargetUpdateStateFailed }

// ValidateImmutableFields validates that immutable fields (namespace, config) haven't changed
// Returns an error if namespace or config differ between existing and new targets
func ValidateImmutableFields(existing, new *pkgmodel.Target) error {
	if existing.Namespace != new.Namespace {
		return model.TargetAlreadyExistsError{
			TargetLabel:       new.Label,
			ExistingNamespace: existing.Namespace,
			FormaNamespace:    new.Namespace,
			MismatchType:      "namespace",
		}
	}

	if !util.JsonEqualRaw(existing.Config, new.Config) {
		return model.TargetAlreadyExistsError{
			TargetLabel:    new.Label,
			ExistingConfig: existing.Config,
			FormaConfig:    new.Config,
			MismatchType:   "config",
		}
	}

	return nil
}

// PersistTargetUpdates is sent to the ResourcePersister actor to persist target updates
// to the datastore.
type PersistTargetUpdates struct {
	TargetUpdates []TargetUpdate
	CommandID     string
}

// UpdateTargetStates is sent to the FormaCommandPersister to update the
// target update states in the command record.
type UpdateTargetStates struct {
	CommandID     string
	TargetUpdates []TargetUpdate
}

// ShouldTriggerDiscovery determines if discovery should be triggered
// for a target update based on whether the target is newly discoverable.
// Returns true if:
//   - For create operations: the target is discoverable
//   - For update operations: the target is discoverable AND wasn't discoverable before
func ShouldTriggerDiscovery(update *TargetUpdate) bool {
	if !update.Target.Discoverable {
		return false
	}

	if update.Operation == TargetOperationCreate {
		return true
	}

	if update.Operation == TargetOperationUpdate {
		// Trigger if target wasn't discoverable before
		return update.ExistingTarget == nil || !update.ExistingTarget.Discoverable
	}

	return false
}
