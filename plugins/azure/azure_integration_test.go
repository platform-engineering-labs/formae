// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

var (
	testSubscriptionID = "b161e970-a04d-48a4-ac91-18d06e8d8047"
	testResourceGroup  string
	azurePlugin        *Azure
	testNativeID       string
)

func TestMain(m *testing.M) {
	// Setup: Create unique resource group name
	testResourceGroup = fmt.Sprintf("formae-test-rg-%d", time.Now().Unix())
	azurePlugin = &Azure{}

	// Run tests
	code := m.Run()

	// Cleanup: Delete resource group if it was created using Azure SDK directly
	if testNativeID != "" {
		ctx := context.Background()

		// Create Azure client for cleanup
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			log.Printf("Failed to create credential for cleanup: %v\n", err)
		} else {
			rgClient, err := armresources.NewResourceGroupsClient(testSubscriptionID, cred, nil)
			if err != nil {
				log.Printf("Failed to create resource groups client for cleanup: %v\n", err)
			} else {
				// BeginDelete returns a poller for async deletion
				poller, err := rgClient.BeginDelete(ctx, testResourceGroup, nil)
				if err != nil {
					log.Printf("Failed to start resource group deletion: %v\n", err)
				} else {
					// Poll until deletion completes
					_, err = poller.PollUntilDone(ctx, nil)
					if err != nil {
						log.Printf("Failed to complete resource group deletion: %v\n", err)
					} else {
						log.Printf("Successfully deleted resource group: %s\n", testResourceGroup)
					}
				}
			}
		}
	}

	// Exit with test result code
	// Note: We don't call os.Exit here to allow deferred cleanup
	if code != 0 {
		panic(fmt.Sprintf("tests failed with code %d", code))
	}
}

func TestAzureCreate_Integration(t *testing.T) {
	ctx := context.Background()

	// Prepare target configuration
	targetConfig := fmt.Appendf(nil, `{"SubscriptionId":"%s"}`, testSubscriptionID)

	// Prepare resource properties (Resource Group)
	properties := []byte(`{
		"location": "eastus",
		"tags": {
			"test": "formae",
			"purpose": "integration-test"
		}
	}`)

	// Create request
	req := &resource.CreateRequest{
		Resource: &model.Resource{
			Type:       "Azure::Resources::ResourceGroup",
			Label:      testResourceGroup,
			Stack:      "test-stack",
			Properties: properties,
		},
		Target: &model.Target{
			Namespace: "Azure",
			Config:    targetConfig,
		},
	}

	// Execute: Call Azure plugin Create
	result, err := azurePlugin.Create(ctx, req)

	// Assert: Expect success
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationCreate, result.ProgressResult.Operation)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)
	assert.NotEmpty(t, result.ProgressResult.NativeID)
	assert.Equal(t, "Azure::Resources::ResourceGroup", result.ProgressResult.ResourceType)

	// Store NativeID for cleanup
	testNativeID = result.ProgressResult.NativeID
	t.Logf("Created resource group with ID: %s", testNativeID)

	// Verify tags were set correctly by reading from Azure
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	assert.NoError(t, err)
	rgClient, err := armresources.NewResourceGroupsClient(testSubscriptionID, cred, nil)
	assert.NoError(t, err)

	rg, err := rgClient.Get(ctx, testResourceGroup, nil)
	assert.NoError(t, err)
	assert.NotNil(t, rg.Tags)
	assert.Equal(t, "formae", *rg.Tags["test"])
	assert.Equal(t, "integration-test", *rg.Tags["purpose"])
	t.Logf("Verified tags: %v", rg.Tags)
}

func TestAzureRead_Integration(t *testing.T) {
	ctx := context.Background()

	// Create unique resource group name for this test
	rgName := fmt.Sprintf("formae-test-rg-read-%d", time.Now().Unix())

	// Create resource group using Azure SDK directly (not our plugin)
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	assert.NoError(t, err)

	rgClient, err := armresources.NewResourceGroupsClient(testSubscriptionID, cred, nil)
	assert.NoError(t, err)

	// Create resource group with specific properties
	location := "westus"
	testTag := "formae-read-test"
	purposeTag := "read-verification"
	params := armresources.ResourceGroup{
		Location: &location,
		Tags: map[string]*string{
			"test":    &testTag,
			"purpose": &purposeTag,
		},
	}

	createdRg, err := rgClient.CreateOrUpdate(ctx, rgName, params, nil)
	assert.NoError(t, err)
	assert.NotNil(t, createdRg.ID)
	t.Logf("Created resource group via Azure SDK: %s", *createdRg.ID)

	// Defer cleanup
	defer func() {
		poller, err := rgClient.BeginDelete(ctx, rgName, nil)
		if err != nil {
			log.Printf("Failed to start deletion of test resource group: %v\n", err)
			return
		}
		_, err = poller.PollUntilDone(ctx, nil)
		if err != nil {
			log.Printf("Failed to delete test resource group: %v\n", err)
		} else {
			log.Printf("Successfully deleted test resource group: %s\n", rgName)
		}
	}()

	// Now use our plugin to Read the resource
	targetConfig := fmt.Appendf(nil, `{"SubscriptionId":"%s"}`, testSubscriptionID)

	readReq := &resource.ReadRequest{
		NativeID:     *createdRg.ID,
		ResourceType: "Azure::Resources::ResourceGroup",
		Target: &model.Target{
			Namespace: "Azure",
			Config:    targetConfig,
		},
	}

	// Execute: Call Azure plugin Read
	result, err := azurePlugin.Read(ctx, readReq)

	// Assert: Expect success and correct properties
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify the resource type
	assert.Equal(t, "Azure::Resources::ResourceGroup", result.ResourceType)

	// Parse the properties to verify them
	var props map[string]interface{}
	err = json.Unmarshal([]byte(result.Properties), &props)
	assert.NoError(t, err)

	// Verify location
	assert.Equal(t, location, props["location"])

	// Verify tags using GetTagsFromProperties
	tags := model.GetTagsFromProperties([]byte(result.Properties))
	assert.Len(t, tags, 2)

	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[tag.Key] = tag.Value
	}
	assert.Equal(t, testTag, tagMap["test"])
	assert.Equal(t, purposeTag, tagMap["purpose"])

	t.Logf("Read operation successful. Properties: %v", props)
}

func TestAzureDelete_Integration(t *testing.T) {
	ctx := context.Background()

	// Create unique resource group name for this test
	rgName := fmt.Sprintf("formae-test-rg-delete-%d", time.Now().Unix())

	// Create resource group using Azure SDK directly (not our plugin)
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	assert.NoError(t, err)

	rgClient, err := armresources.NewResourceGroupsClient(testSubscriptionID, cred, nil)
	assert.NoError(t, err)

	// Create resource group
	location := "westus"
	params := armresources.ResourceGroup{
		Location: &location,
	}

	createdRg, err := rgClient.CreateOrUpdate(ctx, rgName, params, nil)
	assert.NoError(t, err)
	assert.NotNil(t, createdRg.ID)
	t.Logf("Created resource group via Azure SDK: %s", *createdRg.ID)

	// Defer cleanup in case test fails before deletion completes
	defer func() {
		// Try to delete if it still exists
		_, err := rgClient.Get(ctx, rgName, nil)
		if err == nil {
			// Resource still exists, clean it up
			poller, err := rgClient.BeginDelete(ctx, rgName, nil)
			if err != nil {
				log.Printf("Failed to start cleanup deletion: %v\n", err)
				return
			}
			_, err = poller.PollUntilDone(ctx, nil)
			if err != nil {
				log.Printf("Failed to complete cleanup deletion: %v\n", err)
			} else {
				log.Printf("Cleanup: deleted test resource group %s\n", rgName)
			}
		}
	}()

	// Prepare target configuration
	targetConfig := fmt.Appendf(nil, `{"SubscriptionId":"%s"}`, testSubscriptionID)

	// Execute: Call Azure plugin Delete
	nativeID := *createdRg.ID
	deleteReq := &resource.DeleteRequest{
		NativeID:     &nativeID,
		ResourceType: "Azure::Resources::ResourceGroup",
		Target: &model.Target{
			Namespace: "Azure",
			Config:    targetConfig,
		},
	}

	deleteResult, err := azurePlugin.Delete(ctx, deleteReq)

	// Assert: Expect InProgress status with RequestID
	assert.NoError(t, err)
	assert.NotNil(t, deleteResult)
	assert.NotNil(t, deleteResult.ProgressResult)
	assert.Equal(t, resource.OperationDelete, deleteResult.ProgressResult.Operation)
	assert.Equal(t, resource.OperationStatusInProgress, deleteResult.ProgressResult.OperationStatus)
	assert.NotEmpty(t, deleteResult.ProgressResult.RequestID, "RequestID should be set for async operations")
	t.Logf("Delete started with RequestID: %s", deleteResult.ProgressResult.RequestID)

	// Poll Status until operation completes
	maxPolls := 60 // With 500ms interval, this gives 30 seconds max
	pollInterval := 500 * time.Millisecond
	var finalStatus resource.OperationStatus
	var statusResult *resource.StatusResult

	for i := 0; i < maxPolls; i++ {
		statusReq := &resource.StatusRequest{
			RequestID:    deleteResult.ProgressResult.RequestID,
			ResourceType: "Azure::Resources::ResourceGroup",
			Target: &model.Target{
				Namespace: "Azure",
				Config:    targetConfig,
			},
		}

		statusResult, err = azurePlugin.Status(ctx, statusReq)
		assert.NoError(t, err)
		assert.NotNil(t, statusResult)
		assert.NotNil(t, statusResult.ProgressResult)

		finalStatus = statusResult.ProgressResult.OperationStatus
		t.Logf("Poll %d: Status = %s, ErrorCode = %s", i+1, finalStatus, statusResult.ProgressResult.ErrorCode)

		if finalStatus == resource.OperationStatusSuccess || finalStatus == resource.OperationStatusFailure {
			// Operation completed - check error code
			if finalStatus == resource.OperationStatusFailure {
				// For Delete operations, NotFound error code means success
				// (The PluginOperator interprets this, but we test it here directly)
				assert.Equal(t, resource.OperationErrorCodeNotFound, statusResult.ProgressResult.ErrorCode,
					"Delete should complete with NotFound error code (resource no longer exists)")
			}
			break
		}

		// Should be InProgress while deleting
		assert.Equal(t, resource.OperationStatusInProgress, finalStatus, "Status should be InProgress while operation is ongoing")

		time.Sleep(pollInterval)
	}

	// Assert: Final status should be Failure with NotFound error code
	// The PluginOperator will interpret NotFound as Success for Delete operations
	assert.Equal(t, resource.OperationStatusFailure, finalStatus, "Status should return Failure when resource not found")
	assert.Equal(t, resource.OperationErrorCodeNotFound, statusResult.ProgressResult.ErrorCode,
		"Error code should be NotFound when resource is deleted")
	t.Logf("Delete completed - Status returned Failure with NotFound (expected for deleted resources)")

	// Verify resource is actually deleted from Azure
	_, err = rgClient.Get(ctx, rgName, nil)
	assert.Error(t, err, "Resource group should not exist after deletion")
	t.Logf("Verified resource group deleted from Azure")
}

func TestAzureUpdate_Integration(t *testing.T) {
	ctx := context.Background()

	// Create unique resource group name for this test
	rgName := fmt.Sprintf("formae-test-rg-update-%d", time.Now().Unix())

	// Step 1: Create resource group using Azure SDK with initial tags
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	assert.NoError(t, err)
	rgClient, err := armresources.NewResourceGroupsClient(testSubscriptionID, cred, nil)
	assert.NoError(t, err)

	// Create with initial tags
	location := "westus"
	initialTags := map[string]*string{
		"environment": stringPtr("dev"),
		"test":        stringPtr("formae-update-test"),
	}
	params := armresources.ResourceGroup{
		Location: &location,
		Tags:     initialTags,
	}

	createResult, err := rgClient.CreateOrUpdate(ctx, rgName, params, nil)
	assert.NoError(t, err)
	assert.NotNil(t, createResult.ID)
	nativeID := *createResult.ID
	t.Logf("Created resource group: %s with ID: %s", rgName, nativeID)

	// Verify initial tags
	t.Logf("Initial tags: environment=dev, test=formae-update-test")

	// Cleanup: Ensure resource group is deleted even if test fails
	defer func() {
		poller, err := rgClient.BeginDelete(ctx, rgName, nil)
		if err != nil {
			t.Logf("Failed to start deletion: %v", err)
			return
		}
		_, err = poller.PollUntilDone(ctx, nil)
		if err != nil {
			t.Logf("Failed to complete deletion: %v", err)
		} else {
			t.Logf("Cleaned up resource group: %s", rgName)
		}
	}()

	// Step 2: Prepare target configuration for plugin
	targetConfig := fmt.Appendf(nil, `{"SubscriptionId":"%s"}`, testSubscriptionID)

	// Step 3: Prepare updated properties (modify existing tag, add new tag, remove old tag)
	// We're updating: environment: dev -> prod, adding: purpose=testing, keeping: test
	updatedProperties := []byte(`{
		"location": "westus",
		"tags": {
			"environment": "prod",
			"test": "formae-update-test",
			"purpose": "testing"
		}
	}`)

	// Step 4: Call Azure plugin Update
	updateReq := &resource.UpdateRequest{
		Resource: &model.Resource{
			Type:       "Azure::Resources::ResourceGroup",
			Label:      rgName,
			Stack:      "test-stack",
			Properties: updatedProperties,
		},
		Target: &model.Target{
			Namespace: "Azure",
			Config:    targetConfig,
		},
		NativeID: &nativeID,
	}

	result, err := azurePlugin.Update(ctx, updateReq)

	// Assert: Expect success
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationUpdate, result.ProgressResult.Operation)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)
	assert.Equal(t, nativeID, result.ProgressResult.NativeID)
	assert.Equal(t, "Azure::Resources::ResourceGroup", result.ProgressResult.ResourceType)
	t.Logf("Update completed successfully")

	// Step 5: Verify tags were updated correctly by reading from Azure
	updatedRG, err := rgClient.Get(ctx, rgName, nil)
	assert.NoError(t, err)
	assert.NotNil(t, updatedRG.Tags)

	// Verify updated tag values
	assert.Equal(t, "prod", *updatedRG.Tags["environment"], "environment tag should be updated to prod")
	assert.Equal(t, "formae-update-test", *updatedRG.Tags["test"], "test tag should remain unchanged")
	assert.Equal(t, "testing", *updatedRG.Tags["purpose"], "purpose tag should be added")
	t.Logf("Verified updated tags: %v", updatedRG.Tags)
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}
