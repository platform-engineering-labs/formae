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
	MaxRequestsPerSecond() int

	Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error)
	Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error)
	Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error)

	Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error)

	List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error)

	TargetBehavior() resource.TargetBehavior

	// GetMatchFilters returns declarative filters for discovery (serializable)
	GetMatchFilters() []MatchFilter
}

// PluginInfo provides read-only plugin metadata for discovery operations.
// This interface is implemented by both local ResourcePlugin and remote plugin info proxies.
type PluginInfo interface {
	GetNamespace() string
	SupportedResources() []ResourceDescriptor
	SchemaForResourceType(resourceType string) (model.Schema, error)
	GetMatchFilters() []MatchFilter
}

// ResourceFilter is a function that determines if a resource should be filtered. It
// MatchFilter is a declarative, serializable filter definition for discovery
type MatchFilter struct {
	ResourceTypes []string          // Resource types this filter applies to
	Conditions    []FilterCondition // All conditions must match (AND logic)
	Action        FilterAction      // What to do when conditions match
}

type FilterCondition struct {
	Type ConditionType // TagMatch, PropertyMatch

	// For TagMatch - check if resource has tag with key in list and matching value
	TagKeys  []string // Tag keys to look for (e.g., "SkipDiscovery")
	TagValue string   // Expected tag value (e.g., "true")

	// For PropertyMatch - direct property check
	PropertyPath  string // Property name (e.g., "SkipDiscovery")
	PropertyValue string // Expected value (e.g., "true")
}

type FilterAction string

const (
	FilterActionExclude FilterAction = "exclude" // Exclude matching resources from discovery
	FilterActionInclude FilterAction = "include" // Only include matching resources
)

type ConditionType string

const (
	ConditionTypeTagMatch      ConditionType = "tag_match"
	ConditionTypePropertyMatch ConditionType = "property_match"
)

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
