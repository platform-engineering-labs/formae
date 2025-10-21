// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import "github.com/apple/pkl-go/pkl"

func init() {
	pkl.RegisterMapping("formae.Config#User", User{})
}

type ServerConfig struct {
	Nodename     string `pkl:"nodename"`
	Hostname     string `pkl:"hostname"`
	Port         int32  `pkl:"port"`
	Secret       string `pkl:"secret"`
	ObserverPort int32  `pkl:"observerPort"`
	TLSCert      string `pkl:"tlsCert"`
	TLSKey       string `pkl:"tlsKey"`
}

type DatastoreConfig struct {
	DatastoreType string         `pkl:"datastoreType"`
	Sqlite        SqliteConfig   `pkl:"sqlite"`
	Postgres      PostgresConfig `pkl:"postgres"`
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

type RetryConfig struct {
	StatusCheckInterval *pkl.Duration `pkl:"statusCheckInterval"`
	MaxRetries          int32         `pkl:"maxRetries"`
	RetryDelay          *pkl.Duration `pkl:"retryDelay"`
}

type SynchronizationConfig struct {
	Enabled  bool          `pkl:"enabled"`
	Interval *pkl.Duration `pkl:"interval"`
}

type LoggingConfig struct {
	FilePath        string `pkl:"filePath"`
	FileLogLevel    string `pkl:"fileLogLevel"`
	ConsoleLogLevel string `pkl:"consoleLogLevel"`
}

type Target struct {
	Label     string `pkl:"label"`
	Namespace string `pkl:"namespace"`
	// Output is postprocessed, hence the key case difference
	Config *pkl.Object `pkl:"Config"`
}

type User struct {
	Username string `pkl:"username"`
	Password string `pkl:"password"`
}

type DiscoveryConfig struct {
	Enabled                 bool          `pkl:"enabled"`
	ScanTargets             []Target      `pkl:"scanTargets"`
	Interval                *pkl.Duration `pkl:"interval"`
	LabelTagKeys            []string      `pkl:"labelTagKeys"`
	ResourceTypesToDiscover []string      `pkl:"resourceTypesToDiscover"`
}

type OTLPConfig struct {
	Endpoint string `pkl:"endpoint"`
	Protocol string `pkl:"protocol"`
	Insecure bool   `pkl:"insecure"`
}

type OTelConfig struct {
	Enabled     bool       `pkl:"enabled"`
	ServiceName string     `pkl:"serviceName"`
	OTLP        OTLPConfig `pkl:"otlp"`
}

type AgentConfig struct {
	Server          ServerConfig          `pkl:"server"`
	Datastore       DatastoreConfig       `pkl:"datastore"`
	Retry           RetryConfig           `pkl:"retry"`
	Synchronization SynchronizationConfig `pkl:"synchronization"`
	Discovery       DiscoveryConfig       `pkl:"discovery"`
	Logging         LoggingConfig         `pkl:"logging"`
	OTel            OTelConfig            `pkl:"oTel"`
}

type APIConfig struct {
	URL  string `pkl:"url"`
	Port int32  `pkl:"port"`
}

type CliConfig struct {
	API                   APIConfig `pkl:"api"`
	DisableUsageReporting bool      `pkl:"disableUsageReporting"`
}

type PluginConfig struct {
	// Output is postprocessed, hence the key case difference
	Network        *pkl.Object `pkl:"Network"`
	Authentication *pkl.Object `pkl:"Authentication"`
}

type Config struct {
	Agent   AgentConfig  `pkl:"agent"`
	Cli     CliConfig    `pkl:"cli"`
	Plugins PluginConfig `pkl:"plugins"`
}
