// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package blackbox

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/tests/testcontrol"
)

// ViolationKind classifies the type of invariant violation.
type ViolationKind int

const (
	ViolationPhantomResource    ViolationKind = iota // in inventory but not in cloud
	ViolationOrphanedResource                        // in cloud but not in inventory
	ViolationPropertyMismatch                        // inventory and cloud properties differ
	ViolationCommandNotTerminal                      // command not in terminal state
)

// Violation describes a single invariant violation.
type Violation struct {
	Kind    ViolationKind
	Message string
}

// CommandState is a simplified view of a command's status for invariant checking.
type CommandState struct {
	ID    string
	State string
}

// CheckInvariants checks the core correctness invariants against actual state.
// It returns any violations found.
//
// Invariants checked:
//  1. No phantom resources: every resource in inventory exists in cloud state
//  2. No orphaned cloud resources: every cloud resource is tracked in inventory
//  3. Property consistency: inventory properties match cloud state properties
func CheckInvariants(inventory []pkgmodel.Resource, cloudState map[string]testcontrol.CloudStateEntry) []Violation {
	var violations []Violation

	// Build lookup of inventory resources by NativeID
	inventoryByNativeID := make(map[string]pkgmodel.Resource, len(inventory))
	for _, res := range inventory {
		if res.NativeID != "" {
			inventoryByNativeID[res.NativeID] = res
		}
	}

	// Invariant 1: No phantom resources
	// Every resource in inventory should exist in cloud state
	for _, res := range inventory {
		if res.NativeID == "" {
			continue
		}
		if _, ok := cloudState[res.NativeID]; !ok {
			violations = append(violations, Violation{
				Kind:    ViolationPhantomResource,
				Message: fmt.Sprintf("phantom resource: %s (%s) in inventory but not in cloud state", res.Label, res.NativeID),
			})
		}
	}

	// Invariant 2: No orphaned cloud resources
	// Every cloud resource should be tracked in inventory
	for nativeID, entry := range cloudState {
		if _, ok := inventoryByNativeID[nativeID]; !ok {
			violations = append(violations, Violation{
				Kind:    ViolationOrphanedResource,
				Message: fmt.Sprintf("orphaned resource: %s (%s) in cloud state but not in inventory", nativeID, entry.ResourceType),
			})
		}
	}

	// Invariant 3: Property consistency
	// For resources present in both, properties should match
	for nativeID, cloudEntry := range cloudState {
		invRes, ok := inventoryByNativeID[nativeID]
		if !ok {
			continue // already reported as orphaned
		}
		invProps := string(invRes.Properties)
		if invProps != "" && cloudEntry.Properties != "" && invProps != cloudEntry.Properties {
			violations = append(violations, Violation{
				Kind: ViolationPropertyMismatch,
				Message: fmt.Sprintf("property mismatch for %s: inventory=%s cloud=%s",
					nativeID, invProps, cloudEntry.Properties),
			})
		}
	}

	return violations
}

// CheckCommandCompleteness verifies that all commands are in terminal states.
func CheckCommandCompleteness(commands []CommandState) []Violation {
	var violations []Violation
	for _, cmd := range commands {
		if cmd.State != "Success" && cmd.State != "Failed" && cmd.State != "Canceled" {
			violations = append(violations, Violation{
				Kind:    ViolationCommandNotTerminal,
				Message: fmt.Sprintf("command %s in non-terminal state: %s", cmd.ID, cmd.State),
			})
		}
	}
	return violations
}
