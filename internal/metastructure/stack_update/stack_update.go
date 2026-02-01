// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package stack_update

import (
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type (
	StackOperation   = types.OperationType
	StackUpdateState = types.StackUpdateState
)

const (
	StackOperationCreate = types.OperationCreate
	StackOperationUpdate = types.OperationUpdate
	StackOperationDelete = types.OperationDelete

	StackUpdateStateNotStarted = types.StackUpdateStateNotStarted
	StackUpdateStateSuccess    = types.StackUpdateStateSuccess
	StackUpdateStateFailed     = types.StackUpdateStateFailed
)

// StackUpdate represents an update to a stack in the system
type StackUpdate struct {
	Stack         pkgmodel.Stack   `json:"Stack"`
	ExistingStack *pkgmodel.Stack  `json:"ExistingStack,omitempty"`
	Operation     StackOperation   `json:"Operation"`
	State         StackUpdateState `json:"State"`
	StartTs       time.Time        `json:"StartTs"`
	ModifiedTs    time.Time        `json:"ModifiedTs"`
	Version       string           `json:"Version"`
	ErrorMessage  string           `json:"ErrorMessage,omitempty"`
}

// HasChange returns true if there's a meaningful change to the stack
func (su *StackUpdate) HasChange() bool {
	if su.ExistingStack == nil {
		return true
	}
	return su.ExistingStack.Description != su.Stack.Description
}

// PersistStackUpdates is sent to the ResourcePersister actor to persist stack updates
// to the datastore.
type PersistStackUpdates struct {
	StackUpdates []StackUpdate
	CommandID    string
}
