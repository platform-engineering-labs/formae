// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/google/uuid"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/status"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

type testState struct {
	NativeID    string
	awsProvider AWS
}

var state *testState

const TestNativeIDOverride = "Test"

func TestMain(m *testing.M) {
	var err error
	state = &testState{
		awsProvider: AWS{},
	}

	// Check for debug override
	if nativeID := TestNativeIDOverride; nativeID != "" {
		state.NativeID = nativeID
		os.Exit(m.Run())
	}

	// Create two test resources
	req := &resource.CreateRequest{
		Resource: &model.Resource{
			Label: "pel-zone",
			Schema: model.Schema{
				Identifier: "Id",
				Hints: map[string]model.FieldHint{
					"Name": {
						CreateOnly: true,
					},
				},
				Fields: []string{"HostedZoneConfig", "HostedZoneTags", "Name", "QueryLoggingConfig", "VPCs"},
			},
			Type:       "AWS::Route53::HostedZone",
			Stack:      "pel-dns",
			Properties: []byte(`{"HostedZoneTags":[{"Key":"FormaeResourceLabel","Value":"pel-zone-1"},{"Key":"FormaeStackLabel","Value":"pel-dns"}],"Name":"test.integration.aws."}`),
		},
	}

	res, err := state.awsProvider.Create(context.Background(), req)
	if err != nil {
		log.Printf("Failed to create test resource: %v\n", err)
		os.Exit(1)
	}

	success := waitForCondition(func() bool {
		status, err := state.awsProvider.Status(context.Background(), &resource.StatusRequest{
			RequestID: res.ProgressResult.RequestID,
		})
		if err != nil {
			log.Printf("Failed to get status: %v\n", err)
			return false
		}

		if status.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			state.NativeID = status.ProgressResult.NativeID
			return true
		}
		return false
	}, 10*time.Second, 1*time.Second)

	if !success {
		log.Println("Timeout waiting for resource creation")
		os.Exit(1)
	}
	// Run tests
	code := m.Run()

	// Cleanup unless using debug override
	if TestNativeIDOverride == "" {
		deleteResult, err := state.awsProvider.Delete(context.Background(), &resource.DeleteRequest{
			NativeID:     &state.NativeID,
			ResourceType: "AWS::Route53::HostedZone",
		})
		if err != nil {
			log.Printf("Failed to delete test resource: %v\n", err)
			os.Exit(1)
		}

		// Wait for deletion
		success = false
		for i := 0; i < 10; i++ {
			statusResult, err := state.awsProvider.Status(context.Background(), &resource.StatusRequest{
				RequestID: deleteResult.ProgressResult.RequestID,
			})
			if err != nil {
				log.Printf("Failed to get delete status: %v\n", err)
				os.Exit(1)
			}
			if statusResult.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
				success = true
				break
			}
			time.Sleep(5 * time.Second)
		}
	}

	os.Exit(code)
}

func waitForOperation(provider AWS, requestID string) (string, bool, error) {
	for i := 0; i < 10; i++ {
		statusResult, err := provider.Status(context.Background(), &resource.StatusRequest{
			RequestID: requestID,
		})
		if err != nil {
			return "", false, fmt.Errorf("failed to get status: %w", err)
		}

		switch statusResult.ProgressResult.OperationStatus {
		case resource.OperationStatusFailure:
			return "", false, nil
		case resource.OperationStatusSuccess:
			return statusResult.ProgressResult.NativeID, true, nil
		}

		time.Sleep(5 * time.Second)
	}

	return "", false, fmt.Errorf("timeout waiting for operation completion")
}

func waitForCondition(predicate func() bool, waitFor time.Duration, tick time.Duration) bool {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	timeout := time.After(waitFor)
	for {
		select {
		case <-timeout:
			return false
		case <-ticker.C:
			if predicate() {
				return true
			}
		}
	}
}

func TestAWSCreate_Integration(t *testing.T) {
	req := &resource.CreateRequest{
		Resource: &model.Resource{
			Label:      "pel-zone",
			Stack:      "pel-dns",
			Properties: []byte(`{"Name": "test-integration.integration.aws."}`),
			Type:       "AWS::Route53::HostedZone",
		},
	}

	awsProvider := AWS{}
	result, err := awsProvider.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("AWS Create failed: %v", err)
	}

	// Verify that the CreateResult has a valid operation status and request token.
	if result.ProgressResult == nil {
		t.Fatal("expected a non-nil ProgressResult")
	}
	if result.ProgressResult.Operation != resource.OperationCreate {
		t.Errorf("expected operation %s, got %s", resource.OperationCreate, result.ProgressResult.Operation)
	}

	if result.ProgressResult.OperationStatus != status.FromOperationStatus("IN_PROGRESS") {
		t.Errorf("expected operation status 'IN_PROGRESS', got %s", result.ProgressResult.OperationStatus)
	}
	if result.ProgressResult.RequestID == "" {
		t.Error("expected non-empty RequestID")
	}

	t.Logf("Created AWS resource: %+v", result.ProgressResult)

	var statusResult *resource.StatusResult
	success := false

	// Without this sleep it fails immediately
	time.Sleep(3 * time.Second)
	for range 10 {
		statusResult, err = awsProvider.Status(context.Background(), &resource.StatusRequest{RequestID: result.ProgressResult.RequestID})
		t.Logf("Status result of Create Operation: %+v", statusResult)
		if err != nil {
			t.Fatalf("AWS Status failed: %v", err)
		}
		if statusResult.ProgressResult.OperationStatus == resource.OperationStatusFailure || statusResult.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			success = true
			break
		}
		time.Sleep(5 * time.Second)
	}
	assert.True(t, success)

	res, err := awsProvider.List(context.Background(), &resource.ListRequest{ResourceType: "AWS::Route53::HostedZone"})

	if err != nil {
		t.Fatalf("AWS List failed: %v", err)
	}

	//assert that the list is not empty
	if len(res.Resources) == 0 {
		t.Fatalf("expected non-empty list of resources")
	}

	t.Logf("Integration Create status result: %+v", statusResult)

	_, err = awsProvider.Read(context.Background(), &resource.ReadRequest{NativeID: statusResult.ProgressResult.NativeID, ResourceType: statusResult.ProgressResult.ResourceType})
	assert.NoError(t, err)

	t.Logf("Integration Get result: %+v", res.Resources[0])
	deleteResult, err := awsProvider.Delete(context.Background(), &resource.DeleteRequest{NativeID: &statusResult.ProgressResult.NativeID, ResourceType: statusResult.ProgressResult.ResourceType})
	t.Logf("Integration Delete result: %+v", deleteResult)
	assert.NoError(t, err)

	success = false
	for range 10 {
		statusResult, err = awsProvider.Status(context.Background(), &resource.StatusRequest{RequestID: deleteResult.ProgressResult.RequestID})
		t.Logf("Status result of Delete Operation: %+v", statusResult)
		if err != nil {

			t.Fatalf("AWS Status failed: %v", err)
		}
		if statusResult.ProgressResult.OperationStatus == status.FromOperationStatus("SUCCESS") || statusResult.ProgressResult.OperationStatus == status.FromOperationStatus("FAILED") {
			success = true
			break
		}
		time.Sleep(5 * time.Second)
	}
	assert.True(t, success)

}

func TestAWS_UpdateLogGroupRetentionDays(t *testing.T) {

	resourceLg := model.Resource{
		Label: "pel-test-lg",
		Schema: model.Schema{
			Identifier: "Arn",
			Fields:     []string{"DataProtectionPolicy", "FieldIndexPolicies", "KmsKeyId", "LogGroupClass", "LogGroupName", "RetentionInDays", "Tags"},
		},
		Type:       "AWS::Logs::LogGroup",
		Stack:      "pel-test-lgs",
		Properties: []byte(`{"LogGroupClass":"STANDARD","LogGroupName":"test.group","RetentionInDays":3,"Tags":[{"Key":"FormaeResourceLabel","Value":"lg-group"},{"Key":"FormaeStackLabel","Value":"pel-lgs"}]}`),
	}

	res, err := state.awsProvider.Create(context.Background(), &resource.CreateRequest{
		Resource: &resourceLg,
	})

	time.Sleep(3 * time.Second)

	NativeID, success, err := waitForOperation(state.awsProvider, res.ProgressResult.RequestID)
	assert.NoError(t, err)
	assert.True(t, success)
	assert.NotEqual(t, "", NativeID)

	resourceLgUpdate := model.Resource{
		Label: "pel-test-lg",
		Schema: model.Schema{
			Identifier: "Arn",
			Fields:     []string{"DataProtectionPolicy", "FieldIndexPolicies", "KmsKeyId", "LogGroupClass", "LogGroupName", "RetentionInDays", "Tags"},
		},
		Type:       "AWS::Logs::LogGroup",
		Stack:      "pel-test-lgs",
		Properties: []byte(`{"LogGroupClass":"STANDARD","LogGroupName":"test.group","RetentionInDays":7,"Tags":[{"Key":"FormaeResourceLabel","Value":"lg-group"},{"Key":"FormaeStackLabel","Value":"pel-lgs"}]}`),
	}

	updateRes, err := state.awsProvider.Update(context.Background(), &resource.UpdateRequest{
		Resource: &resourceLgUpdate,
		NativeID: &NativeID,
	})

	time.Sleep(3 * time.Second)
	NativeID, success, err = waitForOperation(state.awsProvider, updateRes.ProgressResult.RequestID)
	assert.NoError(t, err)
	assert.True(t, success)
	assert.NotEqual(t, "", NativeID)

	deleteRes, err := state.awsProvider.Delete(context.Background(), &resource.DeleteRequest{
		NativeID:     &NativeID,
		ResourceType: "AWS::Logs::LogGroup",
	})
	assert.NoError(t, err)
	NativeID, success, err = waitForOperation(state.awsProvider, deleteRes.ProgressResult.RequestID)
	assert.NoError(t, err)
	assert.True(t, success)
}

func TestAWS_UpdateSQSwithTheoreticalReplace(t *testing.T) {

	resourceSQS := model.Resource{
		Label: "pel-test-sqs",
		Schema: model.Schema{
			Identifier: "Arn",
			Hints: map[string]model.FieldHint{
				"FifoQueue": {
					CreateOnly: true,
				},
				"QueueName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"ContentBasedDeduplication", "DeduplicationScope", "DelaySeconds", "FifoQueue", "FifoThroughputLimit", "KmsDataKeyReusePeriodSeconds", "KmsMasterKeyId", "MaximumMessageSize", "MessageRetentionPeriod", "QueueName", "ReceiveMessageWaitTimeSeconds", "RedriveAllowPolicy", "RedrivePolicy", "SqsManagedSseEnabled", "Tags", "VisibilityTimeout"},
		},
		Type:       "AWS::SQS::Queue",
		Stack:      "pel-test-sqs",
		Properties: []byte(`{"QueueName": "pel-test-replace","Tags":[{"Key":"FormaeResourceLabel","Value":"sqs-queue"},{"Key":"FormaeStackLabel","Value":"pel-queue"}]}`),
	}

	res, err := state.awsProvider.Create(context.Background(), &resource.CreateRequest{
		Resource: &resourceSQS,
	})
	assert.NoError(t, err)
	t.Logf("Create SQS result: %+v", res)

	time.Sleep(3 * time.Second)

	t.Logf("Waiting SQS create status: %+v", res)
	NativeID, success, err := waitForOperation(state.awsProvider, res.ProgressResult.RequestID)
	t.Logf("SQS created  with NativeID: %s and status %t", NativeID, success)
	assert.NoError(t, err)
	assert.True(t, success)
	assert.NotEqual(t, "", NativeID)

	resourceSQSreplace := model.Resource{
		Label: "pel-test-sqs",
		Schema: model.Schema{
			Identifier: "Arn",
			Hints: map[string]model.FieldHint{
				"FifoQueue": {
					CreateOnly: true,
				},
				"QueueName": {
					CreateOnly: true,
				},
			},
			Fields: []string{"ContentBasedDeduplication", "DeduplicationScope", "DelaySeconds", "FifoQueue", "FifoThroughputLimit", "KmsDataKeyReusePeriodSeconds", "KmsMasterKeyId", "MaximumMessageSize", "MessageRetentionPeriod", "QueueName", "ReceiveMessageWaitTimeSeconds", "RedriveAllowPolicy", "RedrivePolicy", "SqsManagedSseEnabled", "Tags", "VisibilityTimeout"},
		},
		Type:       "AWS::SQS::Queue",
		Stack:      "pel-test-sqs",
		Properties: []byte(`{"QueueName": "pel-test-replace-different","Tags":[{"Key":"FormaeResourceLabel","Value":"sqs-queue"},{"Key":"FormaeStackLabel","Value":"pel-queue"}]}`),
	}

	deleteRes, err := state.awsProvider.Delete(context.Background(), &resource.DeleteRequest{
		NativeID:     &NativeID,
		ResourceType: "AWS::SQS::Queue",
	})

	assert.NoError(t, err)
	NativeID, success, err = waitForOperation(state.awsProvider, deleteRes.ProgressResult.RequestID)
	assert.NoError(t, err)
	assert.True(t, success)

	resReplace, err := state.awsProvider.Create(context.Background(), &resource.CreateRequest{
		Resource: &resourceSQSreplace,
	})

	time.Sleep(3 * time.Second)

	ReplaceNativeID, success, err := waitForOperation(state.awsProvider, resReplace.ProgressResult.RequestID)
	assert.NoError(t, err)
	assert.True(t, success)
	assert.NotEqual(t, "", ReplaceNativeID)

	deleteReplaceRes, err := state.awsProvider.Delete(context.Background(), &resource.DeleteRequest{
		NativeID:     &ReplaceNativeID,
		ResourceType: "AWS::SQS::Queue",
	})

	assert.NoError(t, err)
	_, success, err = waitForOperation(state.awsProvider, deleteReplaceRes.ProgressResult.RequestID)
	assert.NoError(t, err)
	assert.True(t, success)

}

func Test_Create(t *testing.T) {
	got, err := state.awsProvider.Status(context.Background(), &resource.StatusRequest{
		RequestID:    "/change/C0000052RK65ETMF6GZW",
		ResourceType: "AWS::Route53::RecordSet",
	})

	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusSuccess, got.ProgressResult.OperationStatus)
}

func TestAWS_ReadErr_ResourceTypeNotFound(t *testing.T) {
	awsProvider := AWS{}

	res, err := awsProvider.Read(context.Background(), &resource.ReadRequest{NativeID: "12345", ResourceType: "AWS::Fake::Resource"})

	assert.Equal(t, res.ErrorCode, resource.OperationErrorCode("NotFound"))
	assert.NotNil(t, err)
}

func TestAWS_ReadErr_NativeIDNotFound(t *testing.T) {
	awsProvider := AWS{}

	res, err := awsProvider.Read(context.Background(), &resource.ReadRequest{NativeID: "hopefully-this-doesnt-exist-12345", ResourceType: "AWS::S3::Bucket"})

	assert.Equal(t, res.ErrorCode, resource.OperationErrorCode("NotFound"))
	assert.NotNil(t, err)
}

func TestAWS_ReadPlugin(t *testing.T) {
	awsProvider := AWS{}

	res, err := awsProvider.Read(context.Background(), &resource.ReadRequest{NativeID: "rtb-0aa0f4c075fbfaa43|0.0.0.32/0", ResourceType: "AWS::EC2::Route", Metadata: []byte(`{"DestinationCidrBlock":"0.0.0.32/0","GatewayID":"igw-0a2b9c796490239f4","RouteTableID":"rtb-0aa0f4c075fbfaa43"}"`), Target: &model.Target{}})

	assert.Equal(t, res.ErrorCode, resource.OperationErrorCode("NotFound"))
	assert.NotNil(t, err)
}

func TestAWS_List(t *testing.T) {
	// This test uses MaxResults to limit the number of resources returned in order to test pagination. In CloudControl,
	// MaxResults acts as a hint to the underlying service calls, not as a strict limit on the number of results returned
	// in a single response. The EC2 API does honor the MaxResults as a strict maximum for the number of results returned in
	// a single response.

	instances := 6
	requestIds := make([]string, instances)
	nativeIds := make([]string, instances)

	for i := range instances {
		id := uuid.New().String()
		res, err := state.awsProvider.Create(context.Background(), ec2InstanceCreateRequest(id))
		assert.NoError(t, err)
		requestIds[i] = res.ProgressResult.RequestID
	}

	assert.Eventually(t, func() bool {
		statuses := make([]*resource.StatusResult, instances)
		for i, requestID := range requestIds {
			status, err := state.awsProvider.Status(context.Background(), &resource.StatusRequest{
				RequestID: requestID,
			})
			assert.NoError(t, err)
			statuses[i] = status
			t.Logf("Status of request %d: %+v, error code: %v", i, status.ProgressResult.OperationStatus, status.ProgressResult.ErrorCode)
			if status.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
				nativeIds[i] = status.ProgressResult.NativeID
			}
		}

		return !slices.ContainsFunc(statuses, func(s *resource.StatusResult) bool {
			return s.ProgressResult.OperationStatus != resource.OperationStatusSuccess
		})
	}, 60*time.Second, 1*time.Second)

	awsProvider := AWS{}

	var resources []resource.Resource
	var nextToken *string
	for {
		res, err := awsProvider.List(context.Background(), &resource.ListRequest{ResourceType: "AWS::EC2::Instance", PageSize: 5, PageToken: nextToken})
		assert.NoError(t, err)
		resources = append(resources, res.Resources...)
		nextToken = res.NextPageToken
		if nextToken == nil || *nextToken == "" {
			break
		}
	}

	assert.Len(t, resources, 6)

	deleteRequestIds := make([]string, instances)
	for i := range instances {
		deleteRes, err := awsProvider.Delete(context.Background(), &resource.DeleteRequest{
			NativeID:     &nativeIds[i],
			ResourceType: "AWS::EC2::Instance",
		})
		assert.NoError(t, err)
		deleteRequestIds[i] = deleteRes.ProgressResult.RequestID
	}

	assert.Eventually(t, func() bool {
		statuses := make([]*resource.StatusResult, len(deleteRequestIds))
		for i, requestID := range deleteRequestIds {
			status, err := awsProvider.Status(context.Background(), &resource.StatusRequest{
				RequestID: requestID,
			})
			assert.NoError(t, err)
			statuses[i] = status
		}
		return !slices.ContainsFunc(statuses, func(s *resource.StatusResult) bool {
			return s.ProgressResult.OperationStatus != resource.OperationStatusSuccess
		})
	}, 60*time.Second, 1*time.Second)
}

func ec2InstanceCreateRequest(id string) *resource.CreateRequest {
	return &resource.CreateRequest{
		Resource: &model.Resource{
			Label: fmt.Sprintf("pel-test-%s", id),
			Schema: model.Schema{
				Identifier: "Name",
					Hints: map[string]model.FieldHint{
					"ImageId": {
						CreateOnly: true,
					},
					"InstanceType": {
						CreateOnly: true,
					},
				},
				Fields: []string{"ImageId", "InstanceType", "Tags"},
			},
			Type:       "AWS::EC2::Instance",
			Stack:      "pel-test-s3",
			Properties: []byte(fmt.Sprintf(`{"ImageId": "%s","InstanceType": "t2.micro", "Tags":[{"Key":"FormaeResourceLabel","Value":"s3-bucket-%x"},{"Key":"FormaeStackLabel","Value":"pel-test"}]}`, getValidAmiId(), id)),
		},
	}
}

func getValidAmiId() string {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	// Get the latest AMI ID from SSM Parameter Store
	name := "/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2"
	ssmClient := ssm.NewFromConfig(cfg)
	paramInput := &ssm.GetParameterInput{
		Name: &name,
	}

	paramResult, err := ssmClient.GetParameter(context.Background(), paramInput)
	if err != nil {
		log.Fatal(err)
	}

	return *paramResult.Parameter.Value
}

/*
func Test_Status(t *testing.T) {

res, err := state.awsProvider.Status(context.Background(), &resource.StatusRequest{
RequestId:    "bc22f15e-bbb5-4677-932a-27a933bac06a",
ResourceType: "AWS::EC2::VPC",
Target:       `{"Config":{"Region":"eu-west-2"}}`,
})

fmt.Println(res.ProgressResult)
fmt.Println(err)
}
*/
