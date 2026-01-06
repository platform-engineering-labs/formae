// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package descriptors

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractSchemaFromDependencies(t *testing.T) {
	// Get the path to local PKL schemas
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "Failed to get current file path")

	// Navigate from pkg/plugin/descriptors to plugins/
	baseDir := filepath.Join(filepath.Dir(thisFile), "../../..")

	deps := []Dependency{
		{Name: "formae", Value: filepath.Join(baseDir, "plugins/pkl/schema/PklProject")},
		{Name: "aws", Value: filepath.Join(baseDir, "plugins/aws/schema/pkl/PklProject")},
	}

	ctx := context.Background()
	descriptors, err := ExtractSchemaFromDependencies(ctx, deps)
	require.NoError(t, err, "ExtractSchemaFromDependencies should not return an error")
	require.NotEmpty(t, descriptors, "Expected at least one resource descriptor")

	t.Logf("Extracted %d resource descriptors", len(descriptors))

	// Verify we got a reasonable number of AWS resources
	assert.Greater(t, len(descriptors), 100, "Expected more than 100 AWS resource descriptors")

	// Verify specific resources exist
	var foundS3Bucket, foundEC2VPC bool
	for _, desc := range descriptors {
		switch desc.Type {
		case "AWS::S3::Bucket":
			foundS3Bucket = true
		case "AWS::EC2::VPC":
			foundEC2VPC = true
		}
	}

	assert.True(t, foundS3Bucket, "Expected to find AWS::S3::Bucket descriptor")
	assert.True(t, foundEC2VPC, "Expected to find AWS::EC2::VPC descriptor")
}
