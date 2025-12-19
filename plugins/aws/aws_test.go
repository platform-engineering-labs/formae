// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/descriptors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAWSPluginResourceTypes(t *testing.T) {
	t.Log("Testing AWS Plugin...")

	aws := AWS{}

	t.Logf("Plugin Name: %s", aws.Name())
	t.Logf("Plugin Namespace: %s", aws.Namespace())
	t.Logf("Plugin Version: %s", aws.Version().String())

	resources := aws.SupportedResources()
	t.Logf("Loaded %d resource descriptors:", len(resources))

	recordSetIndex := slices.IndexFunc(resources, func(r plugin.ResourceDescriptor) bool {
		return r.Type == "AWS::Route53::RecordSet"
	})
	recordSetDescriptor := resources[recordSetIndex]

	assert.Len(t, recordSetDescriptor.ParentResourceTypesWithMappingProperties, 1)

	if len(resources) <= 100 {
		t.Errorf("Expected more than 100 resource descriptors, got %d", len(resources))
	}

	for i, resource := range resources {
		schema, err := aws.SchemaForResourceType(resource.Type)
		assert.NoError(t, err, "Expected no error when getting schema for resource type %s", resource.Type)
		t.Logf("  %d. %s \tSchema {Identifier=%s, Tags=%s, Fields=%s}", i+1, resource.Type, schema.Identifier, schema.Tags, schema.Fields)
	}

	if len(resources) == 0 {
		t.Log("Warning: No resource descriptors loaded!")
	}

	s3BucketSchema, err := aws.SchemaForResourceType("AWS::S3::Bucket")
	assert.NoError(t, err, "Expected no error when getting schema for AWS::S3::Bucket")
	assert.NotNil(t, s3BucketSchema, "Expected schema for AWS::S3::Bucket to be non-nil")
	assert.Equal(t, "BucketName", s3BucketSchema.Identifier)
}

func TestExtractSchemaFromAWSPkl(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "Failed to get current file path")

	awsSchemaPath := filepath.Join(filepath.Dir(thisFile), "schema", "pkl", "PklProject")

	ctx := context.Background()

	//TODO need to fix formae version here
	dependencies := []descriptors.Dependency{
		{Name: "formae", Value: "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@0.75.1"},
		{Name: "aws", Value: awsSchemaPath},
	}

	extractedDescriptors, err := descriptors.ExtractSchema(ctx, dependencies)
	require.NoError(t, err, "ExtractSchema should not return an error")
	require.NotEmpty(t, extractedDescriptors, "Expected at least one resource descriptor")

	t.Logf("Extracted %d resource descriptors from AWS schema", len(extractedDescriptors))

	assert.Greater(t, len(extractedDescriptors), 100, "Expected more than 100 AWS resource descriptors")

	var foundS3Bucket, foundEC2VPC, foundLambdaFunction bool
	for _, desc := range extractedDescriptors {
		switch desc.Type {
		case "AWS::S3::Bucket":
			foundS3Bucket = true
			assert.Equal(t, "BucketName", desc.Schema.Identifier)
		case "AWS::EC2::VPC":
			foundEC2VPC = true
		case "AWS::Lambda::Function":
			foundLambdaFunction = true
		}
	}

	assert.True(t, foundS3Bucket, "Expected to find AWS::S3::Bucket descriptor")
	assert.True(t, foundEC2VPC, "Expected to find AWS::EC2::VPC descriptor")
	assert.True(t, foundLambdaFunction, "Expected to find AWS::Lambda::Function descriptor")
}
