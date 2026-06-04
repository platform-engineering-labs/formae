// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// RFC-0041: pure label rename. The renderer keeps the existing `update`
// verb and surfaces the rename inside the `by doing the following:` block
// as a `change label from "<old>" to "<new>"` entry, matching the
// `change property` style.
func TestRenderSimulation_PureLabelRename(t *testing.T) {
	simulation := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "rename-pure-id",
			Command:   "apply",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:    util.NewID(),
					ResourceType:  "FakeAWS::EC2::Instance",
					ResourceLabel: "app-server",
					OldLabel:      "web-server",
					StackName:     "prod",
					OldStackName:  "prod",
					Operation:     apimodel.OperationUpdate,
					State:         "NotStarted",
				},
			},
		},
	}

	result, err := RenderSimulation(&simulation)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "update resource app-server", "verb stays as update")
	assert.Contains(t, result, "by doing the following:", "rename surfaces inside the patch block")
	assert.Contains(t, result, `change label from "web-server" to "app-server"`, "label rename rendered as a patch-style entry")
	assert.NotContains(t, result, "label: web-server -> app-server", "no parent-level label sub-line")
	assert.NotContains(t, result, "RENAME", "no dedicated RENAME verb")
}

// Label rename + property change — both surface inside the same
// `by doing the following:` block. Label first, then property changes.
func TestRenderSimulation_RenameWithPropertyChange(t *testing.T) {
	patch := json.RawMessage(`[{"op":"replace","path":"/InstanceType","value":"t3.medium"}]`)
	oldProps := json.RawMessage(`{"InstanceType":"t3.small"}`)
	newProps := json.RawMessage(`{"InstanceType":"t3.medium"}`)

	simulation := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "rename-with-update-id",
			Command:   "apply",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:    util.NewID(),
					ResourceType:  "FakeAWS::EC2::Instance",
					ResourceLabel: "app-server",
					OldLabel:      "web-server",
					StackName:     "prod",
					OldStackName:  "prod",
					Operation:     apimodel.OperationUpdate,
					State:         "NotStarted",
					PatchDocument: patch,
					Properties:    newProps,
					OldProperties: oldProps,
				},
			},
		},
	}

	result, err := RenderSimulation(&simulation)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "update resource app-server")
	assert.Contains(t, result, `change label from "web-server" to "app-server"`)
	assert.Contains(t, result, "InstanceType", "property change still rendered")
}

// RFC-0041 edge case: bring-under-management + rename. The `change label`
// entry appears alongside the `from unmanaged to <stack>` sub-line on the
// parent.
func TestRenderSimulation_BringingUnderManagementAndRename(t *testing.T) {
	simulation := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "import-and-rename-id",
			Command:   "apply",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:    util.NewID(),
					ResourceType:  "FakeAWS::EC2::Instance",
					ResourceLabel: "web-server",
					OldLabel:      "i-0abc1234",
					StackName:     "prod",
					OldStackName:  "$unmanaged",
					Operation:     apimodel.OperationUpdate,
					State:         "NotStarted",
				},
			},
		},
	}

	result, err := RenderSimulation(&simulation)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "update resource web-server")
	assert.Contains(t, result, `change label from "i-0abc1234" to "web-server"`)
	assert.Contains(t, result, "from unmanaged to prod", "stack-move sub-line on the parent")
	assert.NotContains(t, result, "put resource under management",
		"the redundant management message was dropped — the stack-move sub-line covers it")
}

// RFC-0041: a replace (CreateOnly property changed) that coincides with a
// rename. The replace pair is delete(old-label) + create(new-label) with the
// same GroupID. The renderer must:
//   - show the resource at the NEW label (its post-apply identity), not the
//     old one;
//   - surface the label rename as a `change label from "<old>" to "<new>"`
//     line at the parent level (replace doesn't use `by doing the following:`,
//     it uses `because these immutable properties changed:`);
//   - still show the immutable-property-change block.
func TestRenderSimulation_ReplaceWithRename(t *testing.T) {
	createOnlyPatch := json.RawMessage(`[{"op":"replace","path":"/CidrBlock","value":"172.32.0.0/16"}]`)
	oldProps := json.RawMessage(`{"CidrBlock":"172.31.0.0/16"}`)
	newProps := json.RawMessage(`{"CidrBlock":"172.32.0.0/16"}`)
	groupID := util.NewID()

	simulation := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "replace-with-rename-id",
			Command:   "apply",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:      util.NewID(),
					ResourceType:    "AWS::EC2::VPC",
					ResourceLabel:   "vpc-008eef40942ac586b",
					StackName:       "demo",
					Operation:       apimodel.OperationDelete,
					State:           "NotStarted",
					GroupID:         groupID,
					Properties:      oldProps,
					CreateOnlyPatch: createOnlyPatch,
				},
				{
					ResourceID:    util.NewID(),
					ResourceType:  "AWS::EC2::VPC",
					ResourceLabel: "managed-vpc",
					StackName:     "demo",
					Operation:     apimodel.OperationCreate,
					State:         "NotStarted",
					GroupID:       groupID,
					Properties:    newProps,
				},
			},
		},
	}

	result, err := RenderSimulation(&simulation)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "replace resource managed-vpc",
		"replace must show the resource at its NEW label")
	assert.NotContains(t, result, "replace resource vpc-008eef40942ac586b",
		"old label must not be the display name")
	assert.Contains(t, result, `change label from "vpc-008eef40942ac586b" to "managed-vpc"`,
		"label rename surfaces as a dedicated line for replace ops")
	assert.Contains(t, result, "because these immutable properties changed:",
		"the immutable-property-change block still renders")
	assert.Contains(t, result, "CidrBlock", "the CreateOnly property change is rendered")

	// RFC-0041 ordering: the `because these immutable properties changed:`
	// block is the replace's reason, the label rename is incidental. Render
	// the reason first, the incidental change second so the operator reads
	// cause-before-effect.
	becauseIdx := strings.Index(result, "because these immutable properties changed:")
	renameIdx := strings.Index(result, `change label from "vpc-008eef40942ac586b" to "managed-vpc"`)
	assert.True(t, becauseIdx >= 0 && renameIdx > becauseIdx,
		"replace ordering: `because ...` must come before `change label ...` (becauseIdx=%d, renameIdx=%d)", becauseIdx, renameIdx)
}

// No rename + property change -> existing UPDATE verb, no `change label`
// entry. Regression guard against the rename detection misfiring.
func TestRenderSimulation_PlainUpdate_NoOldLabel(t *testing.T) {
	patch := json.RawMessage(`[{"op":"replace","path":"/InstanceType","value":"t3.medium"}]`)

	simulation := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "plain-update-id",
			Command:   "apply",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:    util.NewID(),
					ResourceType:  "FakeAWS::EC2::Instance",
					ResourceLabel: "web-server",
					StackName:     "prod",
					OldStackName:  "prod",
					Operation:     apimodel.OperationUpdate,
					State:         "NotStarted",
					PatchDocument: patch,
				},
			},
		},
	}

	result, err := RenderSimulation(&simulation)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "update resource web-server")
	assert.NotContains(t, result, "change label")
	assert.NotContains(t, result, "label:")
}
