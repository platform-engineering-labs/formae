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
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestRecordResourceProgress_UpdatesResourceProgress_ForDeleteOperation(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation: OperationDelete,
		State:     ResourceUpdateStateNotStarted,
	}
	readProgress := &resource.ProgressResult{
		Operation:       resource.OperationRead,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-request-id",
		StartTs:         util.TimeNow(),
		ModifiedTs:      util.TimeNow().Add(10 * time.Second),
		MaxAttempts:     3,
	}

	err := resourceUpdate.RecordProgress(readProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, readProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	firstDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-delete-request-id",
		StartTs:         util.TimeNow().Add(60 * time.Second),
		ModifiedTs:      util.TimeNow().Add(80 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(firstDeleteProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, firstDeleteProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	secondDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-delete-request-id",
		StartTs:         util.TimeNow().Add(120 * time.Second),
		ModifiedTs:      util.TimeNow().Add(140 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(secondDeleteProgress)
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, secondDeleteProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)
}

func TestRecordResourceProgress_UpdatesResourceProgress_ForCreateOperation(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted,
	}
	firstCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-create-request-id",
		StartTs:         util.TimeNow().Add(60 * time.Second),
		ModifiedTs:      util.TimeNow().Add(80 * time.Second),
		MaxAttempts:     3,
	}

	err := resourceUpdate.RecordProgress(firstCreateProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, firstCreateProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, firstCreateProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	secondCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-create-request-id",
		StartTs:         util.TimeNow().Add(120 * time.Second),
		ModifiedTs:      util.TimeNow().Add(140 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(secondCreateProgress)
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.WithinDuration(t, firstCreateProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, secondCreateProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)
}

func TestRecordResourceProgress_UpdatesResourceProgress_ForUpdateOperation(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation: OperationUpdate,
		State:     ResourceUpdateStateNotStarted,
	}
	readProgress := &resource.ProgressResult{
		Operation:       resource.OperationRead,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-request-id",
		StartTs:         util.TimeNow(),
		ModifiedTs:      util.TimeNow().Add(10 * time.Second),
		MaxAttempts:     3,
	}

	err := resourceUpdate.RecordProgress(readProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, readProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	firstUpdateProgress := &resource.ProgressResult{
		Operation:       resource.OperationUpdate,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-update-request-id",
		StartTs:         util.TimeNow().Add(60 * time.Second),
		ModifiedTs:      util.TimeNow().Add(80 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(firstUpdateProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, firstUpdateProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	secondUpdateProgress := &resource.ProgressResult{
		Operation:       resource.OperationUpdate,
		OperationStatus: resource.OperationStatusFailure,
		RequestID:       "test-second-update-request-id",
		StartTs:         util.TimeNow().Add(120 * time.Second),
		ModifiedTs:      util.TimeNow().Add(140 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(secondUpdateProgress)
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateFailed, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, secondUpdateProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)
}

func TestRecordResourceProgress_UpdatesResourceProgress_ForReplaceOperation(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		Operation: OperationReplace,
		State:     ResourceUpdateStateNotStarted,
	}
	readProgress := &resource.ProgressResult{
		Operation:       resource.OperationRead,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-request-id",
		StartTs:         util.TimeNow(),
		ModifiedTs:      util.TimeNow().Add(10 * time.Second),
		MaxAttempts:     3,
	}

	err := resourceUpdate.RecordProgress(readProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, readProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	firstDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-replace-request-id",
		StartTs:         util.TimeNow().Add(60 * time.Second),
		ModifiedTs:      util.TimeNow().Add(80 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(firstDeleteProgress)
	assert.NoError(t, err)

	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, firstDeleteProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	secondDeleteProgress := &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-replace-request-id",
		StartTs:         util.TimeNow().Add(120 * time.Second),
		ModifiedTs:      util.TimeNow().Add(140 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(secondDeleteProgress)
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, secondDeleteProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	firstCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusInProgress,
		RequestID:       "test-create-request-id",
		StartTs:         util.TimeNow().Add(180 * time.Second),
		ModifiedTs:      util.TimeNow().Add(200 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(firstCreateProgress)
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateInProgress, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, firstCreateProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)

	secondCreateProgress := &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusSuccess,
		RequestID:       "test-second-create-request-id",
		StartTs:         util.TimeNow().Add(240 * time.Second),
		ModifiedTs:      util.TimeNow().Add(260 * time.Second),
		MaxAttempts:     3,
	}

	err = resourceUpdate.RecordProgress(secondCreateProgress)
	assert.NoError(t, err)
	assert.Equal(t, ResourceUpdateStateSuccess, resourceUpdate.State)
	assert.WithinDuration(t, readProgress.StartTs, resourceUpdate.StartTs, 1*time.Second)
	assert.WithinDuration(t, secondCreateProgress.ModifiedTs, resourceUpdate.ModifiedTs, 1*time.Second)
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
		Resource: pkgmodel.Resource{
			Properties: json.RawMessage(fmt.Sprintf(`{"resolvable": {"$ref":"formae://%s#/Id"}}`, resourceKsuid)),
		},
	}
	err := resourceUpdate.ResolveValue(resolvableUri, "12345")
	assert.NoError(t, err)

	expectedJson := fmt.Sprintf(`{"resolvable":{"$ref":"formae://%s#/Id","$value":"12345"}}`, resourceKsuid)
	assert.JSONEq(t, expectedJson, string(resourceUpdate.Resource.Properties))
}

func TestUpdateState_InProgress(t *testing.T) {
	resourceUpdate := &ResourceUpdate{
		State: ResourceUpdateStateNotStarted,
		ProgressResult: []resource.ProgressResult{
			{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusInProgress,
				MaxAttempts:     3,
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
		ProgressResult: []resource.ProgressResult{
			{
				Operation:       resource.OperationRead,
				OperationStatus: resource.OperationStatusFailure,
				MaxAttempts:     3,
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
		ProgressResult: []resource.ProgressResult{
			{
				Operation:       resource.OperationRead,
				OperationStatus: resource.OperationStatusSuccess,
				MaxAttempts:     3,
			},
			{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusFailure,
				MaxAttempts:     3,
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
		ProgressResult: []resource.ProgressResult{
			{
				Operation:       resource.OperationCreate,
				OperationStatus: resource.OperationStatusSuccess,
				MaxAttempts:     3,
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

func Test_mergeRefsPreservingUserRefs_PreservesRefsInArrays(t *testing.T) {
	// This test verifies that $ref structures inside arrays are preserved during merge.
	// This is critical for dependency tracking during DELETE operations.
	//
	// Scenario: Provider provides Instance with networks array containing $ref to subnet's network_id
	// User provides: networks: [{uuid: {$ref: "formae://subnet#network_id", $value: "net-123"}}]
	// Plugin returns: networks: [{uuid: "net-123"}]
	// Expected: networks: [{uuid: {$ref: "formae://subnet#network_id", $value: "net-123"}}]
	//
	// If $ref is lost, ExtractResolvableURIs won't find the dependency,
	// and DELETE will run Instance and Subnet in parallel, causing SubnetInUse errors.

	subnetKsuid := util.NewID()

	userProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{
				"uuid": {
					"$ref": "formae://%s#/network_id",
					"$value": "net-123"
				}
			}
		]
	}`, subnetKsuid)

	pluginProps := []byte(`{
		"name": "my-instance",
		"networks": [
			{
				"uuid": "net-123"
			}
		]
	}`)

	schema := pkgmodel.Schema{
		Fields: []string{"name", "networks"},
		Hints:  map[string]pkgmodel.FieldHint{},
	}

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, schema)
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	// Verify name is preserved
	require.Equal(t, "my-instance", mergedMap["name"])

	// Verify networks array preserves the $ref structure
	networks := mergedMap["networks"].([]any)
	require.Len(t, networks, 1)

	network := networks[0].(map[string]any)
	uuid := network["uuid"].(map[string]any)

	// The $ref should be preserved from userProps
	require.Equal(t, fmt.Sprintf("formae://%s#/network_id", subnetKsuid), uuid["$ref"])
	// The $value should be updated from pluginProps (or preserved if same)
	require.Equal(t, "net-123", uuid["$value"])
}

func Test_mergeRefsPreservingUserRefs_PreservesRefsInArrays_MixedWithHardcoded(t *testing.T) {
	// More realistic test matching Provider Instance scenario:
	// - First network: hardcoded public network ID (no $ref)
	// - Second network: $ref to subnet's network_id
	// Plugin may return them in different order

	subnetKsuid := util.NewID()
	extNetID := "b347ed75-8603-4ce0-a40c-c6c98a8820fc"
	privateNetID := "a9a04d82-051a-42d4-87a6-83052fccb081"

	userProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{
				"uuid": "%s"
			},
			{
				"uuid": {
					"$ref": "formae://%s#/network_id",
					"$value": "%s"
				}
			}
		]
	}`, extNetID, subnetKsuid, privateNetID)

	// Plugin returns in DIFFERENT order (private first, then public)
	pluginProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{
				"uuid": "%s"
			},
			{
				"uuid": "%s"
			}
		]
	}`, privateNetID, extNetID)

	schema := pkgmodel.Schema{
		Fields: []string{"name", "networks"},
		Hints:  map[string]pkgmodel.FieldHint{},
	}

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, schema)
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	networks := mergedMap["networks"].([]any)
	require.Len(t, networks, 2)

	// Plugin element 0 (privateNetID) should match user element 1 (the one with $ref)
	network0 := networks[0].(map[string]any)
	uuid0, isMap := network0["uuid"].(map[string]any)
	require.True(t, isMap, "networks[0].uuid should be a map with $ref preserved")
	require.Equal(t, fmt.Sprintf("formae://%s#/network_id", subnetKsuid), uuid0["$ref"])
	require.Equal(t, privateNetID, uuid0["$value"])

	// Plugin element 1 (extNetID) should match user element 0 (hardcoded, no $ref)
	network1 := networks[1].(map[string]any)
	uuid1, isString := network1["uuid"].(string)
	require.True(t, isString, "networks[1].uuid should be a plain string")
	require.Equal(t, extNetID, uuid1)
}

func Test_mergeRefsPreservingUserRefs_WithoutInitialValue(t *testing.T) {
	// Test that merge works when user properties have $ref WITHOUT $value
	// This is the format produced by translateFormaeReferencesToKsuid

	subnetKsuid := util.NewID()
	extNetID := "b347ed75-8603-4ce0-a40c-c6c98a8820fc"
	privateNetID := "a9a04d82-051a-42d4-87a6-83052fccb081"

	// User properties with $ref ONLY (no $value) - this is what translation produces
	userProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{
				"uuid": "%s"
			},
			{
				"uuid": {
					"$ref": "formae://%s#/network_id"
				}
			}
		]
	}`, extNetID, subnetKsuid)

	// Plugin returns resolved values
	pluginProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{
				"uuid": "%s"
			},
			{
				"uuid": "%s"
			}
		]
	}`, privateNetID, extNetID)

	schema := pkgmodel.Schema{
		Fields: []string{"name", "networks"},
		Hints:  map[string]pkgmodel.FieldHint{},
	}

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, schema)
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	networks := mergedMap["networks"].([]any)
	require.Len(t, networks, 2)

	// Find which network has the $ref
	var refFound bool
	var refValue string
	for _, net := range networks {
		netMap := net.(map[string]any)
		if uuid, ok := netMap["uuid"].(map[string]any); ok {
			if ref, hasRef := uuid["$ref"].(string); hasRef {
				refFound = true
				refValue = ref
				// Should also have $value from plugin
				require.Contains(t, uuid, "$value", "merged $ref object should have $value")
			}
		}
	}

	require.True(t, refFound, "$ref should be preserved in merged properties")
	require.Equal(t, fmt.Sprintf("formae://%s#/network_id", subnetKsuid), refValue)
}

// Test_EndToEnd_MergeToDestroyDependency verifies that the entire flow works:
// 1. User properties with $ref (no $value) are merged with plugin properties
// 2. The merged result preserves $ref with $value
// 3. A resource with the merged properties can be used to create a destroy update
// 4. The destroy update's RemainingResolvables contains the expected URIs
func Test_EndToEnd_MergeToDestroyDependency(t *testing.T) {
	// Setup: Create a scenario like the Provider Instance with networks referencing Subnet
	subnetKsuid := util.NewID()
	instanceKsuid := util.NewID()
	extNetID := "b347ed75-8603-4ce0-a40c-c6c98a8820fc"
	privateNetID := "4cba1d81-051a-42d4-87a6-83052fccb081"

	// Step 1: User properties with $ref ONLY (no $value) - what PKL translation produces
	userProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{"uuid": "%s"},
			{"uuid": {"$ref": "formae://%s#/network_id"}}
		]
	}`, extNetID, subnetKsuid)

	// Step 2: Plugin returns properties (plain values, potentially in different order)
	pluginProps := fmt.Appendf(nil, `{
		"name": "my-instance",
		"networks": [
			{"uuid": "%s"},
			{"uuid": "%s"}
		]
	}`, privateNetID, extNetID)

	schema := pkgmodel.Schema{
		Fields: []string{"name", "networks"},
		Hints:  map[string]pkgmodel.FieldHint{},
	}

	// Step 3: Merge the properties (simulates what happens after Create)
	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps, schema)
	require.NoError(t, err)

	// Step 4: Create a resource with the merged properties (simulates what's stored in datastore)
	instanceResource := pkgmodel.Resource{
		Label:      "my-instance",
		Type:       "Provider::Compute::Instance",
		Stack:      "default",
		Ksuid:      instanceKsuid,
		Properties: merged,
		Schema:     schema,
	}

	// Step 5: Create a destroy update from the resource
	target := pkgmodel.Target{
		Label: "test-target",
	}
	destroyUpdate, err := NewResourceUpdateForDestroy(instanceResource, target, FormaCommandSourceUser)
	require.NoError(t, err)

	// Step 6: Verify RemainingResolvables contains the Subnet's URI
	require.NotEmpty(t, destroyUpdate.RemainingResolvables,
		"Destroy update should have RemainingResolvables extracted from properties")

	// Find the subnet's URI in the resolvables
	expectedSubnetURI := pkgmodel.FormaeURI(fmt.Sprintf("formae://%s#/network_id", subnetKsuid))
	found := false
	for _, uri := range destroyUpdate.RemainingResolvables {
		if uri.Stripped() == expectedSubnetURI.Stripped() {
			found = true
			break
		}
	}
	require.True(t, found,
		"RemainingResolvables should contain the subnet's URI. Got: %v", destroyUpdate.RemainingResolvables)

	t.Logf("Success! Destroy update has RemainingResolvables: %v", destroyUpdate.RemainingResolvables)
}
