// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package azure

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
