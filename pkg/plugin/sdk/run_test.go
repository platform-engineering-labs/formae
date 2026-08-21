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

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// mockPlugin is a minimal ResourcePlugin implementation for testing
type mockPlugin struct{}

func (m *mockPlugin) RateLimit() pkgmodel.RateLimitConfig {
	return pkgmodel.RateLimitConfig{
		Scope:                            pkgmodel.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 10,
	}
}

func (m *mockPlugin) DiscoveryFilters() []pkgmodel.MatchFilter {
	return nil
}

func (m *mockPlugin) LabelConfig() pkgmodel.LabelConfig {
	return pkgmodel.LabelConfig{DefaultQuery: "$.Name"}
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
func getTestPaths() (pluginDir, formaeSchemaPath string) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("Failed to get current file path")
	}
	sdkDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(sdkDir, "..", "..", "..")

	pluginDir = filepath.Join(repoRoot, "internal", "testplugin", "fakeaws")
	formaeSchemaPath = filepath.Join(repoRoot, "internal", "schema", "pkl", "schema", "PklProject")

	return pluginDir, formaeSchemaPath
}

func TestSetupPluginFromDir_CreatesFullResourcePlugin(t *testing.T) {
	pluginDir, formaeSchemaPath := getTestPaths()

	wrapped, err := SetupPluginFromDir(context.Background(), &mockPlugin{}, pluginDir, RunConfig{
		FormaeSchemaPath: formaeSchemaPath,
	})
	require.NoError(t, err)

	// Verify identity methods come from manifest
	assert.Equal(t, "fake-aws", wrapped.Name())
	assert.NotNil(t, wrapped.Version())
	assert.Equal(t, "FakeAWS", wrapped.Namespace())

	// Verify schema methods return data
	resources := wrapped.SupportedResources()
	assert.NotEmpty(t, resources, "SupportedResources should return at least some resources")

	// Verify we can get schema for a known resource type
	schema, err := wrapped.SchemaForResourceType("FakeAWS::EC2::VPC")
	require.NoError(t, err)
	assert.NotEmpty(t, schema.Identifier, "Schema should have an identifier")
	assert.NotEmpty(t, schema.Fields, "Schema should have fields")

	// Verify config methods are delegated to the plugin
	assert.Equal(t, 10, wrapped.RateLimit().MaxRequestsPerSecondForNamespace)
	assert.Equal(t, "$.Name", wrapped.LabelConfig().DefaultQuery)
}

// oidcAwarePlugin records every OidcTokenSource installed on it.
type oidcAwarePlugin struct {
	mockPlugin
	installs []plugin.OidcTokenSource
}

func (p *oidcAwarePlugin) SetOidcTokenSource(src plugin.OidcTokenSource) {
	p.installs = append(p.installs, src)
}

func TestConfigureWrapped_InstallsSourceOnceAndForwards(t *testing.T) {
	inner := &oidcAwarePlugin{}
	manifest := &plugin.Manifest{Name: "fake-aws", Version: "1.0.0", Namespace: "FakeAWS"}
	wrapped, err := plugin.WrapPlugin(inner, manifest, nil, nil)
	require.NoError(t, err)

	require.NoError(t, configureWrapped(wrapped, manifest))

	require.Len(t, inner.installs, 1, "the SDK must install exactly one token source")
	// The installed source reads the broker client off the per-call context, so
	// outside an operation it has nothing to call.
	_, err = inner.installs[0].IdentityToken(context.Background(), "sts.amazonaws.com")
	assert.ErrorIs(t, err, plugin.ErrNoOidcBroker)
}

func TestSetupPluginFromDir_InstallsOidcTokenSource(t *testing.T) {
	pluginDir, formaeSchemaPath := getTestPaths()
	inner := &oidcAwarePlugin{}

	_, err := SetupPluginFromDir(context.Background(), inner, pluginDir, RunConfig{
		FormaeSchemaPath: formaeSchemaPath,
	})
	require.NoError(t, err)

	assert.Len(t, inner.installs, 1)
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
