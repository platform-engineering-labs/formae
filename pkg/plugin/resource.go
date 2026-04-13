// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"context"
	"encoding/json"

	"github.com/masterminds/semver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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
	RateLimit() pkgmodel.RateLimitConfig
	DiscoveryFilters() []pkgmodel.MatchFilter
	LabelConfig() pkgmodel.LabelConfig

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
	SchemaForResourceType(resourceType string) (pkgmodel.Schema, error)
}

// ObservablePlugin is an optional interface for plugins that support observability.
// The SDK will check if a wrapped plugin implements this interface and configure
// logging and metrics if so.
type ObservablePlugin interface {
	SetObservability(logger Logger, metrics MetricRegistry)
}

// Configurable is an optional interface for plugins that accept custom configuration.
// If implemented, the SDK calls Configure with the plugin-specific config JSON from
// the user's formae.conf.pkl (the fields beyond BaseResourcePluginConfig).
// Configure is called once during plugin setup, before the plugin announces to the agent.
//
// SECURITY: the config payload is transported to the plugin process via the
// FORMAE_PLUGIN_CONFIG environment variable (base64-encoded JSON). Environment
// is cleartext-equivalent - readable via /proc/<pid>/environ, surfaced in core
// dumps, and may appear in journal output under some configurations. Plugin
// authors MUST NOT place secrets (API keys, passwords, private keys) in
// Configurable fields. Route secrets through the $ref/$value resolver path
// instead, which is designed for that purpose.
//
// Configure is called once per plugin-process lifetime. Config changes in
// formae.conf.pkl require an agent restart to take effect.
type Configurable interface {
	Configure(config json.RawMessage) error
}

// PluginInfo provides read-only plugin metadata for discovery operations.
// This interface is implemented by both local ResourcePlugin and remote plugin info proxies.
type PluginInfo interface {
	GetNamespace() string
	SupportedResources() []ResourceDescriptor
	SchemaForResourceType(resourceType string) (pkgmodel.Schema, error)
	DiscoveryFilters() []pkgmodel.MatchFilter
	LabelConfig() pkgmodel.LabelConfig
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
