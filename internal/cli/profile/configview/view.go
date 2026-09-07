// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package configview builds the redacted, versioned view of a resolved
// formae configuration that `formae profile show` renders. Both output modes
// consume this one value, so what counts as a secret cannot differ between
// them.
//
// Inclusion policy: typed non-secret fields are emitted; typed secrets are
// replaced by the caller's mask; opaque per-plugin blocks are reduced to their
// type discriminator, and free-form connection parameters are omitted, because
// in both cases a credential can hide under a key this package cannot name.
package configview

import (
	"encoding/json"
	"time"

	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// SchemaVersion identifies the shape of this view. Consumers read it before
// anything else; bump it when a field changes meaning or disappears.
const SchemaVersion = 1

const (
	// Redacted replaces a secret in machine output.
	Redacted = "<redacted>"
	// HumanMask replaces a secret in human output, matching how the CLI masks
	// opaque values elsewhere.
	HumanMask = "••••••••"
)

func From(profile string, cfg *pkgmodel.Config, mask string) map[string]any {
	v := map[string]any{
		"schemaVersion": SchemaVersion,
		"profile":       profile,
		"cli":           cliView(cfg.Cli),
		"agent":         agentView(cfg.Agent, mask),
		"artifacts":     artifactsView(cfg.Artifacts, mask),
		"pluginDir":     cfg.PluginDir,
	}
	if n := networkView(cfg.Network, mask); n != nil {
		v["network"] = n
	}
	if len(cfg.Warnings) > 0 {
		v["warnings"] = cfg.Warnings
	}
	return v
}

func cliView(cli pkgmodel.CliConfig) map[string]any {
	return map[string]any{
		"connection":            ConnectionView(cli.Connection),
		"disableUsageReporting": cli.DisableUsageReporting,
		"theme":                 cli.Theme,
		"appearance":            cli.Appearance,
	}
}

// ConnectionView tags the arm, so a consumer switches on mode rather than
// probing for a field, and emits only that arm's fields.
//
// Exported so callers outside this package render a connection identically: a
// second renderer would be a second shape for consumers to keep up with.
func ConnectionView(conn pkgmodel.Connection) map[string]any {
	switch c := conn.(type) {
	case *pkgmodel.HostedConnection:
		return map[string]any{
			"mode":         "hosted",
			"endpoint":     c.Endpoint,
			"installation": c.Installation,
			"auth":         opaqueView(c.Auth),
		}
	case *pkgmodel.ClassicConnection:
		v := map[string]any{"mode": "classic", "url": c.URL, "port": c.Port}
		if a := opaqueView(c.Auth); a != nil {
			v["auth"] = a
		}
		return v
	default:
		return nil
	}
}

// opaqueView reduces an opaque per-plugin block to its discriminator. Its
// other keys belong to a plugin we may not ship, so we cannot know which of
// them hold credentials.
func opaqueView(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	t := gjson.GetBytes(raw, "type").String()
	if t == "" {
		return nil
	}
	return map[string]any{"type": t}
}

func agentView(a pkgmodel.AgentConfig, mask string) map[string]any {
	return map[string]any{
		"server": map[string]any{
			"nodename":      a.Server.Nodename,
			"hostname":      a.Server.Hostname,
			"port":          a.Server.Port,
			"ergoPort":      a.Server.ErgoPort,
			"registrarPort": a.Server.RegistrarPort,
			"observerPort":  a.Server.ObserverPort,
			"tlsCert":       a.Server.TLSCert,
			"tlsKey":        a.Server.TLSKey,
			"secret":        maskIfSet(a.Server.Secret, mask),
		},
		"datastore": datastoreView(a.Datastore, mask),
		"retry": map[string]any{
			"statusCheckInterval": dur(a.Retry.StatusCheckInterval),
			"maxRetries":          a.Retry.MaxRetries,
			"retryDelay":          dur(a.Retry.RetryDelay),
		},
		"synchronization": map[string]any{
			"enabled":  a.Synchronization.Enabled,
			"interval": dur(a.Synchronization.Interval),
		},
		"discovery": map[string]any{
			"enabled":                 a.Discovery.Enabled,
			"interval":                dur(a.Discovery.Interval),
			"resourceTypesToDiscover": a.Discovery.ResourceTypesToDiscover,
		},
		"logging": map[string]any{
			"filePath":        a.Logging.FilePath,
			"fileLogLevel":    a.Logging.FileLogLevel.String(),
			"consoleLogLevel": a.Logging.ConsoleLogLevel.String(),
		},
		"stackExpirer": map[string]any{
			"disabled": a.StackExpirer.Disabled,
			"interval": dur(a.StackExpirer.Interval),
		},
		"oTel": map[string]any{
			"enabled":     a.OTel.Enabled,
			"serviceName": a.OTel.ServiceName,
			"otlp": map[string]any{
				"enabled":     a.OTel.OTLP.Enabled,
				"endpoint":    a.OTel.OTLP.Endpoint,
				"protocol":    a.OTel.OTLP.Protocol,
				"insecure":    a.OTel.OTLP.Insecure,
				"temporality": a.OTel.OTLP.Temporality,
			},
			"prometheus": map[string]any{"enabled": a.OTel.Prometheus.Enabled},
		},
		"auth":            opaqueView(a.Auth),
		"resourcePlugins": resourcePluginsView(a.ResourcePlugins),
	}
}

func datastoreView(d pkgmodel.DatastoreConfig, mask string) map[string]any {
	v := map[string]any{"datastoreType": d.DatastoreType}
	switch d.DatastoreType {
	case pkgmodel.SqliteDatastore:
		v["sqlite"] = map[string]any{"filePath": d.Sqlite.FilePath}
	case pkgmodel.PostgresDatastore:
		// connectionParams is appended to the connection string verbatim and
		// can carry a password, so it is omitted rather than masked.
		v["postgres"] = map[string]any{
			"host":     d.Postgres.Host,
			"port":     d.Postgres.Port,
			"user":     d.Postgres.User,
			"password": maskIfSet(d.Postgres.Password, mask),
			"database": d.Postgres.Database,
			"schema":   d.Postgres.Schema,
		}
	case pkgmodel.MSSQLDatastore:
		v["mssql"] = map[string]any{
			"host":                   d.MSSQL.Host,
			"port":                   d.MSSQL.Port,
			"database":               d.MSSQL.Database,
			"authMode":               d.MSSQL.AuthMode,
			"user":                   d.MSSQL.User,
			"password":               maskIfSet(d.MSSQL.Password, mask),
			"encrypt":                d.MSSQL.Encrypt,
			"trustServerCertificate": d.MSSQL.TrustServerCertificate,
			"maxOpenConns":           d.MSSQL.MaxOpenConns,
			"connMaxLifetime":        dur(d.MSSQL.ConnMaxLifetime),
		}
	case pkgmodel.AuroraDataAPIDatastore:
		v["auroraDataAPI"] = map[string]any{
			"clusterARN": d.AuroraDataAPI.ClusterARN,
			"secretARN":  d.AuroraDataAPI.SecretARN,
			"database":   d.AuroraDataAPI.Database,
			"region":     d.AuroraDataAPI.Region,
			"endpoint":   d.AuroraDataAPI.Endpoint,
		}
	}
	return v
}

func artifactsView(a pkgmodel.ArtifactConfig, mask string) map[string]any {
	repos := make([]any, 0, len(a.Repositories))
	for _, r := range a.Repositories {
		repos = append(repos, map[string]any{"uri": r.URI.Redacted(), "type": string(r.Type)})
	}
	return map[string]any{
		"url":          a.URL.Redacted(),
		"repositories": repos,
		"username":     maskIfSet(a.Username, mask),
		"password":     maskIfSet(a.Password, mask),
	}
}

func resourcePluginsView(plugins []pkgmodel.ResourcePluginUserConfig) []any {
	out := make([]any, 0, len(plugins))
	for _, p := range plugins {
		v := map[string]any{
			"type":                    p.Type,
			"enabled":                 p.Enabled,
			"resourceTypesToDiscover": p.ResourceTypesToDiscover,
			"discoveryFilters":        len(p.DiscoveryFilters),
			// pluginConfig is opaque per-plugin data and is not emitted.
		}
		if p.RateLimit != nil {
			v["rateLimit"] = map[string]any{
				"scope":                            string(p.RateLimit.Scope),
				"maxRequestsPerSecondForNamespace": p.RateLimit.MaxRequestsPerSecondForNamespace,
			}
		}
		if p.Retry != nil {
			v["retry"] = map[string]any{
				"statusCheckInterval": dur(p.Retry.StatusCheckInterval),
				"maxRetries":          p.Retry.MaxRetries,
				"retryDelay":          dur(p.Retry.RetryDelay),
			}
		}
		if p.LabelConfig != nil {
			v["labelConfig"] = map[string]any{"defaultQuery": p.LabelConfig.DefaultQuery}
		}
		out = append(out, v)
	}
	return out
}

func networkView(n *pkgmodel.NetworkConfig, mask string) map[string]any {
	if n == nil {
		return nil
	}
	v := map[string]any{"type": n.Type}
	if n.Tailscale != nil {
		v["tailscale"] = map[string]any{
			"tls":           n.Tailscale.TLS,
			"hostname":      n.Tailscale.Hostname,
			"advertiseTags": n.Tailscale.AdvertiseTags,
			"authKey":       maskIfSet(n.Tailscale.AuthKey, mask),
		}
	}
	// LegacyRawJSON is an opaque blob from the deprecated plugins.network
	// block and is not emitted: its shape belongs to a network plugin.
	return v
}

// maskIfSet masks a secret that has a value and leaves an unset one empty, so
// a reader can tell "not configured" from "configured, not shown".
func maskIfSet(v, mask string) string {
	if v == "" {
		return ""
	}
	return mask
}

func dur(d time.Duration) string { return d.String() }
