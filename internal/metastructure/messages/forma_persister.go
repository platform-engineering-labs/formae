// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package messages

import (
	"encoding/json"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/policy_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/stack_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

type MarkResourceUpdateAsComplete struct {
	CommandID                  string
	ResourceURI                pkgmodel.FormaeURI
	Operation                  types.OperationType // The ResourceUpdate operation (create, delete, update, etc.)
	FinalState                 types.ResourceUpdateState
	ResourceStartTs            time.Time
	ResourceModifiedTs         time.Time
	ResourceProperties         json.RawMessage
	ResourceReadOnlyProperties json.RawMessage
	Version                    string
	// FailureReason carries a human-readable explanation for a failure that
	// was not recorded as plugin progress (e.g. a terminal resolve miss), so
	// the persisted command's error message survives a reload.
	FailureReason string
}

type UpdateResourceProgress struct {
	CommandID                  string
	ResourceURI                pkgmodel.FormaeURI
	Operation                  types.OperationType // The ResourceUpdate operation (create, delete, update, etc.)
	ResourceStartTs            time.Time
	ResourceModifiedTs         time.Time
	ResourceState              types.ResourceUpdateState
	Progress                   plugin.TrackedProgress
	ResourceProperties         json.RawMessage
	ResourceReadOnlyProperties json.RawMessage
	Version                    string
	// ResolvedRootDigests carries the update's resolution-provenance digests
	// (source URI -> canonical digest) so they become durable exactly when
	// progress does: recovery that resumes persisted progress skips
	// resolution and must stamp from these, never recompute.
	ResolvedRootDigests map[string]string
}

type MarkTargetUpdateAsComplete struct {
	CommandID       string
	TargetLabel     string
	TargetOperation string
	FinalState      types.TargetUpdateState
	ModifiedTs      time.Time
}

type UpdateStackStates struct {
	CommandID    string
	StackUpdates []stack_update.StackUpdate
}

type UpdatePolicyStates struct {
	CommandID     string
	PolicyUpdates []policy_update.PolicyUpdate
}
