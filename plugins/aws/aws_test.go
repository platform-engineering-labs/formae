// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"slices"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/stretchr/testify/assert"
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
