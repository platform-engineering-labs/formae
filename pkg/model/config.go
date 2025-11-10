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
	SqliteDatastore   = "sqlite"
	PostgresDatastore = "postgres"
)

type ServerConfig struct {
	Nodename     string
	Hostname     string
	Port         int
	Secret       string
	ObserverPort int
	TLSCert      string
	TLSKey       string
}

type DatastoreConfig struct {
	DatastoreType string
	Sqlite        SqliteConfig
	Postgres      PostgresConfig
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
	Endpoint string
	Protocol string
	Insecure bool
}

type OTelConfig struct {
	Enabled     bool
	ServiceName string
	OTLP        OTLPConfig
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

type CliConfig struct {
	API                   APIConfig
	DisableUsageReporting bool
}

type PluginConfig struct {
	Network        json.RawMessage
	Authentication json.RawMessage
}

type Config struct {
	Agent   AgentConfig
	Cli     CliConfig
	Plugins PluginConfig
}
