// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package policy_update

import (
	"encoding/json"
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
	PolicyOperationAttach PolicyOperation = "attach"
	PolicyOperationDetach PolicyOperation = "detach" // Remove standalone policy attachment from stack
	PolicyOperationSkip   PolicyOperation = "skip"   // Policy not deleted because still referenced
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
	Policy           pkgmodel.Policy   `json:"-"`
	ExistingPolicy   pkgmodel.Policy   `json:"-"`
	Operation        PolicyOperation   `json:"Operation"`
	State            PolicyUpdateState `json:"State"`
	StackLabel       string            `json:"StackLabel"`       // For inline policies - the stack this policy belongs to
	PolicyRef        string            `json:"PolicyRef"`        // For attach operations - label of standalone policy being referenced
	ReferencingStacks []string         `json:"ReferencingStacks"` // For skip operations - stacks still referencing this policy
	StartTs          time.Time         `json:"StartTs"`
	ModifiedTs       time.Time         `json:"ModifiedTs"`
	Version          string            `json:"Version"`
	ErrorMessage     string            `json:"ErrorMessage,omitempty"`
}

// policyUpdateJSON is a helper struct for JSON marshaling/unmarshaling
type policyUpdateJSON struct {
	Policy            json.RawMessage   `json:"Policy,omitempty"`
	ExistingPolicy    json.RawMessage   `json:"ExistingPolicy,omitempty"`
	Operation         PolicyOperation   `json:"Operation"`
	State             PolicyUpdateState `json:"State"`
	StackLabel        string            `json:"StackLabel"`
	PolicyRef         string            `json:"PolicyRef"`
	ReferencingStacks []string          `json:"ReferencingStacks,omitempty"`
	StartTs           time.Time         `json:"StartTs"`
	ModifiedTs        time.Time         `json:"ModifiedTs"`
	Version           string            `json:"Version"`
	ErrorMessage      string            `json:"ErrorMessage,omitempty"`
}

// MarshalJSON implements custom JSON marshaling for PolicyUpdate
func (pu PolicyUpdate) MarshalJSON() ([]byte, error) {
	var policyJSON, existingPolicyJSON json.RawMessage
	var err error

	if pu.Policy != nil {
		policyJSON, err = json.Marshal(pu.Policy)
		if err != nil {
			return nil, err
		}
	}

	if pu.ExistingPolicy != nil {
		existingPolicyJSON, err = json.Marshal(pu.ExistingPolicy)
		if err != nil {
			return nil, err
		}
	}

	return json.Marshal(policyUpdateJSON{
		Policy:            policyJSON,
		ExistingPolicy:    existingPolicyJSON,
		Operation:         pu.Operation,
		State:             pu.State,
		StackLabel:        pu.StackLabel,
		PolicyRef:         pu.PolicyRef,
		ReferencingStacks: pu.ReferencingStacks,
		StartTs:           pu.StartTs,
		ModifiedTs:        pu.ModifiedTs,
		Version:           pu.Version,
		ErrorMessage:      pu.ErrorMessage,
	})
}

// UnmarshalJSON implements custom JSON unmarshaling for PolicyUpdate
func (pu *PolicyUpdate) UnmarshalJSON(data []byte) error {
	var helper policyUpdateJSON
	if err := json.Unmarshal(data, &helper); err != nil {
		return err
	}

	pu.Operation = helper.Operation
	pu.State = helper.State
	pu.StackLabel = helper.StackLabel
	pu.PolicyRef = helper.PolicyRef
	pu.ReferencingStacks = helper.ReferencingStacks
	pu.StartTs = helper.StartTs
	pu.ModifiedTs = helper.ModifiedTs
	pu.Version = helper.Version
	pu.ErrorMessage = helper.ErrorMessage

	if len(helper.Policy) > 0 {
		policy, err := pkgmodel.ParsePolicy(helper.Policy)
		if err != nil {
			return err
		}
		pu.Policy = policy
	}

	if len(helper.ExistingPolicy) > 0 {
		existingPolicy, err := pkgmodel.ParsePolicy(helper.ExistingPolicy)
		if err != nil {
			return err
		}
		pu.ExistingPolicy = existingPolicy
	}

	return nil
}

// PersistPolicyUpdates is a message to persist policy updates
type PersistPolicyUpdates struct {
	PolicyUpdates []PolicyUpdate
	CommandID     string
	StackIDMap    map[string]string // StackLabel -> StackID mapping for inline policies
}
