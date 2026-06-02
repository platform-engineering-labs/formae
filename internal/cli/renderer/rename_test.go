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
// The renderer shows the normal "update" verb and adds a highlighted
// "label: <old> -> <new>" sub-line. There is no separate RENAME verb because
// a rename is part of OperationUpdate; the verb stays consistent with how
// the engine actually models the change.
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
	assert.Contains(t, result, "label: web-server -> app-server", "label change highlighted as a sub-line")
	assert.NotContains(t, result, "RENAME", "no dedicated RENAME verb")
	assert.NotContains(t, result, "[RENAME+UPDATE]", "no combined verb either")
}

// Label rename + property change in same update — verb stays "update";
// label change shows on its own sub-line; property patch listed below.
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

	assert.Contains(t, result, "update resource app-server", "verb stays as update")
	assert.Contains(t, result, "label: web-server -> app-server", "label change shown")
	assert.Contains(t, result, "by doing the following:", "patch listing follows")
	assert.NotContains(t, result, "RENAME")
}

// RFC-0041 edge case: bringing under management AND renaming.
// Both transitions surface — label change AND "from unmanaged to prod".
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

	assert.Contains(t, result, "update resource web-server", "verb stays as update")
	assert.Contains(t, result, "label: i-0abc1234 -> web-server", "label change shown")
	assert.Contains(t, result, "from unmanaged to prod", "stack move from unmanaged shown")
	assert.NotContains(t, result, "RENAME")
}

// No rename + property change -> existing UPDATE verb unchanged, no label
// sub-line. Regression guard against the rename detection misfiring.
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
	assert.NotContains(t, result, "label:")
	assert.NotContains(t, result, "RENAME")
}
