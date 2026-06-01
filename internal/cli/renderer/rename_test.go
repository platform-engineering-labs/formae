// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// RFC-0041: pure label rename — OldLabel set, no PatchDocument property delta.
// Header verb is RENAME, "renamed from <old>" sub-line present.
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

	assert.Contains(t, result, "RENAME resource app-server")
	assert.Contains(t, result, "renamed from web-server")
	assert.NotContains(t, result, "update resource app-server", "must not render as plain UPDATE")
	assert.NotContains(t, result, "[RENAME+UPDATE]", "pure rename must not show combined verb")
}

// Label rename + property change in same update -> [RENAME+UPDATE] combined verb,
// "renamed from" sub-line, and the property patch listing.
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

	assert.Contains(t, result, "[RENAME+UPDATE] resource app-server")
	assert.Contains(t, result, "renamed from web-server")
	assert.Contains(t, result, "by doing the following:")
}

// RFC-0041 edge case: bringing a resource under management AND renaming it in
// the same apply. The renderer must surface both facts:
//   - "RENAME" verb (because OldLabel is set)
//   - "renamed from <discovery-default>" sub-line
//   - "from unmanaged to <stack>" via the existing formatStackLine
//
// The user sees one entry covering both the import and the rename.
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

	assert.Contains(t, result, "RENAME resource web-server", "rename verb shown")
	assert.Contains(t, result, "renamed from i-0abc1234", "previous label shown")
	assert.Contains(t, result, "from unmanaged to prod", "stack move from unmanaged shown")
	assert.NotContains(t, result, "[RENAME+UPDATE]", "no property delta -> not the combined verb")
}

// No rename + property change -> existing UPDATE verb unchanged.
// Guards against the rename detection misfiring on plain updates.
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
	assert.NotContains(t, result, "RENAME")
	assert.NotContains(t, result, "renamed from")
}
