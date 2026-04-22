// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"time"
)

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

const (
	SqliteDatastore        = "sqlite"
	PostgresDatastore      = "postgres"
	AuroraDataAPIDatastore = "auroradataapi"
)

type ServerConfig struct {
	Nodename      string
	Hostname      string
	Port          int
	ErgoPort      int `pkl:"ergoPort"`
	RegistrarPort int
	Secret        string
	ObserverPort  int
	TLSCert       string
	TLSKey        string
}

type DatastoreConfig struct {
	DatastoreType string
	Sqlite        SqliteConfig
	Postgres      PostgresConfig
	AuroraDataAPI AuroraDataAPIConfig
}

type SqliteConfig struct {
	FilePath string
}

type PostgresConfig struct {
	Host             string
	Port             int
	User             string
	Password         string
	Database         string
	Schema           string
	ConnectionParams string
}

type AuroraDataAPIConfig struct {
	ClusterARN string
	SecretARN  string
	Database   string
	Region     string
	Endpoint   string
}

type RetryConfig struct {
	StatusCheckInterval time.Duration
	MaxRetries          int
	RetryDelay          time.Duration
}

type SynchronizationConfig struct {
	Enabled  bool
	Interval time.Duration
}

type LoggingConfig struct {
	FilePath        string
	FileLogLevel    slog.Level
	ConsoleLogLevel slog.Level
}

type DiscoveryConfig struct {
	Enabled                 bool
	Interval                time.Duration
	LabelTagKeys            []string
	ResourceTypesToDiscover []string
}

type OTLPConfig struct {
	Enabled     bool
	Endpoint    string
	Protocol    string
	Insecure    bool
	Temporality string // "delta" (default/OTel-native) or "cumulative" (for Prometheus backends without delta support)
}

type PrometheusConfig struct {
	Enabled bool `pkl:"enabled"`
}

type OTelConfig struct {
	Enabled     bool
	ServiceName string
	OTLP        OTLPConfig       `pkl:"otlp"`
	Prometheus  PrometheusConfig `pkl:"prometheus"`
}

type StackExpirerConfig struct {
	Interval time.Duration
	Disabled bool
}

type TailscaleConfig struct {
	TLS           bool
	AuthKey       string
	Hostname      string
	AdvertiseTags []string
}

type NetworkConfig struct {
	Type      string
	Tailscale *TailscaleConfig

	// LegacyRawJSON holds the raw JSON from the deprecated plugins.network config.
	// When set, the server passes this directly to the network registry instead
	// of marshaling the typed Tailscale config.
	LegacyRawJSON json.RawMessage `json:"-"`
}

// ResourcePluginUserConfig holds per-plugin configuration from the user's
// formae.conf.pkl. Fields are optional — nil means "use plugin default."
type ResourcePluginUserConfig struct {
	Type                    string
	Enabled                 bool
	RateLimit               *RateLimitConfig
	LabelConfig             *LabelConfig
	DiscoveryFilters        []MatchFilter
	ResourceTypesToDiscover []string
	Retry                   *RetryConfig
	PluginConfig            json.RawMessage
}

type AgentConfig struct {
	Server          ServerConfig
	Datastore       DatastoreConfig
	Retry           RetryConfig
	Synchronization SynchronizationConfig
	Discovery       DiscoveryConfig
	Logging         LoggingConfig
	OTel            OTelConfig
	StackExpirer    StackExpirerConfig
	Auth            json.RawMessage
	ResourcePlugins []ResourcePluginUserConfig
}

type APIConfig struct {
	URL  string
	Port int
}

// RepositoryType discriminates orbital repositories by purpose.
type RepositoryType string

const (
	RepositoryTypeBinary       RepositoryType = "binary"
	RepositoryTypeFormaePlugin RepositoryType = "formae-plugin"
)

// Repository points to a single orbital repository.
type Repository struct {
	URI  url.URL
	Type RepositoryType
}

type ArtifactConfig struct {
	URL      url.URL
	Username string
	Password string
	// Repositories is the canonical list. When empty and URL is set,
	// the translation layer populates it with a single binary entry.
	Repositories []Repository
}

type CliConfig struct {
	API                   APIConfig
	DisableUsageReporting bool
	Auth                  json.RawMessage
}

type Config struct {
	Agent     AgentConfig
	Artifacts ArtifactConfig
	Cli       CliConfig
	Network   *NetworkConfig
	PluginDir string
	Warnings  []string `json:"-"`
}
