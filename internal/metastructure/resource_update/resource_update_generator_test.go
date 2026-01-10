// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestTranslateFormaeReferencesToKsuid(t *testing.T) {
	ds, _ := GetDeps(t)

	t.Run("translates resolvable objects to KSUID refs", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{},
			Resources: []pkgmodel.Resource{
				{
					Label:  "vpc",
					Type:   "AWS::EC2::VPC",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"CidrBlock": "10.0.0.0/16"
					}`),
				},
				{
					Label:  "subnet",
					Type:   "AWS::EC2::Subnet",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
				},
			},
		}

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		var subnetProps map[string]any
		err = json.Unmarshal(forma.Resources[1].Properties, &subnetProps)
		require.NoError(t, err)

		vpcIdRef := subnetProps["VpcId"].(map[string]any)
		assert.Contains(t, vpcIdRef, "$ref")

		refValue := vpcIdRef["$ref"].(string)
		assert.True(t, strings.HasPrefix(refValue, "formae://"))
		assert.Contains(t, refValue, "#/VpcId")
		assert.Contains(t, refValue, forma.Resources[0].Ksuid)
	})

	t.Run("translates intra-forma references to KSUIDs", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{},
			Resources: []pkgmodel.Resource{
				{
					Label:  "vpc",
					Type:   "AWS::EC2::VPC",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"CidrBlock": "10.0.0.0/16"
					}`),
				},
				{
					Label:  "subnet",
					Type:   "AWS::EC2::Subnet",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
				},
			},
		}

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		assert.NotEmpty(t, forma.Resources[0].Ksuid, "VPC should have KSUID")
		assert.NotEmpty(t, forma.Resources[1].Ksuid, "Subnet should have KSUID")

		var subnetProps map[string]any
		err = json.Unmarshal(forma.Resources[1].Properties, &subnetProps)
		require.NoError(t, err)

		vpcIdRef := subnetProps["VpcId"].(map[string]any)["$ref"].(string)

		assert.True(t, strings.HasPrefix(vpcIdRef, "formae://"), "Should start with formae://")
		assert.Contains(t, vpcIdRef, "#/VpcId", "Should end with #/VpcId")

		assert.NotContains(t, vpcIdRef, "vpc.ec2.aws", "Should not contain triplet format")
		assert.NotContains(t, vpcIdRef, "test-stack", "Should not contain stack name")
		assert.NotContains(t, vpcIdRef, "/vpc#", "Should not contain resource label")

		vpcKsuid := forma.Resources[0].Ksuid
		expectedRef := "formae://" + vpcKsuid + "#/VpcId"
		assert.Equal(t, expectedRef, vpcIdRef, "Should reference VPC by its KSUID")
	})

	t.Run("handles resources with no references unchanged", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{},
			Resources: []pkgmodel.Resource{
				{
					Label:  "vpc",
					Type:   "AWS::EC2::VPC",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"CidrBlock": "10.0.0.0/16",
						"StaticValue": "no-refs-here"
					}`),
				},
			},
		}

		originalProperties := string(forma.Resources[0].Properties)

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		var originalProps, translatedProps map[string]any
		json.Unmarshal([]byte(originalProperties), &originalProps)
		json.Unmarshal(forma.Resources[0].Properties, &translatedProps)

		assert.Equal(t, originalProps["CidrBlock"], translatedProps["CidrBlock"])
		assert.Equal(t, originalProps["StaticValue"], translatedProps["StaticValue"])
		assert.NotEmpty(t, forma.Resources[0].Ksuid)
	})

	t.Run("preserves existing KSUIDs", func(t *testing.T) {
		existingKsuid := "01HPX7J0X16Q4WKZV3K4XGXFP"
		forma := &pkgmodel.Forma{
			Resources: []pkgmodel.Resource{
				{
					Label:      "vpc",
					Type:       "AWS::EC2::VPC",
					Stack:      "test-stack",
					Target:     "aws-target",
					Ksuid:      existingKsuid, // Already has KSUID
					Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				},
			},
		}

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)
		assert.Equal(t, existingKsuid, forma.Resources[0].Ksuid)
	})

	t.Run("handles multiple references in same resource", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{},
			Resources: []pkgmodel.Resource{
				{
					Label: "vpc", Type: "AWS::EC2::VPC", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				},
				{
					Label: "sg", Type: "AWS::EC2::SecurityGroup", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{"GroupDescription": "Test SG"}`),
				},
				{
					Label: "subnet", Type: "AWS::EC2::Subnet", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{"CidrBlock": "10.0.1.0/24"}`),
				},
				{
					Label: "instance", Type: "AWS::EC2::Instance", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{
						"SecurityGroupIds": [{
							"$res": true,
							"$label": "sg",
							"$type": "AWS::EC2::SecurityGroup",
							"$stack": "test-stack",
							"$property": "GroupId"
						}],
						"SubnetId": {
							"$res": true,
							"$label": "subnet",
							"$type": "AWS::EC2::Subnet",
							"$stack": "test-stack",
							"$property": "SubnetId"
						},
						"VpcId": {
							"$res": true,
							"$label": "vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						}
					}`),
				},
			},
		}

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		// Verify all 3 different references were translated
		var instanceProps map[string]any
		json.Unmarshal(forma.Resources[3].Properties, &instanceProps)

		sgRef := instanceProps["SecurityGroupIds"].([]any)[0].(map[string]any)["$ref"].(string)
		subnetRef := instanceProps["SubnetId"].(map[string]any)["$ref"].(string)
		vpcRef := instanceProps["VpcId"].(map[string]any)["$ref"].(string)

		// All should be KSUID format now
		assert.Contains(t, sgRef, forma.Resources[1].Ksuid)
		assert.Contains(t, subnetRef, forma.Resources[2].Ksuid)
		assert.Contains(t, vpcRef, forma.Resources[0].Ksuid)
		assert.True(t, strings.HasPrefix(sgRef, "formae://"))
		assert.True(t, strings.HasPrefix(subnetRef, "formae://"))
		assert.True(t, strings.HasPrefix(vpcRef, "formae://"))
	})

	t.Run("handles same reference multiple times", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{},
			Resources: []pkgmodel.Resource{
				{
					Label: "vpc", Type: "AWS::EC2::VPC", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				},
				{
					Label: "resource-with-duplicate-refs", Type: "AWS::Custom::Resource", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{
						"PrimaryVpc": {
							"$res": true,
							"$label": "vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						},
						"SecondaryVpc": {
							"$res": true,
							"$label": "vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						},
						"Config": {
							"VpcId": {
								"$res": true,
								"$label": "vpc",
								"$type": "AWS::EC2::VPC",
								"$stack": "test-stack",
								"$property": "VpcId"
							}
						}
					}`),
				},
			},
		}

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		var resourceProps map[string]any
		json.Unmarshal(forma.Resources[1].Properties, &resourceProps)

		expectedKsuidRef := "formae://" + forma.Resources[0].Ksuid + "#/VpcId"

		primaryRef := resourceProps["PrimaryVpc"].(map[string]any)["$ref"].(string)
		secondaryRef := resourceProps["SecondaryVpc"].(map[string]any)["$ref"].(string)
		configRef := resourceProps["Config"].(map[string]any)["VpcId"].(map[string]any)["$ref"].(string)

		assert.Equal(t, expectedKsuidRef, primaryRef)
		assert.Equal(t, expectedKsuidRef, secondaryRef)
		assert.Equal(t, expectedKsuidRef, configRef)
	})

	t.Run("handles external references", func(t *testing.T) {
		// First store an external resource
		externalVpc := pkgmodel.Resource{
			Label: "external-vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "external-stack",
			Ksuid: "external-ksuid-789",
		}
		_, err := ds.StoreResource(&externalVpc, "external-command")
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:  "subnet",
					Type:   "AWS::EC2::Subnet",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "external-vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "external-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
				},
			},
		}

		_, err = translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		var subnetProps map[string]any
		err = json.Unmarshal(forma.Resources[0].Properties, &subnetProps)
		require.NoError(t, err)

		vpcIdRef := subnetProps["VpcId"].(map[string]any)["$ref"].(string)
		expectedRef := "formae://external-ksuid-789#/VpcId"
		assert.Equal(t, expectedRef, vpcIdRef)
	})

	t.Run("handles nested resolvable objects", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label: "vpc", Type: "AWS::EC2::VPC", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				},
				{
					Label: "resource-with-nested-refs", Type: "AWS::Custom::Resource", Stack: "test-stack", Target: "aws-target",
					Properties: json.RawMessage(`{
						"Config": {
							"Network": {
								"VpcId": {
									"$res": true,
									"$label": "vpc",
									"$type": "AWS::EC2::VPC",
									"$stack": "test-stack",
									"$property": "VpcId"
								}
							}
						}
					}`),
				},
			},
		}

		_, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		var resourceProps map[string]any
		json.Unmarshal(forma.Resources[1].Properties, &resourceProps)

		config := resourceProps["Config"].(map[string]any)
		network := config["Network"].(map[string]any)
		vpcIdRef := network["VpcId"].(map[string]any)["$ref"].(string)

		expectedRef := "formae://" + forma.Resources[0].Ksuid + "#/VpcId"
		assert.Equal(t, expectedRef, vpcIdRef)
	})
}

func TestGenerateResourceUpdatesWithTranslation(t *testing.T) {
	ds, _ := GetDeps(t)

	command := pkgmodel.CommandApply
	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{
			{Label: "test-stack"},
		},
		Targets: []pkgmodel.Target{
			{
				Label:     "aws-target",
				Config:    json.RawMessage(`{"Region": "us-west-2"}`),
				Namespace: "aws",
			},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:      "vpc",
				Type:       "AWS::EC2::VPC",
				Stack:      "test-stack",
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
			},
			{
				Label:  "subnet",
				Type:   "AWS::EC2::Subnet",
				Stack:  "test-stack",
				Target: "aws-target",
				Properties: json.RawMessage(`{
					"VpcId": {
						"$res": true,
						"$label": "vpc",
						"$type": "AWS::EC2::VPC",
						"$stack": "test-stack",
						"$property": "VpcId"
					},
					"CidrBlock": "10.0.1.0/24"
				}`),
			},
		},
	}

	updates, err := GenerateResourceUpdates(forma, command, mode, FormaCommandSourceUser, []*pkgmodel.Target{}, ds)
	require.NoError(t, err)
	require.Len(t, updates, 2)

	var subnetUpdate *ResourceUpdate
	for i := range updates {
		if updates[i].DesiredState.Label == "subnet" {
			subnetUpdate = &updates[i]
			break
		}
	}
	require.NotNil(t, subnetUpdate, "Should have subnet update")

	var subnetProps map[string]any
	err = json.Unmarshal(subnetUpdate.DesiredState.Properties, &subnetProps)
	require.NoError(t, err)

	vpcIdRef := subnetProps["VpcId"].(map[string]any)["$ref"].(string)

	assert.True(t, strings.HasPrefix(vpcIdRef, "formae://"))
	assert.Contains(t, vpcIdRef, "#/VpcId")
	assert.NotContains(t, vpcIdRef, "vpc.ec2.aws")
	assert.NotContains(t, vpcIdRef, "test-stack")

	assert.NotEmpty(t, updates[0].DesiredState.Ksuid)
	assert.NotEmpty(t, updates[1].DesiredState.Ksuid)
}

// Update the reference labels tests to use resolvable objects
func TestTranslateFormaeReferencesToKsuid_ReturnsCompleteMapping(t *testing.T) {
	ds, _ := GetDeps(t)

	t.Run("captures current command resources", func(t *testing.T) {
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:      "vpc",
					Type:       "AWS::EC2::VPC",
					Stack:      "test-stack",
					Target:     "aws-target",
					Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				},
				{
					Label:  "subnet",
					Type:   "AWS::EC2::Subnet",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "test-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
				},
			},
		}

		mapping, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		assert.Equal(t, "vpc", mapping[forma.Resources[0].Ksuid])
		assert.Equal(t, "subnet", mapping[forma.Resources[1].Ksuid])
		assert.Len(t, mapping, 2, "Should map both current command resources")
	})

	t.Run("captures external reference labels", func(t *testing.T) {
		externalVpc := pkgmodel.Resource{
			Label: "external-vpc",
			Type:  "AWS::EC2::VPC",
			Stack: "external-stack",
			Ksuid: "external-ksuid-789",
		}
		_, err := ds.StoreResource(&externalVpc, "external-command")
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:  "subnet",
					Type:   "AWS::EC2::Subnet",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "external-vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "external-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
				},
			},
		}

		mapping, err := translateFormaeReferencesToKsuid(forma, ds)
		require.NoError(t, err)

		assert.Equal(t, "subnet", mapping[forma.Resources[0].Ksuid])
		assert.Equal(t, "external-vpc", mapping["external-ksuid-789"])
		assert.Len(t, mapping, 2, "Should have mappings for both current and external resources")
	})
}

func TestGenerateResourceUpdates_PopulatesReferenceLabels(t *testing.T) {
	ds, _ := GetDeps(t)

	externalResource := pkgmodel.Resource{
		Label: "shared-vpc",
		Type:  "AWS::EC2::VPC",
		Stack: "shared-stack",
		Ksuid: "shared-ksuid-999",
	}
	_, err := ds.StoreResource(&externalResource, "previous-command")
	require.NoError(t, err)

	command := pkgmodel.CommandApply
	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{
				Label:     "aws-target",
				Config:    json.RawMessage(`{"Region": "us-west-2"}`),
				Namespace: "aws",
			},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:      "new-vpc",
				Type:       "AWS::EC2::VPC",
				Stack:      "test-stack",
				Target:     "aws-target",
				Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
			},
			{
				Label:  "subnet",
				Type:   "AWS::EC2::Subnet",
				Stack:  "test-stack",
				Target: "aws-target",
				Properties: json.RawMessage(`{
					"VpcId": {
						"$res": true,
						"$label": "new-vpc",
						"$type": "AWS::EC2::VPC",
						"$stack": "test-stack",
						"$property": "VpcId"
					},
					"SharedVpcId": {
						"$res": true,
						"$label": "shared-vpc",
						"$type": "AWS::EC2::VPC",
						"$stack": "shared-stack",
						"$property": "VpcId"
					},
					"CidrBlock": "10.0.1.0/24"
				}`),
			},
		},
	}

	updates, err := GenerateResourceUpdates(forma, command, mode, FormaCommandSourceUser, []*pkgmodel.Target{}, ds)
	require.NoError(t, err)
	require.Len(t, updates, 2)

	for _, update := range updates {
		assert.NotNil(t, update.ReferenceLabels, "ReferenceLabels should be populated")
		assert.NotEmpty(t, update.ReferenceLabels, "ReferenceLabels should not be empty")
	}

	var vpcUpdate, subnetUpdate *ResourceUpdate
	for i := range updates {
		if updates[i].DesiredState.Label == "new-vpc" {
			vpcUpdate = &updates[i]
		} else if updates[i].DesiredState.Label == "subnet" {
			subnetUpdate = &updates[i]
		}
	}
	require.NotNil(t, vpcUpdate, "Should have VPC update")
	require.NotNil(t, subnetUpdate, "Should have subnet update")

	expectedLabels := map[string]string{
		vpcUpdate.DesiredState.Ksuid:    "new-vpc",
		subnetUpdate.DesiredState.Ksuid: "subnet",
		"shared-ksuid-999":          "shared-vpc",
	}

	assert.Equal(t, expectedLabels, vpcUpdate.ReferenceLabels, "VPC update should have complete mapping")
	assert.Equal(t, expectedLabels, subnetUpdate.ReferenceLabels, "Subnet update should have complete mapping")
}

func TestGenerateResourceUpdates_ReferenceLabelsEdge(t *testing.T) {
	ds, _ := GetDeps(t)

	t.Run("handles resources with no references", func(t *testing.T) {
		command := pkgmodel.CommandApply
		mode := pkgmodel.FormaApplyModeReconcile
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Targets: []pkgmodel.Target{
				{Label: "aws-target", Config: json.RawMessage(`{}`), Namespace: "aws"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "standalone-vpc",
					Type:       "AWS::EC2::VPC",
					Stack:      "test-stack",
					Target:     "aws-target",
					Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
				},
			},
		}

		updates, err := GenerateResourceUpdates(forma, command, mode, FormaCommandSourceUser, []*pkgmodel.Target{}, ds)
		require.NoError(t, err)
		require.Len(t, updates, 1)

		assert.NotNil(t, updates[0].ReferenceLabels)
		assert.Equal(t, "standalone-vpc", updates[0].ReferenceLabels[updates[0].DesiredState.Ksuid])
		assert.Len(t, updates[0].ReferenceLabels, 1)
	})

	t.Run("handles unresolvable external references gracefully", func(t *testing.T) {
		command := pkgmodel.CommandApply
		mode := pkgmodel.FormaApplyModeReconcile
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Targets: []pkgmodel.Target{
				{Label: "aws-target", Config: json.RawMessage(`{}`), Namespace: "aws"},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "subnet-with-bad-ref",
					Type:   "AWS::EC2::Subnet",
					Stack:  "test-stack",
					Target: "aws-target",
					Properties: json.RawMessage(`{
						"VpcId": {
							"$res": true,
							"$label": "nonexistent-vpc",
							"$type": "AWS::EC2::VPC",
							"$stack": "nonexistent-stack",
							"$property": "VpcId"
						},
						"CidrBlock": "10.0.1.0/24"
					}`),
				},
			},
		}

		updates, err := GenerateResourceUpdates(forma, command, mode, FormaCommandSourceUser, []*pkgmodel.Target{}, ds)
		require.NoError(t, err)
		require.Len(t, updates, 1)

		assert.NotNil(t, updates[0].ReferenceLabels)
		assert.Equal(t, "subnet-with-bad-ref", updates[0].ReferenceLabels[updates[0].DesiredState.Ksuid])

		var subnetProps map[string]any
		err = json.Unmarshal(updates[0].DesiredState.Properties, &subnetProps)
		require.NoError(t, err)
		vpcId := subnetProps["VpcId"].(map[string]any)
		assert.Equal(t, true, vpcId["$res"])
		assert.Equal(t, "nonexistent-vpc", vpcId["$label"])
	})
}

func TestGenerateResourceUpdates_TargetValidation(t *testing.T) {
	ds, _ := GetDeps(t)

	baseCommand := pkgmodel.CommandApply
	baseMode := pkgmodel.FormaApplyModeReconcile
	baseForma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:  "test-instance",
				Type:   "AWS::EC2::Instance",
				Stack:  "test-stack",
				Target: "test-target",
			},
		},
	}

	t.Run("allows matching target configurations", func(t *testing.T) {
		command := baseCommand
		mode := baseMode
		forma := *baseForma
		forma.Targets = []pkgmodel.Target{
			{
				Label:     "test-target",
				Config:    json.RawMessage(`{"Region": "us-west-2", "AccountId": "123456789"}`),
				Namespace: "aws",
			},
		}

		existingTargets := []*pkgmodel.Target{
			{
				Label:     "test-target",
				Config:    json.RawMessage(`{"Region": "us-west-2", "AccountId": "123456789"}`),
				Namespace: "aws",
			},
		}

		updates, err := GenerateResourceUpdates(&forma, command, mode, FormaCommandSourceUser, existingTargets, ds)
		assert.NoError(t, err)
		assert.Len(t, updates, 1)
		assert.Equal(t, "test-target", updates[0].ResourceTarget.Label)
	})

	t.Run("rejects mismatched target namespace", func(t *testing.T) {
		command := baseCommand
		mode := baseMode
		forma := *baseForma
		forma.Targets = []pkgmodel.Target{
			{
				Label:     "test-target",
				Config:    json.RawMessage(`{"Region": "us-west-2"}`),
				Namespace: "aws", // Different namespace
			},
		}

		existingTargets := []*pkgmodel.Target{
			{
				Label:     "test-target",
				Config:    json.RawMessage(`{"Region": "us-west-2"}`),
				Namespace: "gcp", // Different namespace
			},
		}

		updates, err := GenerateResourceUpdates(&forma, command, mode, FormaCommandSourceUser, existingTargets, ds)
		assert.Error(t, err)
		assert.Nil(t, updates)
	})

	t.Run("rejects mismatched target config", func(t *testing.T) {
		command := baseCommand
		mode := baseMode
		forma := *baseForma
		forma.Targets = []pkgmodel.Target{
			{
				Label:     "test-target",
				Config:    json.RawMessage(`{"Region": "us-west-2", "AccountId": "123456789"}`),
				Namespace: "aws",
			},
		}

		existingTargets := []*pkgmodel.Target{
			{
				Label:     "test-target",
				Config:    json.RawMessage(`{"Region": "us-east-1", "AccountId": "987654321"}`), // Different config
				Namespace: "aws",
			},
		}

		updates, err := GenerateResourceUpdates(&forma, command, mode, FormaCommandSourceUser, existingTargets, ds)
		assert.Error(t, err)
		assert.Nil(t, updates)
	})

	t.Run("allows new targets to be added", func(t *testing.T) {
		command := baseCommand
		mode := baseMode
		forma := *baseForma
		forma.Targets = []pkgmodel.Target{
			{
				Label:     "new-target",
				Config:    json.RawMessage(`{"Region": "us-west-2"}`),
				Namespace: "aws",
			},
		}
		forma.Resources[0].Target = "new-target"

		existingTargets := []*pkgmodel.Target{} // No existing targets

		updates, err := GenerateResourceUpdates(&forma, command, mode, FormaCommandSourceUser, existingTargets, ds)
		assert.NoError(t, err)
		assert.Len(t, updates, 1)
		assert.Equal(t, "new-target", updates[0].ResourceTarget.Label)
	})
}

func TestGenerateResourceUpdatesForDestroy_SameLabel_DifferentTypes(t *testing.T) {
	ds, _ := GetDeps(t)

	// Same label, different types
	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:  "my-resource",
				Type:   "AWS::ApiGateway::RestApi",
				Stack:  "test-stack",
				Target: "aws-target",
				Ksuid:  "ksuid-api-gateway",
			},
			{
				Label:  "my-resource",
				Type:   "AWS::CloudFront::Distribution",
				Stack:  "test-stack",
				Target: "aws-target",
				Ksuid:  "ksuid-cloudfront",
			},
		},
	}

	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:  "my-resource",
				Type:   "AWS::ApiGateway::RestApi",
				Stack:  "test-stack",
				Target: "aws-target",
			},
			{
				Label:  "my-resource",
				Type:   "AWS::CloudFront::Distribution",
				Stack:  "test-stack",
				Target: "aws-target",
			},
		},
	}

	targetMap := map[string]*pkgmodel.Target{
		"aws-target": {Label: "aws-target", Namespace: "AWS"},
	}

	updates, err := generateResourceUpdatesForDestroy(forma, FormaCommandSourceUser, targetMap, ds)
	require.NoError(t, err)

	// Should yield 2 delete ops
	assert.Len(t, updates, 2, "Should create delete operations for both resource types")

	types := make(map[string]bool)
	for _, update := range updates {
		assert.Equal(t, OperationDelete, update.Operation)
		assert.Equal(t, "my-resource", update.DesiredState.Label)
		types[update.DesiredState.Type] = true
	}

	assert.True(t, types["AWS::ApiGateway::RestApi"], "Should have API Gateway delete")
	assert.True(t, types["AWS::CloudFront::Distribution"], "Should have CloudFront delete")
}

func TestGenerateResourceUpdatesForSync_SameLabel_DifferentTypes(t *testing.T) {
	ds, _ := GetDeps(t)

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:  "shared-name",
				Type:   "AWS::S3::Bucket",
				Stack:  "test-stack",
				Target: "aws-target",
				Ksuid:  "ksuid-bucket",
			},
			{
				Label:  "shared-name",
				Type:   "AWS::S3::BucketPolicy",
				Stack:  "test-stack",
				Target: "aws-target",
				Ksuid:  "ksuid-policy",
			},
		},
	}

	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:  "shared-name",
				Type:   "AWS::S3::Bucket",
				Stack:  "test-stack",
				Target: "aws-target",
			},
			{
				Label:  "shared-name",
				Type:   "AWS::S3::BucketPolicy",
				Stack:  "test-stack",
				Target: "aws-target",
			},
		},
	}

	targetMap := map[string]*pkgmodel.Target{
		"aws-target": {Label: "aws-target", Namespace: "AWS"},
	}

	updates, err := generateResourceUpdatesForSync(forma, FormaCommandSourceUser, targetMap, ds)
	require.NoError(t, err)

	assert.Len(t, updates, 2, "Should create sync operations for both resource types")

	types := make(map[string]bool)
	for _, update := range updates {
		assert.Equal(t, OperationRead, update.Operation)
		assert.Equal(t, "shared-name", update.DesiredState.Label)
		types[update.DesiredState.Type] = true
	}

	assert.True(t, types["AWS::S3::Bucket"], "Should have Bucket sync")
	assert.True(t, types["AWS::S3::BucketPolicy"], "Should have BucketPolicy sync")
}

func TestFindDependencyUpdates_SameLabel_DifferentTypes(t *testing.T) {
	vpcKsuid := pkgmodel.NewFormaeURI(util.NewID(), "")
	apiGatewayKsuid := pkgmodel.NewFormaeURI(util.NewID(), "")
	cloudFrontKsuid := pkgmodel.NewFormaeURI(util.NewID(), "")

	// Two resources with same label but different types, both depend on VPC
	allStacks := []*pkgmodel.Forma{
		{
			Resources: []pkgmodel.Resource{
				{
					Label: "vpc",
					Type:  "AWS::EC2::VPC",
					Stack: "test-stack",
					Ksuid: vpcKsuid.KSUID(),
				},
				{
					Label:      "my-resource",
					Type:       "AWS::ApiGateway::RestApi",
					Stack:      "test-stack",
					Ksuid:      apiGatewayKsuid.KSUID(),
					Properties: json.RawMessage(fmt.Sprintf(`{"VpcId": {"$ref": "%s#/VpcId"}}`, vpcKsuid)),
				},
				{
					Label:      "my-resource",
					Type:       "AWS::CloudFront::Distribution",
					Stack:      "test-stack",
					Ksuid:      cloudFrontKsuid.KSUID(),
					Properties: json.RawMessage(fmt.Sprintf(`{"OriginVpcId": {"$ref": "%s#/VpcId"}}`, vpcKsuid)),
				},
			},
		},
	}

	allDeleteUpdates := []ResourceUpdate{
		{
			DesiredState: pkgmodel.Resource{
				Label: "my-resource",
				Type:  "AWS::ApiGateway::RestApi",
				Stack: "test-stack",
				Ksuid: apiGatewayKsuid.KSUID(),
			},
			Operation: OperationDelete,
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "my-resource",
				Type:  "AWS::CloudFront::Distribution",
				Stack: "test-stack",
				Ksuid: cloudFrontKsuid.KSUID(),
			},
			Operation: OperationDelete,
		},
		{
			DesiredState: pkgmodel.Resource{
				Label: "vpc",
				Type:  "AWS::EC2::VPC",
				Stack: "test-stack",
				Ksuid: vpcKsuid.KSUID(),
			},
			Operation: OperationDelete,
		},
	}

	targetMap := map[string]*pkgmodel.Target{
		"aws-target": {Label: "aws-target", Namespace: "AWS"},
	}

	dependencyDeletes := findDependencyUpdates(allDeleteUpdates, allStacks, targetMap, FormaCommandSourceUser)

	assert.Len(t, dependencyDeletes, 0, "Should not create duplicates when resources with same label but different types are already being deleted")
}

func TestGenerateResourceUpdatesForApply_SameLabelDifferentTypes_ReplaceNotGenerated(t *testing.T) {
	ds, _ := GetDeps(t)

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:  "my-resource",
				Type:   "AWS::EC2::Subnet",
				Stack:  "test-stack",
				Target: "aws-target",
				Ksuid:  "ksuid-subnet",
			},
		},
	}

	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// New forma changes the type to VPC (implicit delete subnet, create VPC)
	mode := pkgmodel.FormaApplyModeReconcile
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "aws-target", Namespace: "AWS"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:  "my-resource",
				Type:   "AWS::EC2::VPC",
				Stack:  "test-stack",
				Target: "aws-target",
			},
		},
	}

	targetMap := map[string]*pkgmodel.Target{
		"aws-target": {Label: "aws-target", Namespace: "AWS"},
	}

	updates, err := generateResourceUpdatesForApply(forma, mode, FormaCommandSourceUser, targetMap, ds)
	require.NoError(t, err)

	assert.Len(t, updates, 2, "Should have delete for old type and create for new type")

	var deleteOp, createOp *ResourceUpdate
	for i := range updates {
		if updates[i].Operation == OperationDelete {
			deleteOp = &updates[i]
		} else if updates[i].Operation == OperationCreate {
			createOp = &updates[i]
		}
	}

	require.NotNil(t, deleteOp, "Should have delete operation")
	require.NotNil(t, createOp, "Should have create operation")

	assert.Equal(t, "my-resource", deleteOp.DesiredState.Label)
	assert.Equal(t, "AWS::EC2::Subnet", deleteOp.DesiredState.Type)

	assert.Equal(t, "my-resource", createOp.DesiredState.Label)
	assert.Equal(t, "AWS::EC2::VPC", createOp.DesiredState.Type)

	// They should be treated as independent, not as a replacement
	assert.Empty(t, deleteOp.GroupID, "Delete should not be grouped (not a replacement)")
	assert.Empty(t, createOp.GroupID, "Create should not be grouped (not a replacement)")
}
