// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
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
// A resource is excluded when it is of a type the filter applies to and every
// one of the filter's conditions matches. A filter naming no conditions
// excludes nothing.
type MatchFilter struct {
	ResourceTypes []string          // Resource types this filter applies to; empty means every type
	Conditions    []FilterCondition // All conditions must match (AND logic) to exclude
}

// AppliesTo reports whether this filter is scoped to the given resource type.
//
// An empty ResourceTypes list applies to every type. That is what a filter
// naming only conditions reads as, and writing one is the ordinary way to say
// "exclude anything tagged like this, whatever it is".
func (f MatchFilter) AppliesTo(resourceType string) bool {
	if len(f.ResourceTypes) == 0 {
		return true
	}
	return slices.Contains(f.ResourceTypes, resourceType)
}

// FiltersForType returns the filters that apply to the given resource type.
func FiltersForType(filters []MatchFilter, resourceType string) []MatchFilter {
	var result []MatchFilter
	for i := range filters {
		if filters[i].AppliesTo(resourceType) {
			result = append(result, filters[i])
		}
	}
	return result
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
	MSSQLDatastore         = "mssql"
)

const (
	MSSQLAuthSQL              = "sql"               // username/password
	MSSQLAuthWorkloadIdentity = "workload-identity" // Azure AD workload identity
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
	MSSQL         MSSQLConfig
}

type SqliteConfig struct {
	FilePath string
}

// PasswordProvider resolves a datastore password. It is consulted once per new
// database connection, so a deployment whose credential is rotated out from
// under a long-running process can hand back the current value without the
// process being restarted. Returning an error fails that connection attempt.
type PasswordProvider func(context.Context) (string, error)

type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	// PasswordSecretArn names a Secrets Manager secret holding the password.
	// When set, the datastore resolves the credential from it on every new
	// connection instead of using Password, so a rotation is picked up without
	// restarting the process. Empty means Password is used as-is.
	PasswordSecretArn string
	// PasswordProvider, when set, supersedes Password: it is called for every
	// new connection the pool opens. Nil means Password is used as-is.
	PasswordProvider PasswordProvider
	// PasswordProviderFactory builds the provider for PasswordSecretArn.
	// Production leaves it nil and gets the Secrets Manager provider; tests set
	// it to inject a controllable authority at the datastore boundary, which is
	// the only place the wiring can actually be observed.
	PasswordProviderFactory func(ctx context.Context, secretARN string) (PasswordProvider, error)
	Database                string
	Schema                  string
	ConnectionParams        string
}

type AuroraDataAPIConfig struct {
	ClusterARN string
	SecretARN  string
	Database   string
	Region     string
	Endpoint   string
}

// MSSQLConfig configures the Microsoft SQL Server datastore backend.
// User/Password apply only when AuthMode == MSSQLAuthSQL.
type MSSQLConfig struct {
	Host                   string
	Port                   int
	Database               string
	AuthMode               string
	User                   string
	Password               string
	Encrypt                bool
	TrustServerCertificate bool
	ConnectionParams       string
	MaxOpenConns           int
	ConnMaxLifetime        time.Duration
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
	TLS             bool
	AuthKey         string
	Hostname        string
	AdvertiseTags   []string
	EgressProxyPort int
}

type NetworkConfig struct {
	Type      string
	Tailscale *TailscaleConfig

	// LegacyRawJSON holds the raw JSON from the deprecated plugins.network config.
	// When set, the server passes this directly to the network registry instead
	// of marshaling the typed Tailscale config.
	LegacyRawJSON json.RawMessage `json:"-"`
}

// PluginConfigJSON returns the JSON to hand the network plugin: the legacy
// raw JSON if present (from the deprecated plugins.network config), otherwise
// the typed Tailscale config marshaled to JSON.
func (c *NetworkConfig) PluginConfigJSON() ([]byte, error) {
	if len(c.LegacyRawJSON) > 0 {
		return c.LegacyRawJSON, nil
	}

	configJSON, err := json.Marshal(c.Tailscale)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal network config: %w", err)
	}

	return configJSON, nil
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

// OidcCredentialPluginUserConfig holds per-broker configuration from the
// user's formae.conf.pkl. PluginConfig is the broker's own opaque payload:
// everything the user declared beyond the two base fields, handed to the
// broker process verbatim.
type OidcCredentialPluginUserConfig struct {
	Type         string
	Enabled      bool
	PluginConfig json.RawMessage
}

type AgentConfig struct {
	Server                ServerConfig
	Datastore             DatastoreConfig
	Retry                 RetryConfig
	Synchronization       SynchronizationConfig
	Discovery             DiscoveryConfig
	Logging               LoggingConfig
	OTel                  OTelConfig
	StackExpirer          StackExpirerConfig
	Auth                  json.RawMessage
	ResourcePlugins       []ResourcePluginUserConfig
	OidcCredentialPlugins []OidcCredentialPluginUserConfig
}

// Connection is where the CLI sends commands. It has exactly two arms, so a
// configuration cannot be both classic and hosted, and a hosted connection
// cannot exist without the installation it addresses.
type Connection interface {
	isConnection()
}

// installationRE is the routing-key grammar: 27 base62 characters, case
// sensitive, which is the text form of a KSUID.
var installationRE = regexp.MustCompile(`^[0-9A-Za-z]{27}$`)

// ValidInstallationID reports whether id is well formed as an installation
// identifier.
//
// This is a shape check and deliberately not a decode. 27 base62 digits span a
// wider range than the 160 bits a KSUID encodes, so a few strings this accepts
// would fail a KSUID parser. Refusing them here would make this client
// stricter than the edge that does the routing, which validates the same
// grammar: we would then refuse an identifier the router would have accepted,
// and gain nothing, because nothing mints an identifier that cannot be
// decoded. A value that is well formed but not routable is a 404 from the
// edge, which says so far more usefully than a local guess would.
//
// One definition, exported, because this grammar had four copies in four
// repositories and a format change reached only some of them: the edge routed
// on the new shape while three validators still refused it, which made a real
// installation unaddressable from either end.
func ValidInstallationID(id string) bool { return installationRE.MatchString(id) }

// ClassicConnection addresses a self-hosted agent.
type ClassicConnection struct {
	URL  string
	Port int
	// Auth is the opaque per-plugin auth configuration, nil when the agent
	// needs no credential.
	Auth json.RawMessage
}

func (*ClassicConnection) isConnection() {}

// HostedConnection addresses one installation behind the hosted endpoint.
// Endpoint is a canonical https origin and Installation a canonical KSUID:
// both are validated when the configuration is loaded, so consumers can use
// them directly.
type HostedConnection struct {
	Endpoint     string
	Installation string
	Auth         json.RawMessage
}

func (*HostedConnection) isConnection() {}

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
	Connection Connection

	DisableUsageReporting bool
	Theme                 string
	Appearance            string
}

// AuthConfig returns the auth plugin configuration carried by the connection,
// or nil when the profile configures no auth plugin.
func (c CliConfig) AuthConfig() json.RawMessage {
	switch conn := c.Connection.(type) {
	case *ClassicConnection:
		return conn.Auth
	case *HostedConnection:
		return conn.Auth
	}
	return nil
}

type Config struct {
	Agent     AgentConfig
	Artifacts ArtifactConfig
	Cli       CliConfig
	Network   *NetworkConfig
	PluginDir string
	Warnings  []string `json:"-"`
}
