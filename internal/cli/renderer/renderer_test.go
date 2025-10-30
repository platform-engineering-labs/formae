// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestFormatHumanReadableStatus(t *testing.T) {
	status := apimodel.Command{
		CommandID: "test-command-id",
		Command:   "apply",
		State:     "Failed",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::VPC",
				ResourceLabel: "test-vpc",
				StackName:     "test-stack",
				Operation:     "update",
				State:         "Failed",
				ErrorMessage:  "test status message",
			},
		},
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{status}})
	assert.NoError(t, err)

	// When run with make test-all, color escape sequences interfere with string assertions
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "apply command with ID test-command-id: Failed")
	assert.Contains(t, result, "total duration:")
	assert.Contains(t, result, "update resource test-vpc: Failed")
	assert.Contains(t, result, "of type AWS::EC2::VPC")
	assert.Contains(t, result, "in stack test-stack")
	assert.Contains(t, result, "reason for failure: test status message")
}

func TestFormatHumanReadableStatus_MixedStates(t *testing.T) {
	status := apimodel.Command{
		CommandID: "test-mixed-id",
		Command:   "apply",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::Subnet",
				ResourceLabel: "test-subnet",
				StackName:     "test-stack",
				Operation:     "update",
				State:         "Success",
				Duration:      5,
			},
			{
				ResourceID:     util.NewID(),
				ResourceType:   "AWS::EC2::InternetGateway",
				ResourceLabel:  "test-igw",
				StackName:      "test-stack",
				Operation:      "delete",
				State:          "Failed",
				Duration:       8,
				ErrorMessage:   "test status message",
				MaxAttempts:    1,
				CurrentAttempt: 2,
			},
		},
		State: "Failed",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{status}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "apply command with ID test-mixed-id: Failed")
	assert.Contains(t, result, "total duration:")
	assert.Contains(t, result, "update resource test-subnet: Success")
	assert.Contains(t, result, "delete resource test-igw: Failed")
	assert.Contains(t, result, "from stack test-stack")
	assert.Contains(t, result, "of type AWS::EC2::InternetGateway")
	assert.Contains(t, result, "reason for failure: test status message")
}

func TestFormatHumanReadableStatus_Create(t *testing.T) {
	status := apimodel.Command{
		CommandID: "test-replace-id",
		Command:   "apply",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::VPC",
				ResourceLabel: "test-vpc",
				StackName:     "test-stack",
				Operation:     "create",
				State:         "Success",
				Duration:      8,
			},
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::Subnet",
				ResourceLabel: "test-subnet",
				StackName:     "test-stack",
				Operation:     "create",
				State:         "Success",
				Duration:      8,
			},
		},
		State: "Success",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{status}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "apply command with ID test-replace-id: Success")
	assert.Contains(t, result, "create resource test-subnet: Success")
	assert.Contains(t, result, "of type AWS::EC2::Subnet")
}

func TestFormatHumanReadableStatus_Replacement(t *testing.T) {
	status := apimodel.Command{
		CommandID: "test-replace-id",
		Command:   "apply",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::Subnet",
				ResourceLabel: "test-subnet",
				StackName:     "test-stack",
				Operation:     "delete",
				State:         "Success",
				Duration:      8,
				GroupID:       "replace-group-1",
			},
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::Subnet",
				ResourceLabel: "test-subnet",
				StackName:     "test-stack",
				Operation:     "create",
				State:         "Success",
				Duration:      8,
				GroupID:       "replace-group-1",
			},
		},
		State: "Success",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{status}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "apply command with ID test-replace-id: Success")
	assert.Contains(t, result, "replace resource test-subnet: Success")
	assert.Contains(t, result, "of type AWS::EC2::Subnet")

	// Should show the operation type as "replace" for the group
	// i.e. It should not show "delete" and "create" separately
	assert.NotContains(t, result, "delete resource test-subnet")
	assert.NotContains(t, result, "create resource test-subnet")
}

func TestFormatHumanReadableStatus_UnmanagedMigration(t *testing.T) {
	status := apimodel.Command{
		CommandID: "test-unmanaged-id",
		Command:   "apply",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::VPC",
				ResourceLabel: "test-vpc",
				StackName:     "my-stack",
				OldStackName:  "$unmanaged",
				Operation:     "update",
				State:         "Success",
				Duration:      5,
			},
		},
		State: "Success",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{status}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "update resource test-vpc: Success")
	assert.Contains(t, result, "from unmanaged to my-stack")
	assert.NotContains(t, result, "in stack my-stack")
}

func TestRenderSimulation_DuplicateLabels(t *testing.T) {
	simulation := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "test-duplicate-labels",
			Command:   "apply",
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:    util.NewID(),
					ResourceType:  "AWS::EC2::Subnet",
					ResourceLabel: "common-label",
					StackName:     "test-stack",
					Operation:     "create",
				},
				{
					ResourceID:    util.NewID(),
					ResourceType:  "AWS::EC2::RouteTable",
					ResourceLabel: "common-label",
					StackName:     "test-stack",
					Operation:     "create",
				},
				{
					ResourceID:    util.NewID(),
					ResourceType:  "AWS::EC2::SecurityGroup",
					ResourceLabel: "common-label",
					StackName:     "test-stack",
					Operation:     "create",
				},
			},
		},
	}

	result, err := RenderSimulation(&simulation)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "create resource common-label")
	assert.Contains(t, result, "of type AWS::EC2::Subnet")
	assert.Contains(t, result, "of type AWS::EC2::RouteTable")
	assert.Contains(t, result, "of type AWS::EC2::SecurityGroup")

	assert.Condition(t, func() bool {
		cnt := 0
		substr := "common-label"
		for i := 0; i < len(result); {
			idx := strings.Index(result[i:], substr)
			if idx == -1 {
				break
			}
			cnt++
			i += idx + len(substr)
		}
		return cnt == 3
	}, "label should appear 3x")
}

func stripAnsiCodes(t *testing.T, s string) string {
	t.Helper()

	ansi := regexp.MustCompile("\x1b\\[[0-9;]*m")
	return ansi.ReplaceAllString(s, "")
}

func TestFormatHumanReadableStatus_TargetCreate(t *testing.T) {
	cmd := apimodel.Command{
		CommandID: "test-target-create",
		Command:   "apply",
		TargetUpdates: []apimodel.TargetUpdate{
			{
				TargetLabel: "new-target",
				Operation:   "create",
				State:       "Success",
			},
		},
		State: "Success",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{cmd}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "apply command with ID test-target-create: Success")
	assert.Contains(t, result, "create target new-target: Success")
}

func TestFormatHumanReadableStatus_TargetUpdate(t *testing.T) {
	cmd := apimodel.Command{
		CommandID: "test-target-update",
		Command:   "apply",
		TargetUpdates: []apimodel.TargetUpdate{
			{
				TargetLabel:  "existing-target",
				Operation:    "update",
				State:        "Success",
				Discoverable: true,
			},
		},
		State: "Success",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{cmd}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "apply command with ID test-target-update: Success")
	assert.Contains(t, result, "update target existing-target to discoverable: Success")
}

func TestFormatHumanReadableStatus_MixedTargetAndResource(t *testing.T) {
	cmd := apimodel.Command{
		CommandID: "test-mixed-updates",
		Command:   "apply",
		TargetUpdates: []apimodel.TargetUpdate{
			{
				TargetLabel: "test-target",
				Operation:   "create",
				State:       "Success",
			},
		},
		ResourceUpdates: []apimodel.ResourceUpdate{
			{
				ResourceID:    util.NewID(),
				ResourceType:  "AWS::EC2::VPC",
				ResourceLabel: "test-vpc",
				StackName:     "test-stack",
				Operation:     "create",
				State:         "Success",
			},
		},
		State: "Success",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{cmd}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "create target test-target: Success")
	assert.Contains(t, result, "create resource test-vpc: Success")
	assert.Contains(t, result, "of type AWS::EC2::VPC")
}

func TestFormatHumanReadableStatus_TargetFailure(t *testing.T) {
	cmd := apimodel.Command{
		CommandID: "test-target-failure",
		Command:   "apply",
		TargetUpdates: []apimodel.TargetUpdate{
			{
				TargetLabel:  "failed-target",
				Operation:    "update",
				State:        "Failed",
				ErrorMessage: "datastore error",
			},
		},
		State: "Failed",
	}

	result, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{cmd}})
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "update target failed-target to not discoverable: Failed")
	assert.Contains(t, result, "reason for failure: datastore error")
}

func TestRenderSimulation_WithTargets(t *testing.T) {
	sim := apimodel.Simulation{
		ChangesRequired: true,
		Command: apimodel.Command{
			CommandID: "test-sim-targets",
			Command:   "apply",
			TargetUpdates: []apimodel.TargetUpdate{
				{
					TargetLabel: "sim-target",
					Operation:   "create",
				},
			},
			ResourceUpdates: []apimodel.ResourceUpdate{
				{
					ResourceID:    util.NewID(),
					ResourceType:  "AWS::EC2::VPC",
					ResourceLabel: "sim-vpc",
					StackName:     "sim-stack",
					Operation:     "create",
				},
			},
		},
	}

	result, err := RenderSimulation(&sim)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "create target sim-target")
	assert.Contains(t, result, "create resource sim-vpc")
}

func TestRenderInventoryTargets(t *testing.T) {
	targets := []*pkgmodel.Target{
		{
			Label:        "prod-us-east-1",
			Namespace:    "AWS",
			Discoverable: true,
			Config:       json.RawMessage(`{"Region":"us-east-1","Type":"prod"}`),
		},
		{
			Label:        "dev-us-west-2",
			Namespace:    "AWS",
			Discoverable: false,
			Config:       json.RawMessage(`{"Region":"us-west-2","Type":"dev"}`),
		},
	}

	result, err := RenderInventoryTargets(targets, 0)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "prod-us-east-1")
	assert.Contains(t, result, "dev-us-west-2")
	assert.Contains(t, result, "AWS")
	assert.Contains(t, result, "true")
	assert.Contains(t, result, "false")
	assert.Contains(t, result, "Region=us-east-1")
	assert.Contains(t, result, "Showing 2 of 2")
}

func TestRenderInventoryTargets_MaxRows(t *testing.T) {
	targets := []*pkgmodel.Target{
		{Label: "target-1", Namespace: "AWS", Discoverable: true, Config: json.RawMessage(`{}`)},
		{Label: "target-2", Namespace: "AWS", Discoverable: true, Config: json.RawMessage(`{}`)},
		{Label: "target-3", Namespace: "AWS", Discoverable: true, Config: json.RawMessage(`{}`)},
	}

	result, err := RenderInventoryTargets(targets, 2)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "target-1")
	assert.Contains(t, result, "target-2")
	assert.NotContains(t, result, "target-3")
	assert.Contains(t, result, "Showing 2 of 3")
	assert.Contains(t, result, "use --max-results 3 to see all")
}

func TestRenderInventoryTargets_Empty(t *testing.T) {
	result, err := RenderInventoryTargets([]*pkgmodel.Target{}, 0)
	assert.NoError(t, err)
	result = stripAnsiCodes(t, result)

	assert.Contains(t, result, "No targets found")
}

func TestFormatTargetConfig_BasicConfig(t *testing.T) {
	config := json.RawMessage(`{"Region":"us-east-1","AccountID":"123456"}`)
	got := formatTargetConfig(config)
	assert.Contains(t, got, "AccountID=123456")
	assert.Contains(t, got, "Region=us-east-1")
}

func TestFormatTargetConfig_FiltersSensitiveFields(t *testing.T) {
	config := json.RawMessage(`{"Region":"us-east-1","AccessKey":"secret","SecretKey":"topsecret","ApiToken":"token123"}`)
	got := formatTargetConfig(config)
	assert.Contains(t, got, "Region=us-east-1")
	assert.NotContains(t, got, "AccessKey")
	assert.NotContains(t, got, "SecretKey")
	assert.NotContains(t, got, "ApiToken")
}

func TestFormatTargetConfig_HandlesVariousSensitiveFieldNames(t *testing.T) {
	config := json.RawMessage(`{"Region":"us-east-1","password":"pass","credential":"cred","MySecretValue":"val"}`)
	got := formatTargetConfig(config)
	assert.Contains(t, got, "Region=us-east-1")
	assert.NotContains(t, got, "password")
	assert.NotContains(t, got, "credential")
	assert.NotContains(t, got, "MySecretValue")
}

func TestFormatTargetConfig_EmptyConfig(t *testing.T) {
	config := json.RawMessage(`{}`)
	got := formatTargetConfig(config)
	assert.Equal(t, "", got)
}

func TestFormatTargetConfig_SortsOutputAlphabetically(t *testing.T) {
	config := json.RawMessage(`{"Zebra":"z","Apple":"a","Banana":"b"}`)
	got := formatTargetConfig(config)
	assert.Contains(t, got, "Apple=a")
	assert.Contains(t, got, "Banana=b")
	assert.Contains(t, got, "Zebra=z")
}
