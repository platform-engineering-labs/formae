// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"encoding/json"
	"log/slog"
	"time"
)

const (
	SqliteDatastore        = "sqlite"
	PostgresDatastore      = "postgres"
	AuroraDataAPIDatastore = "auroradataapi"
)

type ServerConfig struct {
	Nodename     string
	Hostname     string
	Port         int
	ErgoPort      int `pkl:"ergoPort"`
	RegistrarPort int
	Secret        string
	ObserverPort int
	TLSCert      string
	TLSKey       string
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

type AgentConfig struct {
	Server          ServerConfig
	Datastore       DatastoreConfig
	Retry           RetryConfig
	Synchronization SynchronizationConfig
	Discovery       DiscoveryConfig
	Logging         LoggingConfig
	OTel            OTelConfig
}

type APIConfig struct {
	URL  string
	Port int
}

type ArtifactConfig struct {
	URL      string
	Username string
	Password string
}

type CliConfig struct {
	API                   APIConfig
	DisableUsageReporting bool
}

type PluginConfig struct {
	PluginDir      string
	Network        json.RawMessage
	Authentication json.RawMessage
}

type Config struct {
	Agent     AgentConfig
	Artifacts ArtifactConfig
	Cli       CliConfig
	Plugins   PluginConfig
}
