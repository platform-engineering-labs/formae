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
