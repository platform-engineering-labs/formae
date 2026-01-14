// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"

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
	Throttling() ThrottlingConfig

	Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error)
	Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error)
	Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error)

	Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error)

	List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error)

	// DiscoveryFilters returns declarative filters for discovery (serializable).
	// Resources matching all conditions in a filter are excluded from discovery.
	DiscoveryFilters() []MatchFilter
}

// ThrottlingScope defines the granularity of rate limiting
type ThrottlingScope string

const (
	// ThrottlingScopeNamespace applies rate limiting at the plugin namespace level (e.g., AWS, Azure)
	ThrottlingScopeNamespace ThrottlingScope = "Namespace"
)

// ThrottlingConfig specifies rate limiting behavior for a plugin
type ThrottlingConfig struct {
	Scope                            ThrottlingScope
	MaxRequestsPerSecondForNamespace int
}

// PluginInfo provides read-only plugin metadata for discovery operations.
// This interface is implemented by both local ResourcePlugin and remote plugin info proxies.
type PluginInfo interface {
	GetNamespace() string
	SupportedResources() []ResourceDescriptor
	SchemaForResourceType(resourceType string) (model.Schema, error)
	DiscoveryFilters() []MatchFilter
}

// MatchFilter is a declarative, serializable filter definition for discovery.
// Resources matching ALL conditions in a filter are excluded from discovery.
type MatchFilter struct {
	ResourceTypes []string          // Resource types this filter applies to
	Conditions    []FilterCondition // All conditions must match (AND logic) to exclude
}

// FilterCondition defines a single condition for filtering resources.
// Uses JSONPath expressions to query resource properties.
type FilterCondition struct {
	// PropertyPath is a JSONPath expression to query resource properties.
	// Examples:
	//   - "$.Tags[?(@.Key=='Name')].Value" - get value of tag with key "Name"
	//   - "$.Tags[?(@.Key=~'eks:automode:.*')]" - check if any tag key matches regex
	//   - "$.SkipDiscovery" - get top-level property value
	PropertyPath string

	// PropertyValue is the expected value to match.
	// Empty string means existence check (path returns any value = match).
	// Non-empty means exact string match against the query result.
	PropertyValue string
}

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
