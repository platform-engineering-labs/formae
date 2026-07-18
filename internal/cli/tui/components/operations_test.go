// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package components

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// buildMixedCommand builds a representative Command with creates, updates,
// deletes, replaces across resources, stacks, targets, and policies.
func buildMixedCommand() *apimodel.Command {
	return &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			// 1 resource create (ungrouped)
			{Operation: apimodel.OperationCreate},
			// 2 resource updates (ungrouped)
			{Operation: apimodel.OperationUpdate},
			{Operation: apimodel.OperationUpdate},
			// 1 resource delete (ungrouped)
			{Operation: apimodel.OperationDelete},
			// 1 grouped replace (delete + create in same GroupID)
			{Operation: apimodel.OperationDelete, GroupID: "grp-1"},
			{Operation: apimodel.OperationCreate, GroupID: "grp-1"},
			// reads should be ignored
			{Operation: apimodel.OperationRead},
		},
		TargetUpdates: []apimodel.TargetUpdate{
			{Operation: "create"},
		},
		StackUpdates: []apimodel.StackUpdate{
			{Operation: "create"},
		},
		PolicyUpdates: []apimodel.PolicyUpdate{
			{Operation: "create"},
		},
	}
}

func TestPromptForOperations_PlainTextStable(t *testing.T) {
	cmd := buildMixedCommand()
	out := PromptForOperations(cmd)
	plain := stripANSI(out)

	assert.Contains(t, plain, "create")
	assert.Contains(t, plain, "update")
	assert.Contains(t, plain, "delete")
	assert.Contains(t, plain, "replace")
	// Pin the exact joining / final-"and" / trailing prompt
	assert.Contains(t, plain, " and ")
	assert.Contains(t, plain, "Do you want to continue?")

	tuitest.RequireGolden(t, []byte(plain))
}

func TestPromptForOperations_Empty(t *testing.T) {
	cmd := &apimodel.Command{}
	out := PromptForOperations(cmd)
	assert.Equal(t, "", out)
}

func TestPromptForOperations_OnlyReads(t *testing.T) {
	cmd := &apimodel.Command{
		ResourceUpdates: []apimodel.ResourceUpdate{
			{Operation: apimodel.OperationRead},
		},
	}
	out := PromptForOperations(cmd)
	assert.Equal(t, "", out)
}
