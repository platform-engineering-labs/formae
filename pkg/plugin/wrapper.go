// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"fmt"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// pluginWrapper wraps a user's ResourcePlugin implementation with manifest-derived
// identity methods and auto-extracted schemas to produce a FullResourcePlugin.
type pluginWrapper struct {
	// The user's plugin implementation
	plugin ResourcePlugin

	// From manifest
	name      string
	version   *semver.Version
	namespace string

	// Auto-extracted from schema directory
	descriptors         []ResourceDescriptor
	resourceTypeSchemas map[string]model.Schema
}

// WrapPlugin creates a FullResourcePlugin from a ResourcePlugin using the provided
// manifest and schema information.
func WrapPlugin(
	p ResourcePlugin,
	manifest *Manifest,
	descriptors []ResourceDescriptor,
	schemas map[string]model.Schema,
) (FullResourcePlugin, error) {
	v, err := semver.NewVersion(manifest.Version)
	if err != nil {
		return nil, fmt.Errorf("invalid version in manifest: %w", err)
	}
	return &pluginWrapper{
		plugin:              p,
		name:                manifest.Name,
		version:             v,
		namespace:           manifest.Namespace,
		descriptors:         descriptors,
		resourceTypeSchemas: schemas,
	}, nil
}

// Identity methods - from manifest

func (w *pluginWrapper) Name() string {
	return w.name
}

func (w *pluginWrapper) Version() *semver.Version {
	return w.version
}

func (w *pluginWrapper) Namespace() string {
	return w.namespace
}

// Schema methods - from auto-extraction

func (w *pluginWrapper) SupportedResources() []ResourceDescriptor {
	return w.descriptors
}

func (w *pluginWrapper) SchemaForResourceType(resourceType string) (model.Schema, error) {
	if schema, ok := w.resourceTypeSchemas[resourceType]; ok {
		return schema, nil
	}
	return model.Schema{}, nil
}

// Configuration methods - delegated to user's plugin

func (w *pluginWrapper) RateLimit() RateLimitConfig {
	return w.plugin.RateLimit()
}

func (w *pluginWrapper) DiscoveryFilters() []MatchFilter {
	return w.plugin.DiscoveryFilters()
}

func (w *pluginWrapper) LabelConfig() LabelConfig {
	return w.plugin.LabelConfig()
}

// CRUD operations - delegated to user's plugin

func (w *pluginWrapper) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	return w.plugin.Create(ctx, req)
}

func (w *pluginWrapper) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	return w.plugin.Read(ctx, req)
}

func (w *pluginWrapper) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return w.plugin.Update(ctx, req)
}

func (w *pluginWrapper) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	return w.plugin.Delete(ctx, req)
}

func (w *pluginWrapper) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	return w.plugin.Status(ctx, req)
}

func (w *pluginWrapper) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	return w.plugin.List(ctx, req)
}
