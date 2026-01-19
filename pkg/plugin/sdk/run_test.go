// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package sdk

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// mockPlugin is a minimal ResourcePlugin implementation for testing
type mockPlugin struct{}

func (m *mockPlugin) RateLimit() plugin.RateLimitConfig {
	return plugin.RateLimitConfig{
		Scope:                            plugin.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 10,
	}
}

func (m *mockPlugin) DiscoveryFilters() []plugin.MatchFilter {
	return nil
}

func (m *mockPlugin) LabelConfig() plugin.LabelConfig {
	return plugin.LabelConfig{DefaultQuery: "$.Name"}
}

func (m *mockPlugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	return nil, nil
}

func (m *mockPlugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	return nil, nil
}

func (m *mockPlugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return nil, nil
}

func (m *mockPlugin) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	return nil, nil
}

func (m *mockPlugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	return nil, nil
}

func (m *mockPlugin) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	return nil, nil
}

// getTestPaths returns paths to the test plugin directories relative to this file.
// This allows the test to work regardless of the working directory.
//
// Note: Uses AWS plugin for testing because PKL's import*() glob pattern has limitations
// with local packages that prevent the minimal test-plugin fixture from working.
// The AWS plugin will be externalized eventually, at which point this test should be
// updated to use a self-contained test fixture.
func getTestPaths() (pluginDir, formaeSchemaPath string) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("Failed to get current file path")
	}
	sdkDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(sdkDir, "..", "..", "..")

	// Use AWS plugin for now (has working schema extraction)
	// TODO: Replace with self-contained test plugin once PKL import* glob issue is resolved
	pluginDir = filepath.Join(repoRoot, "plugins", "aws")
	formaeSchemaPath = filepath.Join(repoRoot, "plugins", "pkl", "schema", "PklProject")

	return pluginDir, formaeSchemaPath
}

func TestSetupPluginFromDir_CreatesFullResourcePlugin(t *testing.T) {
	pluginDir, formaeSchemaPath := getTestPaths()

	wrapped, err := SetupPluginFromDir(context.Background(), &mockPlugin{}, pluginDir, RunConfig{
		FormaeSchemaPath: formaeSchemaPath,
	})
	require.NoError(t, err)

	// Verify identity methods come from manifest
	assert.Equal(t, "aws", wrapped.Name())
	assert.NotNil(t, wrapped.Version())
	assert.Equal(t, "AWS", wrapped.Namespace())

	// Verify schema methods return data
	resources := wrapped.SupportedResources()
	assert.NotEmpty(t, resources, "SupportedResources should return at least some resources")
	assert.Greater(t, len(resources), 100, "AWS plugin should have 100+ resources")

	// Verify we can get schema for a known resource type
	schema, err := wrapped.SchemaForResourceType("AWS::EC2::VPC")
	require.NoError(t, err)
	assert.NotEmpty(t, schema.Identifier, "Schema should have an identifier")
	assert.NotEmpty(t, schema.Fields, "Schema should have fields")

	// Verify config methods are delegated to the plugin
	assert.Equal(t, plugin.RateLimitScopeNamespace, wrapped.RateLimit().Scope)
	assert.Equal(t, 10, wrapped.RateLimit().MaxRequestsPerSecondForNamespace)
	assert.Equal(t, "$.Name", wrapped.LabelConfig().DefaultQuery)
}

func TestSetupPluginFromDir_ReturnsErrorForMissingManifest(t *testing.T) {
	_, formaeSchemaPath := getTestPaths()

	_, err := SetupPluginFromDir(context.Background(), &mockPlugin{}, "/nonexistent/path", RunConfig{
		FormaeSchemaPath: formaeSchemaPath,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read manifest")
}

func TestSetupPluginFromDir_ReturnsErrorForInvalidFormaeSchemaPath(t *testing.T) {
	pluginDir, _ := getTestPaths()

	_, err := SetupPluginFromDir(context.Background(), &mockPlugin{}, pluginDir, RunConfig{
		FormaeSchemaPath: "/nonexistent/path/PklProject",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to extract schemas")
}
