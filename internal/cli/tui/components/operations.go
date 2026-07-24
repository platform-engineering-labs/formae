// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// PromptForOperations returns a human-readable prompt summarising the
// operations that will be performed by cmd, followed by a confirmation
// question. Returns "" when there is nothing to do. th supplies the active
// theme's colors for the rendered summary.
func PromptForOperations(th *theme.Theme, cmd *apimodel.Command) string {
	targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces := analyzeCommands(cmd)

	if targetCreates == 0 && targetUpdates == 0 && stackCreates == 0 && stackUpdates == 0 && policyCreates == 0 && policyUpdates == 0 && resourceCreates == 0 && resourceUpdates == 0 && resourceDeletes == 0 && resourceReplaces == 0 {
		return ""
	}

	summary := operationSummary(th, targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces)
	if summary == "" {
		return ""
	}

	return summary + "\n\nDo you want to continue?"
}

// analyzeCommands counts each operation type in cmd, grouping grouped
// (delete+create) pairs as replaces.
func analyzeCommands(cmd *apimodel.Command) (targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces int) {
	// Group operations by GroupId
	groupedOperations := make(map[string][]apimodel.ResourceUpdate)
	ungroupedOperations := make([]apimodel.ResourceUpdate, 0)

	for _, rc := range cmd.ResourceUpdates {
		if rc.Operation == apimodel.OperationRead {
			continue
		}

		if rc.GroupID != "" {
			groupedOperations[rc.GroupID] = append(groupedOperations[rc.GroupID], rc)
		} else {
			ungroupedOperations = append(ungroupedOperations, rc)
		}
	}

	// Count grouped operations
	for _, group := range groupedOperations {
		if len(group) == 0 {
			continue
		}

		hasDelete := false
		hasCreate := false
		hasUpdate := false

		for _, op := range group {
			switch op.Operation {
			case apimodel.OperationDelete:
				hasDelete = true
			case apimodel.OperationCreate:
				hasCreate = true
			case apimodel.OperationUpdate:
				hasUpdate = true
			}
		}

		if hasDelete && hasCreate {
			resourceReplaces++
		} else if hasDelete {
			resourceDeletes++
		} else if hasCreate {
			resourceCreates++
		} else if hasUpdate {
			resourceUpdates++
		}
	}

	// Count ungrouped operations
	for _, rc := range ungroupedOperations {
		switch rc.Operation {
		case apimodel.OperationCreate:
			resourceCreates++
		case apimodel.OperationUpdate:
			resourceUpdates++
		case apimodel.OperationDelete:
			resourceDeletes++
		case apimodel.OperationReplace:
			resourceReplaces++
		}
	}

	for _, tu := range cmd.TargetUpdates {
		switch tu.Operation {
		case "create":
			targetCreates++
		case "update":
			targetUpdates++
		}
	}

	// Count stack updates
	for _, su := range cmd.StackUpdates {
		switch su.Operation {
		case "create":
			stackCreates++
		case "update":
			stackUpdates++
		}
	}

	// Count policy updates
	for _, pu := range cmd.PolicyUpdates {
		switch pu.Operation {
		case "create":
			policyCreates++
		case "update":
			policyUpdates++
		}
	}

	return
}

// operationSummary builds the colored "This operation will …" sentence.
// Colors use theme roles: Error for destructive ops (delete/replace), Done for
// creates, and TextPrimary for updates (no gold; routine updates aren't tinted).
func operationSummary(th *theme.Theme, targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces int) string {
	if targetCreates == 0 && targetUpdates == 0 && stackCreates == 0 && stackUpdates == 0 && policyCreates == 0 && policyUpdates == 0 && resourceCreates == 0 && resourceUpdates == 0 && resourceDeletes == 0 && resourceReplaces == 0 {
		return ""
	}

	errSt := lipgloss.NewStyle().Foreground(th.Palette.Error)
	doneSt := lipgloss.NewStyle().Foreground(th.Palette.Done)
	updateSt := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary)

	var parts []string

	// Destructive resource ops first
	if resourceDeletes > 0 {
		parts = append(parts, errSt.Render(fmt.Sprintf("delete %d resource(s)", resourceDeletes)))
	}
	if resourceReplaces > 0 {
		parts = append(parts, errSt.Render(fmt.Sprintf("replace %d resource(s)", resourceReplaces)))
	}

	// Creates (stacks, policies, targets, resources)
	if stackCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d stack(s)", stackCreates)))
	}
	if policyCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d policy(ies)", policyCreates)))
	}
	if targetCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d target(s)", targetCreates)))
	}
	if resourceCreates > 0 {
		parts = append(parts, doneSt.Render(fmt.Sprintf("create %d resource(s)", resourceCreates)))
	}

	// Updates (stacks, policies, targets, resources)
	if stackUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d stack(s)", stackUpdates)))
	}
	if policyUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d policy(ies)", policyUpdates)))
	}
	if targetUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d target(s)", targetUpdates)))
	}
	if resourceUpdates > 0 {
		parts = append(parts, updateSt.Render(fmt.Sprintf("update %d resource(s)", resourceUpdates)))
	}

	var joinedParts string
	if len(parts) == 1 {
		joinedParts = parts[0]
	} else {
		joinedParts = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}

	return fmt.Sprintf("This operation will %s.", joinedParts)
}
