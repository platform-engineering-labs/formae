// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "github.com/apple/pkl-go/pkl"

func init() {
	pkl.RegisterMapping("formae.Config#User", User{})
	pkl.RegisterMapping("formae.Config#RateLimitConfig", RateLimitConfig{})
	pkl.RegisterMapping("formae.Config#RetryConfig", RetryConfig{})
	pkl.RegisterMapping("formae.Config#LabelConfig", LabelConfig{})
	pkl.RegisterMapping("formae.Config#MatchFilter", MatchFilter{})
	pkl.RegisterMapping("formae.Config#FilterCondition", FilterCondition{})
}

// ResourcePlugin nested types used when decoding BaseResourcePluginConfig
// subclasses from pkl.Object properties.

type RateLimitConfig struct {
	MaxRequestsPerSecond int32 `pkl:"maxRequestsPerSecond"`
}

type LabelConfig struct {
	DefaultQuery      string      `pkl:"defaultQuery"`
	ResourceOverrides *pkl.Object `pkl:"resourceOverrides"`
}

type MatchFilter struct {
	ResourceTypes []string          `pkl:"resourceTypes"`
	Conditions    []FilterCondition `pkl:"conditions"`
}

type FilterCondition struct {
	PropertyPath  string `pkl:"propertyPath"`
	PropertyValue string `pkl:"propertyValue"`
}

type ServerConfig struct {
	Nodename      string `pkl:"nodename"`
	Hostname      string `pkl:"hostname"`
	Port          int32  `pkl:"port"`
	ErgoPort      int32  `pkl:"ergoPort"`
	RegistrarPort int32  `pkl:"registrarPort"`
	Secret        string `pkl:"secret"`
	ObserverPort  int32  `pkl:"observerPort"`
	TLSCert       string `pkl:"tlsCert"`
	TLSKey        string `pkl:"tlsKey"`
}

type DatastoreConfig struct {
	DatastoreType string              `pkl:"datastoreType"`
	Sqlite        SqliteConfig        `pkl:"sqlite"`
	Postgres      PostgresConfig      `pkl:"postgres"`
	AuroraDataAPI AuroraDataAPIConfig `pkl:"auroraDataAPI"`
}

type SqliteConfig struct {
	FilePath string `pkl:"filePath"`
}

type PostgresConfig struct {
	Host             string `pkl:"host"`
	Port             int32  `pkl:"port"`
	User             string `pkl:"user"`
	Password         string `pkl:"password"`
	Database         string `pkl:"database"`
	Schema           string `pkl:"schema"`
	ConnectionParams string `pkl:"connectionParams"`
}

type AuroraDataAPIConfig struct {
	ClusterArn string `pkl:"clusterArn"`
	SecretArn  string `pkl:"secretArn"`
	Database   string `pkl:"database"`
	Region     string `pkl:"region"`
	Endpoint   string `pkl:"endpoint"`
}

type RetryConfig struct {
	StatusCheckInterval *pkl.Duration `pkl:"statusCheckInterval"`
	MaxRetries          int32         `pkl:"maxRetries"`
	RetryDelay          *pkl.Duration `pkl:"retryDelay"`
}

type SynchronizationConfig struct {
	Enabled  bool          `pkl:"enabled"`
	Interval *pkl.Duration `pkl:"interval"`
}

type StackExpirerConfig struct {
	Enabled  bool          `pkl:"enabled"`
	Interval *pkl.Duration `pkl:"interval"`
}

type LoggingConfig struct {
	FilePath        string `pkl:"filePath"`
	FileLogLevel    string `pkl:"fileLogLevel"`
	ConsoleLogLevel string `pkl:"consoleLogLevel"`
}

type Target struct {
	Label        string `pkl:"label"`
	Namespace    string `pkl:"namespace"`
	Discoverable bool   `pkl:"discoverable"`
	// Output is postprocessed, hence the key case difference
	Config *pkl.Object `pkl:"Config"`
}

type User struct {
	Username string `pkl:"username"`
	Password string `pkl:"password"`
}

type DiscoveryConfig struct {
	Enabled                 bool          `pkl:"enabled"`
	Interval                *pkl.Duration `pkl:"interval"`
	LabelTagKeys            []string      `pkl:"labelTagKeys"`
	ResourceTypesToDiscover []string      `pkl:"resourceTypesToDiscover"`
}

type OTLPConfig struct {
	Enabled     bool   `pkl:"enabled"`
	Endpoint    string `pkl:"endpoint"`
	Protocol    string `pkl:"protocol"`
	Insecure    bool   `pkl:"insecure"`
	Temporality string `pkl:"temporality"` // "delta" (default/OTel-native) or "cumulative"
}

type PrometheusConfig struct {
	Enabled bool `pkl:"enabled"`
}

type OTelConfig struct {
	Enabled     bool             `pkl:"enabled"`
	ServiceName string           `pkl:"serviceName"`
	OTLP        OTLPConfig       `pkl:"otlp"`
	Prometheus  PrometheusConfig `pkl:"prometheus"`
}

type TailscaleConfig struct {
	TLS           bool     `pkl:"tls"`
	AuthKey       string   `pkl:"authKey"`
	Hostname      string   `pkl:"hostname"`
	AdvertiseTags []string `pkl:"advertiseTags"`
}

type NetworkConfig struct {
	Type      string           `pkl:"type"`
	Tailscale *TailscaleConfig `pkl:"tailscale"`
}

type AgentConfig struct {
	Server          ServerConfig          `pkl:"server"`
	Datastore       DatastoreConfig       `pkl:"datastore"`
	Retry           *RetryConfig          `pkl:"retry"`
	Synchronization SynchronizationConfig `pkl:"synchronization"`
	Discovery       DiscoveryConfig       `pkl:"discovery"`
	Logging         LoggingConfig         `pkl:"logging"`
	OTel            OTelConfig            `pkl:"oTel"`
	StackExpirer    StackExpirerConfig    `pkl:"stackExpirer"`
	Auth            pkl.Object            `pkl:"auth"`
	ResourcePlugins []pkl.Object          `pkl:"resourcePlugins"`
}

type APIConfig struct {
	URL  string `pkl:"url"`
	Port int32  `pkl:"port"`
}

type ArtifactConfig struct {
	URL      string `pkl:"url"`
	Username string `pkl:"username"`
	Password string `pkl:"password"`
}

type CliConfig struct {
	API                   APIConfig  `pkl:"api"`
	DisableUsageReporting bool       `pkl:"disableUsageReporting"`
	Auth                  pkl.Object `pkl:"auth"`
}

// PluginConfig is deprecated. Use top-level pluginDir, network, and agent.auth / cli.auth.
type PluginConfig struct {
	PluginDir      string      `pkl:"pluginDir"`
	Network        *pkl.Object `pkl:"Network"`
	Authentication *pkl.Object `pkl:"Authentication"`
}

type Config struct {
	Agent     AgentConfig    `pkl:"agent"`
	Artifacts ArtifactConfig `pkl:"artifacts"`
	Cli       CliConfig      `pkl:"cli"`
	Network   *NetworkConfig `pkl:"network"`
	PluginDir string         `pkl:"pluginDir"`
	Plugins   *PluginConfig  `pkl:"plugins"`
}
