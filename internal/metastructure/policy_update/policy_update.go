// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package policy_update

import (
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// PolicyOperation represents the type of policy operation
type PolicyOperation string

const (
	PolicyOperationCreate PolicyOperation = "create"
	PolicyOperationUpdate PolicyOperation = "update"
	PolicyOperationDelete PolicyOperation = "delete"
)

// Re-export state constants for convenience
const (
	PolicyUpdateStateNotStarted = types.PolicyUpdateStateNotStarted
	PolicyUpdateStateSuccess    = types.PolicyUpdateStateSuccess
	PolicyUpdateStateFailed     = types.PolicyUpdateStateFailed
)

// PolicyUpdateState is an alias for types.PolicyUpdateState
type PolicyUpdateState = types.PolicyUpdateState

// PolicyUpdate represents a policy change operation
type PolicyUpdate struct {
	Policy         pkgmodel.Policy
	ExistingPolicy pkgmodel.Policy
	Operation      PolicyOperation
	State          PolicyUpdateState
	StackLabel     string // For inline policies - the stack this policy belongs to
	StartTs        time.Time
	ModifiedTs     time.Time
	Version        string
	ErrorMessage   string
}

// PersistPolicyUpdates is a message to persist policy updates
type PersistPolicyUpdates struct {
	PolicyUpdates []PolicyUpdate
	CommandID     string
	StackIDMap    map[string]string // StackLabel -> StackID mapping for inline policies
}
