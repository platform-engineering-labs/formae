// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"encoding/json"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Handles loading and unloading for various resource plugins

const Resource Type = "resource"

// Must remain stateless to ensure non flaky restarts and hot reloads
type ResourcePlugin interface {
	Namespace() string
	SupportedResources() []ResourceDescriptor
	SchemaForResourceType(resourceType string) (model.Schema, error)
	MaxRequestsPerSecond() int

	Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error)
	Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error)
	Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error)

	Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error)

	List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error)

	TargetBehavior() resource.TargetBehavior

	// GetResourceFilters returns a map of resource types to filter functions
	// If a resource type is in the map, the filter function will be called to determine
	// if the resource should be filtered out during discovery
	GetResourceFilters() map[string]ResourceFilter
}

// ResourceFilter is a function that determines if a resource should be filtered. It
// returns true if the resource should be excluded from discovery.
type ResourceFilter func(properties json.RawMessage, target  model.Target) bool

// used in tests to simulate testable behaviour
const ResourcePluginOverridesContextKey = "resource-plugin-overrides"

type ResourcePluginOverrides struct {
	Create func(request *resource.CreateRequest) (*resource.CreateResult, error)
	Update func(request *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete func(request *resource.DeleteRequest) (*resource.DeleteResult, error)
	Read   func(request *resource.ReadRequest) (*resource.ReadResult, error)
	Status func(request *resource.StatusRequest) (*resource.StatusResult, error)
	List   func(request *resource.ListRequest) (*resource.ListResult, error)
}
