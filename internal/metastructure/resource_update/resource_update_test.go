// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestRecordResourceProgress_UpdatesResourceProgress_ForDeleteOperation(t *testing.T) {
	now := time.Now()
	resourceUpdate := &ResourceUpdate{
		Operation: OperationDelete,
		State:     ResourceUpdateStateNotStarted,
	}
	readProgress := &resource.ProgressResult{
		Operation:       resource.OperationRead,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-request-id",
	}

	err := resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *readProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, now, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, now, resourceUpdate.ModifiedTs, 1*time.Second)

	firstDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-delete-request-id",
	}

	startTs := resourceUpdate.StartTs // Capture start timestamp
	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *firstDeleteProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)

	secondDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-delete-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *secondDeleteProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)
}

func TestRecordResourceProgress_UpdatesResourceProgress_ForCreateOperation(t *testing.T) {
	now := time.Now()
	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted,
	}
	firstCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-create-request-id",
	}

	err := resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *firstCreateProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, now, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, now, resourceUpdate.ModifiedTs, 1*time.Second)

	startTs := resourceUpdate.StartTs // Capture start timestamp
	secondCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-create-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *secondCreateProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)
}

func TestRecordResourceProgress_UpdatesResourceProgress_ForUpdateOperation(t *testing.T) {
	now := time.Now()
	resourceUpdate := &ResourceUpdate{
		Operation: OperationUpdate,
		State:     ResourceUpdateStateNotStarted,
	}
	readProgress := &resource.ProgressResult{
		Operation:       resource.OperationRead,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-request-id",
	}

	err := resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *readProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, now, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, now, resourceUpdate.ModifiedTs, 1*time.Second)

	startTs := resourceUpdate.StartTs // Capture start timestamp
	firstUpdateProgress := &resource.ProgressResult{
		Operation:       resource.OperationUpdate,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-update-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *firstUpdateProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)

	secondUpdateProgress := &resource.ProgressResult{
		Operation:       resource.OperationUpdate,
		OperationStatus: resource.OperationStatusFailure,
		RequestID:       "test-second-update-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *secondUpdateProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateFailed, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)
}

func TestRecordResourceProgress_UpdatesResourceProgress_ForReplaceOperation(t *testing.T) {
	now := time.Now()
	resourceUpdate := &ResourceUpdate{
		Operation: OperationReplace,
		State:     ResourceUpdateStateNotStarted,
	}
	readProgress := &resource.ProgressResult{
		Operation:       resource.OperationRead,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-request-id",
	}

	err := resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *readProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, now, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, now, resourceUpdate.ModifiedTs, 1*time.Second)

	startTs := resourceUpdate.StartTs // Capture start timestamp
	firstDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-replace-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *firstDeleteProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)

	secondDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-replace-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *secondDeleteProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)

	firstCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-create-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *firstCreateProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)

	secondCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-create-request-id",
	}

	err = resourceUpdate.RecordProgress(&plugin.TrackedProgress{ProgressResult: *secondCreateProgress, Attempts: 1, MaxAttempts: 4})
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.Equal(t, startTs, resourceUpdate.StartTs) // StartTs should not change
	assert.WithinDuration(t, time.Now(), resourceUpdate.ModifiedTs, 1*time.Second)
}

func Test_mergeRefsPreservingUserRefs_preservesResolvableValues(t *testing.T) {
	distributionKsuid := util.NewID()
	hostedZoneKsuid := util.NewID()

	userProps := fmt.Appendf(nil, `{
        "AliasTarget": {
            "DNSName": {
                "$ref": "formae://%s#/DomainName",
                "$value": "d28wbczi6hzqwy.cloudfront.net"
            },
            "EvaluateTargetHealth": false,
            "HostedZoneId": "Z2FDTNDATAQYW2"
        },
        "HostedZoneId": {
            "$ref": "formae://%s#/Id",
            "$value": "Z09517633GCSHWAHAJZBF"
        },
        "Name": "aa.test.platform.engineering",
        "TTL": "300",
        "Type": "A"
    }`, distributionKsuid, hostedZoneKsuid)

	pluginProps := fmt.Appendf(nil, `{
        "AliasTarget": {
            "DNSName": {
                "$ref": "formae://%s#/DomainName",
                "$value": null
            },
            "EvaluateTargetHealth": false,
            "HostedZoneId": "Z2FDTNDATAQYW2"
        },
        "HostedZoneId": {
            "$ref": "formae://%s#/Id",
            "$value": null
        },
        "Name": "aa.test.platform.engineering",
        "TTL": "300",
        "Type": "A"
    }`, distributionKsuid, hostedZoneKsuid)

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, pkgmodel.Schema{})
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	// Check that $value fields are preserved from userProps
	aliasTarget := mergedMap["AliasTarget"].(map[string]any)
	dnsName := aliasTarget["DNSName"].(map[string]any)
	require.Equal(t, "d28wbczi6hzqwy.cloudfront.net", dnsName["$value"])

	hostedZoneId := mergedMap["HostedZoneId"].(map[string]any)
	require.Equal(t, "Z09517633GCSHWAHAJZBF", hostedZoneId["$value"])
}

func TestResolveValue_UpdatesResolvedValueInProperties(t *testing.T) {
	resourceKsuid := util.NewID()
	resolvableUri := pkgmodel.FormaeURI(fmt.Sprintf("formae://%s#/Id", resourceKsuid))
	resourceUpdate := &ResourceUpdate{
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(fmt.Sprintf(`{"resolvable": {"$ref":"formae://%s#/Id"}}`, resourceKsuid)),
		},
	}
	err := resourceUpdate.ResolveValue(resolvableUri, "12345")
	assert.NoError(t, err)

	expectedJson := fmt.Sprintf(`{"resolvable":{"$ref":"formae://%s#/Id","$value":"12345"}}`, resourceKsuid)
	assert.JSONEq(t, expectedJson, string(resourceUpdate.DesiredState.Properties))
}

func TestUpdateState_InProgress(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		State: ResourceUpdateStateNotStarted,
		ProgressResult: []plugin.TrackedProgress{
			{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusInProgress,
				},
			},
		},
	}
	resourceUpdate.UpdateState()

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
}

func TestUpdateState_FailedButIncomplete(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation: OperationUpdate,
		State:     ResourceUpdateStateNotStarted,
		ProgressResult: []plugin.TrackedProgress{
			{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.OperationRead,
					OperationStatus: resource.OperationStatusFailure,
				},
			},
		},
	}
	resourceUpdate.UpdateState()

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
}

func TestUpdateState_FailedAndComplete(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation: OperationDelete,
		State:     ResourceUpdateStateNotStarted,
		ProgressResult: []plugin.TrackedProgress{
			{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.OperationRead,
					OperationStatus: resource.OperationStatusSuccess,
				},
			},
			{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusFailure,
				},
			},
		},
	}
	resourceUpdate.UpdateState()

	assert.Equal(t, ResourceUpdateStateFailed, resourceUpdate.State)
}

// TestUpdateState_RestartRecovery_SuccessProgressDerivesSuccessState tests the restart recovery
// scenario where a ResourceUpdate has Success progress from before the restart, and UpdateState
// should correctly derive the Success state.
func TestUpdateState_RestartRecovery_SuccessProgressDerivesSuccessState(t *testing.T) {
	// Simulate a CREATE operation that completed successfully before restart
	// The state is NotStarted (as if it was reset), but progress shows Success
	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted, // State before UpdateState() is called
		ProgressResult: []plugin.TrackedProgress{
			{
				ProgressResult: resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
				},
			},
		},
	}

	// UpdateState should derive Success from the progress, not keep NotStarted
	resourceUpdate.UpdateState()

	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State,
		"UpdateState should derive Success state from Success progress, not keep NotStarted")
}

// TestUpdateState_RestartRecovery_NoProgressDerivesNotStarted tests that resources
// without any progress are correctly set to NotStarted after restart.
func TestUpdateState_RestartRecovery_NoProgressDerivesNotStarted(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation:      OperationCreate,
		State:          ResourceUpdateStateInProgress, // Some stale state
		ProgressResult: nil,                           // No progress recorded
	}

	resourceUpdate.UpdateState()

	assert.Equal(t, ResourceUpdateStateNotStarted, resourceUpdate.State,
		"UpdateState should set NotStarted when there's no progress")
}

func Test_mergeRefsPreservingUserRefs_preservesResolvableValuesWithVisibilityWithoutValue(t *testing.T) {
	hostedZoneKsuid := util.NewID()

	userProps := fmt.Appendf(nil, `{
        "HostedZoneId": {
            "$ref": "formae://%s#/Id",
            "$visibility": "Clear"
        },
        "Name": "a.snarf.platform.engineering",
        "ResourceRecords": ["192.168.55.2"],
        "TTL": 350,
        "Type": "A"
    }`, hostedZoneKsuid)

	pluginProps := []byte(`{
        "HostedZoneId": "Z087209931QU7X2Y9HBBG",
        "Name": "a.snarf.platform.engineering",
        "ResourceRecords": ["192.168.55.2"],
        "TTL": 300,
        "Type": "A"
    }`)

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, pkgmodel.Schema{})
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	// Check that HostedZoneId preserves the $ref structure from userProps
	hostedZoneId := mergedMap["HostedZoneId"].(map[string]any)
	require.Equal(t, fmt.Sprintf("formae://%s#/Id", hostedZoneKsuid), hostedZoneId["$ref"])
	require.Equal(t, "Clear", hostedZoneId["$visibility"])

	// Verify that the resolved value from pluginProps is merged in
	require.Equal(t, "Z087209931QU7X2Y9HBBG", hostedZoneId["$value"])

	// Check other properties are preserved correctly
	require.Equal(t, "a.snarf.platform.engineering", mergedMap["Name"])
	require.Equal(t, "A", mergedMap["Type"])

	// Check ResourceRecords array
	resourceRecords := mergedMap["ResourceRecords"].([]any)
	require.Len(t, resourceRecords, 1)
	require.Equal(t, "192.168.55.2", resourceRecords[0])

	// Check TTL - since the plugin is returning 300 it will overwrite the user changes
	require.Equal(t, float64(300), mergedMap["TTL"])
}

func Test_mergeRefsPreservingUserRefs_preservesResolvableValuesWithVisibilityAndValue(t *testing.T) {
	hostedZoneKsuid := util.NewID()

	userProps := fmt.Appendf(nil, `{
        "HostedZoneId": {
            "$ref": "formae://%s#/Id",
            "$visibility": "Clear",
            "$value": "Z0477298SEIH33ICWILW"
        },
        "Name": "a.snarf.platform.engineering",
        "ResourceRecords": ["192.168.55.1"],
        "TTL": 500,
        "Type": "A"
    }`, hostedZoneKsuid)

	pluginProps := []byte(`{
        "HostedZoneId": "Z0477298SEIH33ICWILW",
        "Name": "a.snarf.platform.engineering",
        "ResourceRecords": ["192.168.55.1"],
        "TTL": 500,
        "Type": "A"
    }`)

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, pkgmodel.Schema{})
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	// Check that HostedZoneId preserves the $ref structure from userProps
	hostedZoneId := mergedMap["HostedZoneId"].(map[string]any)
	require.Equal(t, fmt.Sprintf("formae://%s#/Id", hostedZoneKsuid), hostedZoneId["$ref"])
	require.Equal(t, "Clear", hostedZoneId["$visibility"])

	// Verify that the resolved value is preserved from userProps (since it already has a value)
	require.Equal(t, "Z0477298SEIH33ICWILW", hostedZoneId["$value"])

	// Check other properties are preserved correctly
	require.Equal(t, "a.snarf.platform.engineering", mergedMap["Name"])
	require.Equal(t, "A", mergedMap["Type"])

	// Check ResourceRecords array
	resourceRecords := mergedMap["ResourceRecords"].([]any)
	require.Len(t, resourceRecords, 1)
	require.Equal(t, "192.168.55.1", resourceRecords[0])

	// Check TTL - should preserve the value from userProps/pluginProps (both are 500)
	require.Equal(t, float64(500), mergedMap["TTL"])
}

func Test_mergeRefsPreservingUserRefs_RemovesArrayElements(t *testing.T) {
	hostedZoneKsuid := util.NewID()

	userProps := fmt.Append(nil, `{
        "HostedZoneId": "Z0477298SEIH33ICWILW",
        "Name": "a.snarf.platform.engineering",
        "ResourceRecords": ["192.168.55.1", "192.168.55.2", "192.168.55.3"],
        "TTL": 500,
        "Type": "A"
    }`, hostedZoneKsuid)

	pluginProps := []byte(`{
        "HostedZoneId": "Z0477298SEIH33ICWILW",
        "Name": "a.snarf.platform.engineering",
        "ResourceRecords": ["192.168.55.1", "192.168.55.3"],
        "TTL": 500,
        "Type": "A"
    }`)

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, pkgmodel.Schema{})
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	resourceRecords := mergedMap["ResourceRecords"].([]any)
	require.Len(t, resourceRecords, 2)
	require.Equal(t, "192.168.55.1", resourceRecords[0])
	require.Equal(t, "192.168.55.3", resourceRecords[1])
}

// TestRecordProgress_IntermediateProgressPreservesRefStructuresInArrays tests that $ref structures
// in arrays are preserved when intermediate progress updates (InProgress status) have empty properties.
// This prevents the "self-cannibalization" bug where:
// 1. First progress (InProgress) has empty properties {}
// 2. Merge would wipe out arrays because plugin arrays are empty
// 3. User's $ref structures in arrays would be lost forever
// 4. Final progress (Success) can't restore them because they're already gone
func TestRecordProgress_IntermediateProgressPreservesRefStructuresInArrays(t *testing.T) {
	diskKsuid := util.NewID()
	networkKsuid := util.NewID()
	subnetKsuid := util.NewID()

	// Initial user properties with $ref structures in arrays (simulating a compute instance)
	userProps := fmt.Sprintf(`{
		"region": "us-east-1",
		"name": "formae-demo-instance",
		"instanceType": "small",
		"disks": [{
			"autoDelete": false,
			"boot": true,
			"source": {
				"$ref": "formae://%s#/selfLink",
				"$value": "arn:cloud:compute:us-east-1:123456789:disk/formae-demo-boot-disk"
			}
		}],
		"networkInterfaces": [{
			"network": {
				"$ref": "formae://%s#/selfLink",
				"$value": "arn:cloud:network:us-east-1:123456789:vpc/formae-demo-network"
			},
			"subnetwork": {
				"$ref": "formae://%s#/selfLink",
				"$value": "arn:cloud:network:us-east-1:123456789:subnet/public-subnet"
			}
		}],
		"labels": {"environment": "demo"}
	}`, diskKsuid, networkKsuid, subnetKsuid)

	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted,
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(userProps),
			Schema: pkgmodel.Schema{
				Fields: []string{"region", "name", "instanceType", "disks", "networkInterfaces", "labels"},
			},
		},
	}

	// First progress: InProgress with EMPTY properties (this is what the plugin returns during polling)
	firstProgress := &resource.ProgressResult{
		Operation:          resource.OperationCreate,
		OperationStatus:    resource.OperationStatusInProgress,
		RequestID:          "test-create-request-id",
		StartTs:            util.TimeNow(),
		ModifiedTs:         util.TimeNow().Add(10 * time.Second),
		MaxAttempts:        3,
		ResourceProperties: json.RawMessage(`{}`), // Empty properties during intermediate polling
	}

	err := resourceUpdate.RecordProgress(firstProgress)
	require.NoError(t, err)

	// Verify state is InProgress
	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)

	// CRITICAL: Verify that $ref structures in arrays are PRESERVED after intermediate progress
	var propsAfterFirst map[string]any
	err = json.Unmarshal(resourceUpdate.DesiredState.Properties, &propsAfterFirst)
	require.NoError(t, err)

	// Check disks array still has $ref
	disks, ok := propsAfterFirst["disks"].([]any)
	require.True(t, ok, "disks should still be an array")
	require.Len(t, disks, 1, "disks array should have 1 element")

	disk := disks[0].(map[string]any)
	source, ok := disk["source"].(map[string]any)
	require.True(t, ok, "source should be an object with $ref")
	require.Equal(t, fmt.Sprintf("formae://%s#/selfLink", diskKsuid), source["$ref"],
		"disk source $ref should be preserved after intermediate progress")

	// Check networkInterfaces array still has $ref
	networkInterfaces, ok := propsAfterFirst["networkInterfaces"].([]any)
	require.True(t, ok, "networkInterfaces should still be an array")
	require.Len(t, networkInterfaces, 1, "networkInterfaces array should have 1 element")

	nic := networkInterfaces[0].(map[string]any)
	network, ok := nic["network"].(map[string]any)
	require.True(t, ok, "network should be an object with $ref")
	require.Equal(t, fmt.Sprintf("formae://%s#/selfLink", networkKsuid), network["$ref"],
		"network $ref should be preserved after intermediate progress")

	subnetwork, ok := nic["subnetwork"].(map[string]any)
	require.True(t, ok, "subnetwork should be an object with $ref")
	require.Equal(t, fmt.Sprintf("formae://%s#/selfLink", subnetKsuid), subnetwork["$ref"],
		"subnetwork $ref should be preserved after intermediate progress")

	// Second progress: Success with FULL properties from the cloud provider
	// The plugin returns $ref objects that match the user's $ref structures
	finalPluginProps := fmt.Sprintf(`{
		"deletionProtection": false,
		"disks": [{
			"architecture": "X86_64",
			"autoDelete": false,
			"boot": true,
			"deviceName": "persistent-disk-0",
			"diskSizeGb": "20",
			"source": {
				"$ref": "formae://%s#/selfLink",
				"$value": "arn:cloud:compute:us-east-1:123456789:disk/formae-demo-boot-disk"
			}
		}],
		"labels": {"environment": "demo"},
		"instanceType": "arn:cloud:compute:us-east-1:123456789:instance-type/small",
		"name": "formae-demo-instance",
		"networkInterfaces": [{
			"fingerprint": "inQ5pxBw2-M=",
			"name": "nic0",
			"network": {
				"$ref": "formae://%s#/selfLink",
				"$value": "arn:cloud:network:us-east-1:123456789:vpc/formae-demo-network"
			},
			"networkIP": "10.0.1.7",
			"subnetwork": {
				"$ref": "formae://%s#/selfLink",
				"$value": "arn:cloud:network:us-east-1:123456789:subnet/public-subnet"
			}
		}],
		"region": "us-east-1"
	}`, diskKsuid, networkKsuid, subnetKsuid)

	secondProgress := &resource.ProgressResult{
		Operation:          resource.OperationCreate,
		OperationStatus:    resource.OperationStatusSuccess,
		RequestID:          "test-create-request-id-2",
		StartTs:            util.TimeNow().Add(120 * time.Second),
		ModifiedTs:         util.TimeNow().Add(140 * time.Second),
		MaxAttempts:        3,
		ResourceProperties: json.RawMessage(finalPluginProps),
	}

	err = resourceUpdate.RecordProgress(secondProgress)
	require.NoError(t, err)

	// Verify state is Success
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)

	// Verify that $ref structures are STILL preserved after final merge
	var propsAfterFinal map[string]any
	err = json.Unmarshal(resourceUpdate.DesiredState.Properties, &propsAfterFinal)
	require.NoError(t, err)

	// Check disks array still has $ref after final merge
	disksAfterFinal, ok := propsAfterFinal["disks"].([]any)
	require.True(t, ok, "disks should still be an array after final merge")
	require.Len(t, disksAfterFinal, 1, "disks array should have 1 element after final merge")

	diskAfterFinal := disksAfterFinal[0].(map[string]any)
	sourceAfterFinal, ok := diskAfterFinal["source"].(map[string]any)
	require.True(t, ok, "source should be an object with $ref after final merge")
	require.Equal(t, fmt.Sprintf("formae://%s#/selfLink", diskKsuid), sourceAfterFinal["$ref"],
		"disk source $ref should be preserved after final merge")
	require.Equal(t, "arn:cloud:compute:us-east-1:123456789:disk/formae-demo-boot-disk",
		sourceAfterFinal["$value"], "disk source $value should be preserved")

	// Check networkInterfaces array still has $ref after final merge
	nicsAfterFinal, ok := propsAfterFinal["networkInterfaces"].([]any)
	require.True(t, ok, "networkInterfaces should still be an array after final merge")
	require.Len(t, nicsAfterFinal, 1, "networkInterfaces array should have 1 element after final merge")

	nicAfterFinal := nicsAfterFinal[0].(map[string]any)
	networkAfterFinal, ok := nicAfterFinal["network"].(map[string]any)
	require.True(t, ok, "network should be an object with $ref after final merge")
	require.Equal(t, fmt.Sprintf("formae://%s#/selfLink", networkKsuid), networkAfterFinal["$ref"],
		"network $ref should be preserved after final merge")

	subnetworkAfterFinal, ok := nicAfterFinal["subnetwork"].(map[string]any)
	require.True(t, ok, "subnetwork should be an object with $ref after final merge")
	require.Equal(t, fmt.Sprintf("formae://%s#/selfLink", subnetKsuid), subnetworkAfterFinal["$ref"],
		"subnetwork $ref should be preserved after final merge")
}

// TestRecordProgress_MergePreservesRefStructuresInArrays tests that $ref structures
// in arrays are preserved when plugin returns plain string values instead of $ref objects.
func TestRecordProgress_MergePreservesRefStructuresInArrays(t *testing.T) {
	// User properties with $ref structures in arrays
	userProps := `{
		"name": "my-instance",
		"disks": [{"boot": true, "source": {"$ref": "formae://disk-ksuid#/selfLink", "$value": "disk-1"}}],
		"networkInterfaces": [{"network": {"$ref": "formae://network-ksuid#/selfLink", "$value": "network-1"}, "subnetwork": {"$ref": "formae://subnet-ksuid#/selfLink", "$value": "subnet-1"}}]
	}`

	// Plugin returns PLAIN STRINGS, not $ref objects
	pluginProps := `{
		"name": "my-instance",
		"disks": [{"boot": true, "size": "20GB", "source": "disk-1"}],
		"networkInterfaces": [{"name": "nic0", "network": "network-1", "subnetwork": "subnet-1"}]
	}`

	// Expected: plugin fields merged with $ref structures preserved from user
	expectedProps := `{
		"name": "my-instance",
		"disks": [{"boot": true, "size": "20GB", "source": {"$ref": "formae://disk-ksuid#/selfLink", "$value": "disk-1"}}],
		"networkInterfaces": [{"name": "nic0", "network": {"$ref": "formae://network-ksuid#/selfLink", "$value": "network-1"}, "subnetwork": {"$ref": "formae://subnet-ksuid#/selfLink", "$value": "subnet-1"}}]
	}`

	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted,
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(userProps),
			Schema:     pkgmodel.Schema{Fields: []string{"name", "disks", "networkInterfaces"}},
		},
	}

	progress := &resource.ProgressResult{
		Operation:          resource.OperationCreate,
		OperationStatus:    resource.OperationStatusSuccess,
		RequestID:          "test-request",
		StartTs:            util.TimeNow(),
		ModifiedTs:         util.TimeNow().Add(10 * time.Second),
		MaxAttempts:        3,
		ResourceProperties: json.RawMessage(pluginProps),
	}

	err := resourceUpdate.RecordProgress(progress)
	require.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.JSONEq(t, expectedProps, string(resourceUpdate.DesiredState.Properties))
}

// TestRecordProgress_MergeArrays_UserHasMoreElementsThanPlugin tests that when user properties
// have more array elements than plugin returns, the merged result only contains the elements
// that the plugin returned. This ensures we don't keep stale/removed elements.
func TestRecordProgress_MergeArrays_UserHasMoreElementsThanPlugin(t *testing.T) {
	// User has 2 networkInterfaces
	userProps := `{
		"name": "my-instance",
		"networkInterfaces": [
			{"network": {"$ref": "formae://network1-ksuid#/selfLink", "$value": "network-1"}, "subnetwork": {"$ref": "formae://subnet1-ksuid#/selfLink", "$value": "subnet-1"}},
			{"network": {"$ref": "formae://network2-ksuid#/selfLink", "$value": "network-2"}, "subnetwork": {"$ref": "formae://subnet2-ksuid#/selfLink", "$value": "subnet-2"}}
		]
	}`

	// Plugin returns only 1 networkInterface (the first one)
	pluginProps := `{
		"name": "my-instance",
		"networkInterfaces": [{"name": "nic0", "network": "network-1", "subnetwork": "subnet-1"}]
	}`

	// Expected: only 1 networkInterface with $ref preserved from user's first element
	expectedProps := `{
		"name": "my-instance",
		"networkInterfaces": [{"name": "nic0", "network": {"$ref": "formae://network1-ksuid#/selfLink", "$value": "network-1"}, "subnetwork": {"$ref": "formae://subnet1-ksuid#/selfLink", "$value": "subnet-1"}}]
	}`

	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted,
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(userProps),
			Schema:     pkgmodel.Schema{Fields: []string{"name", "networkInterfaces"}},
		},
	}

	progress := &resource.ProgressResult{
		Operation:          resource.OperationCreate,
		OperationStatus:    resource.OperationStatusSuccess,
		RequestID:          "test-request",
		StartTs:            util.TimeNow(),
		ModifiedTs:         util.TimeNow().Add(10 * time.Second),
		MaxAttempts:        3,
		ResourceProperties: json.RawMessage(pluginProps),
	}

	err := resourceUpdate.RecordProgress(progress)
	require.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.JSONEq(t, expectedProps, string(resourceUpdate.DesiredState.Properties))
}

// TestRecordProgress_MergeArrays_PluginReturnsReorderedElements tests that when plugin returns
// array elements in a different order than user provided, the correct $ref structures are
// matched and preserved based on value matching, not index position.
func TestRecordProgress_MergeArrays_PluginReturnsReorderedElements(t *testing.T) {
	// User: network-1, network-2 (in that order)
	userProps := `{
		"name": "my-instance",
		"networkInterfaces": [
			{"network": {"$ref": "formae://network1-ksuid#/selfLink", "$value": "network-1"}, "subnetwork": {"$ref": "formae://subnet1-ksuid#/selfLink", "$value": "subnet-1"}},
			{"network": {"$ref": "formae://network2-ksuid#/selfLink", "$value": "network-2"}, "subnetwork": {"$ref": "formae://subnet2-ksuid#/selfLink", "$value": "subnet-2"}}
		]
	}`

	// Plugin returns REVERSED order: network-2 first, then network-1
	pluginProps := `{
		"name": "my-instance",
		"networkInterfaces": [
			{"name": "nic1", "network": "network-2", "subnetwork": "subnet-2"},
			{"name": "nic0", "network": "network-1", "subnetwork": "subnet-1"}
		]
	}`

	// Expected: plugin order preserved (2, 1) with correct $ref matched by value
	expectedProps := `{
		"name": "my-instance",
		"networkInterfaces": [
			{"name": "nic1", "network": {"$ref": "formae://network2-ksuid#/selfLink", "$value": "network-2"}, "subnetwork": {"$ref": "formae://subnet2-ksuid#/selfLink", "$value": "subnet-2"}},
			{"name": "nic0", "network": {"$ref": "formae://network1-ksuid#/selfLink", "$value": "network-1"}, "subnetwork": {"$ref": "formae://subnet1-ksuid#/selfLink", "$value": "subnet-1"}}
		]
	}`

	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted,
		DesiredState: pkgmodel.Resource{
			Properties: json.RawMessage(userProps),
			Schema:     pkgmodel.Schema{Fields: []string{"name", "networkInterfaces"}},
		},
	}

	progress := &resource.ProgressResult{
		Operation:          resource.OperationCreate,
		OperationStatus:    resource.OperationStatusSuccess,
		RequestID:          "test-request",
		StartTs:            util.TimeNow(),
		ModifiedTs:         util.TimeNow().Add(10 * time.Second),
		MaxAttempts:        3,
		ResourceProperties: json.RawMessage(pluginProps),
	}

	err := resourceUpdate.RecordProgress(progress)
	require.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.JSONEq(t, expectedProps, string(resourceUpdate.DesiredState.Properties))
}
