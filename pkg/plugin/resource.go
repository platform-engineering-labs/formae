// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Handles loading and unloading for various resource plugins

const Resource Type = "resource"

// ResourcePlugin is the public interface that plugin developers implement.
// Must remain stateless to ensure non-flaky restarts and hot reloads.
//
// Plugin identity (name, version, namespace) and schema methods are handled
// automatically by the SDK - see FullResourcePlugin for the complete interface
// used internally by the formae agent.
type ResourcePlugin interface {
	// Configuration methods
	RateLimit() RateLimitConfig
	DiscoveryFilters() []MatchFilter
	LabelConfig() LabelConfig

	// CRUD operations
	Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error)
	Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error)
	Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error)

	// Async operation support
	Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error)

	// Discovery support
	List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error)
}

// RateLimitScope defines the granularity of rate limiting
type RateLimitScope string

const (
	// RateLimitScopeNamespace applies rate limiting at the plugin namespace level (e.g., AWS, Azure)
	RateLimitScopeNamespace RateLimitScope = "Namespace"
)

// RateLimitConfig specifies rate limiting behavior for a plugin
type RateLimitConfig struct {
	Scope                            RateLimitScope
	MaxRequestsPerSecondForNamespace int
}

// FullResourcePlugin is the internal interface used by the formae agent.
// It includes identity and schema methods that plugin developers don't implement directly.
// The SDK's plugin.Run() function wraps a user's ResourcePlugin with manifest-derived
// implementations of these methods.
type FullResourcePlugin interface {
	ResourcePlugin

	// Identity methods - derived from formae-plugin.pkl manifest
	Name() string
	Version() *semver.Version
	Namespace() string

	// Schema methods - auto-extracted from schema/pkl/ directory
	SupportedResources() []ResourceDescriptor
	SchemaForResourceType(resourceType string) (model.Schema, error)
}

// LabelConfig defines how to extract labels from discovered resources.
// Labels are constructed by evaluating JSONPath queries against resource properties.
type LabelConfig struct {
	// DefaultQuery is a JSONPath expression applied to all resources in this namespace.
	// Example for AWS: `$.Tags[?(@.Key=='Name')].Value`
	// If empty, falls back to NativeID.
	DefaultQuery string

	// ResourceOverrides provides JSONPath expressions for specific resource types,
	// overriding the DefaultQuery. Use for resources without tags or with
	// non-standard label sources.
	// Key: resource type (e.g., "AWS::IAM::Policy")
	// Value: JSONPath expression (e.g., "$.PolicyName")
	ResourceOverrides map[string]string
}

// QueryForResourceType returns the JSONPath query for a given resource type.
// Returns the resource-specific override if exists, otherwise the default query.
func (c LabelConfig) QueryForResourceType(resourceType string) string {
	if override, ok := c.ResourceOverrides[resourceType]; ok {
		return override
	}
	return c.DefaultQuery
}

// ObservablePlugin is an optional interface for plugins that support observability.
// The SDK will check if a wrapped plugin implements this interface and configure
// logging and metrics if so.
type ObservablePlugin interface {
	SetObservability(logger Logger, metrics MetricRegistry)
}

// PluginInfo provides read-only plugin metadata for discovery operations.
// This interface is implemented by both local ResourcePlugin and remote plugin info proxies.
type PluginInfo interface {
	GetNamespace() string
	SupportedResources() []ResourceDescriptor
	SchemaForResourceType(resourceType string) (model.Schema, error)
	DiscoveryFilters() []MatchFilter
	LabelConfig() LabelConfig
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
