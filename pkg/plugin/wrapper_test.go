// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// mockPlugin is a minimal ResourcePlugin implementation for testing
type mockPlugin struct{}

func (m *mockPlugin) RateLimit() RateLimitConfig {
	return RateLimitConfig{
		Scope:                            RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 10,
	}
}

func (m *mockPlugin) DiscoveryFilters() []MatchFilter {
	return nil
}

func (m *mockPlugin) LabelConfig() LabelConfig {
	return LabelConfig{DefaultQuery: "$.Name"}
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

func TestWrapPlugin_CreatesFullResourcePlugin(t *testing.T) {
	manifest := &Manifest{
		Name:             "test-plugin",
		Version:          "1.2.3",
		Namespace:        "Test",
		License:          "MIT",
		MinFormaeVersion: "0.80.0",
	}

	descriptors := []ResourceDescriptor{
		{Type: "Test::Resource::One"},
		{Type: "Test::Resource::Two"},
	}

	schemas := map[string]model.Schema{
		"Test::Resource::One": {Identifier: "Name", Fields: []string{"Name", "Tags"}},
		"Test::Resource::Two": {Identifier: "Value", Fields: []string{"Value"}},
	}

	wrapped, err := WrapPlugin(&mockPlugin{}, manifest, descriptors, schemas)
	require.NoError(t, err)

	// Verify identity methods
	assert.Equal(t, "test-plugin", wrapped.Name())
	assert.Equal(t, "1.2.3", wrapped.Version().String())
	assert.Equal(t, "Test", wrapped.Namespace())

	// Verify schema methods
	assert.Equal(t, descriptors, wrapped.SupportedResources())

	schema1, err := wrapped.SchemaForResourceType("Test::Resource::One")
	require.NoError(t, err)
	assert.Equal(t, schemas["Test::Resource::One"], schema1)

	schema2, err := wrapped.SchemaForResourceType("Test::Resource::Two")
	require.NoError(t, err)
	assert.Equal(t, schemas["Test::Resource::Two"], schema2)

	// Verify config methods are delegated
	assert.Equal(t, RateLimitScopeNamespace, wrapped.RateLimit().Scope)
	assert.Equal(t, 10, wrapped.RateLimit().MaxRequestsPerSecondForNamespace)
	assert.Equal(t, "$.Name", wrapped.LabelConfig().DefaultQuery)
}

func TestWrapPlugin_ReturnsErrorForInvalidVersion(t *testing.T) {
	manifest := &Manifest{
		Name:             "test-plugin",
		Version:          "not-a-version",
		Namespace:        "Test",
		License:          "MIT",
		MinFormaeVersion: "0.80.0",
	}

	_, err := WrapPlugin(&mockPlugin{}, manifest, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid version")
}

func TestWrapPlugin_SchemaForResourceType_ReturnsEmptyForUnknownType(t *testing.T) {
	manifest := &Manifest{
		Name:             "test-plugin",
		Version:          "1.0.0",
		Namespace:        "Test",
		License:          "MIT",
		MinFormaeVersion: "0.80.0",
	}

	wrapped, err := WrapPlugin(&mockPlugin{}, manifest, nil, nil)
	require.NoError(t, err)

	schema, err := wrapped.SchemaForResourceType("Unknown::Type")
	require.NoError(t, err)
	assert.Equal(t, model.Schema{}, schema)
}
