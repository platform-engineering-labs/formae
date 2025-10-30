// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package target_update

import (
	"time"

	"github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type (
	TargetOperation   = types.OperationType
	TargetUpdateState = types.TargetUpdateState
)

const (
	TargetOperationCreate = types.OperationCreate
	TargetOperationUpdate = types.OperationUpdate

	TargetUpdateStateNotStarted = types.TargetUpdateStateNotStarted
	TargetUpdateStateSuccess    = types.TargetUpdateStateSuccess
	TargetUpdateStateFailed     = types.TargetUpdateStateFailed
)

// TargetUpdate represents an update to a target in the system
type TargetUpdate struct {
	Target         pkgmodel.Target   `json:"Target"`
	ExistingTarget *pkgmodel.Target  `json:"ExistingTarget,omitempty"`
	Operation      TargetOperation   `json:"Operation"`
	State          TargetUpdateState `json:"State"`
	StartTs        time.Time         `json:"StartTs"`
	ModifiedTs     time.Time         `json:"ModifiedTs"`
	Version        string            `json:"Version"`
	ErrorMessage   string            `json:"ErrorMessage,omitempty"`
}

// HasChange returns true if the discoverable field changed
func (tu *TargetUpdate) HasChange() bool {
	if tu.ExistingTarget == nil {
		return true
	}
	return tu.ExistingTarget.Discoverable != tu.Target.Discoverable
}

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
