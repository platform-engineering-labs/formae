// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package stack_update

import (
	"fmt"
	"log/slog"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// StackDatastore defines the datastore operations needed for stack updates
type StackDatastore interface {
	GetStackByLabel(label string) (*pkgmodel.Stack, error)
}

// StackUpdateGenerator generates stack updates by comparing desired state with existing state
type StackUpdateGenerator struct {
	datastore StackDatastore
}

// NewStackUpdateGenerator creates a new stack update generator
func NewStackUpdateGenerator(ds StackDatastore) *StackUpdateGenerator {
	return &StackUpdateGenerator{datastore: ds}
}

// GenerateStackUpdates determines what stack changes are needed
func (sg *StackUpdateGenerator) GenerateStackUpdates(stacks []pkgmodel.Stack, command pkgmodel.Command) ([]StackUpdate, error) {
	// For destroy commands, we don't generate stack updates here.
	// Stack deletion happens implicitly when all resources in a stack are deleted
	// (via cleanupEmptyStacks in the changeset executor).
	if command == pkgmodel.CommandDestroy {
		return nil, nil
	}

	var updates []StackUpdate

	for _, stack := range stacks {
		update, hasUpdate, err := sg.determineStackUpdate(stack)
		if err != nil {
			return nil, fmt.Errorf("failed to determine stack update for %s: %w", stack.Label, err)
		}

		if !hasUpdate {
			slog.Debug("No stack update needed", "label", stack.Label)
			continue
		}

		updates = append(updates, update)
		slog.Debug("Generated stack update",
			"label", stack.Label,
			"operation", update.Operation)
	}

	return updates, nil
}

func (sg *StackUpdateGenerator) determineStackUpdate(stack pkgmodel.Stack) (StackUpdate, bool, error) {
	now := util.TimeNow()

	existing, err := sg.datastore.GetStackByLabel(stack.Label)
	if err != nil {
		return StackUpdate{}, false, fmt.Errorf("failed to load stack: %w", err)
	}

	var operation StackOperation
	if existing == nil {
		operation = StackOperationCreate
		// Generate ID upfront for new stacks so it's available throughout the flow
		stack.ID = util.NewID()
	} else {
		// Check if there's any change
		if existing.Description == stack.Description {
			return StackUpdate{}, false, nil
		}
		operation = StackOperationUpdate
		// Use the existing stack's ID
		stack.ID = existing.ID
	}

	return StackUpdate{
		Stack:         stack,
		ExistingStack: existing,
		Operation:     operation,
		State:         StackUpdateStateNotStarted,
		StartTs:       now,
		ModifiedTs:    now,
	}, true, nil
}
