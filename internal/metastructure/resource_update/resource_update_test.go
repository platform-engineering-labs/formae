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

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps)
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
// should correctly derive the Success state. This is critical for Bug 4 (success count lost on restart)
// where previously ReRunIncompleteCommands() would blindly reset all states to NotStarted.
func TestUpdateState_RestartRecovery_SuccessProgressDerivesSuccessState(t *testing.T) {
	// Simulate a CREATE operation that completed successfully before restart
	// The state is NotStarted (as if it was reset), but progress shows Success
	resourceUpdate := &ResourceUpdate{
		Operation: OperationCreate,
		State:     ResourceUpdateStateNotStarted, // Simulates being reset by buggy code
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
		State:         ResourceUpdateStateInProgress, // Some stale state
		ProgressResult: nil,                          // No progress recorded
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

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps)
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

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps)
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

	merged, err := mergeRefsPreservingUserRefs(userProps, pluginProps)
	require.NoError(t, err)

	var mergedMap map[string]any
	err = json.Unmarshal(merged, &mergedMap)
	require.NoError(t, err)

	resourceRecords := mergedMap["ResourceRecords"].([]any)
	require.Len(t, resourceRecords, 2)
	require.Equal(t, "192.168.55.1", resourceRecords[0])
	require.Equal(t, "192.168.55.3", resourceRecords[1])
}
