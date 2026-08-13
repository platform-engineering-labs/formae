// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	pklgo "github.com/apple/pkl-go/pkl"
	"github.com/tidwall/gjson"

	pklmodel "github.com/platform-engineering-labs/formae/internal/schema/pkl/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	defaultClassicURL  = "http://localhost"
	defaultClassicPort = 49684
)

// installationRE matches the canonical lowercase UUID text form. The
// constraint is syntactic on purpose: it mirrors what the edge accepts as a
// routing key, and says nothing about UUID version or variant bits.
var installationRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// buildConnection resolves the CLI's connection, translating the deprecated
// cli.api, cli.auth, and plugins.authentication settings when they are the
// only form present. Setting a legacy field alongside an explicit connection
// is an error rather than a precedence rule: those fields carry a credential,
// and a configuration that names two endpoints does not say which one the
// credential is for.
func buildConnection(
	cli *pklmodel.CliConfig,
	legacyCliAuth json.RawMessage,
	warnings *[]string,
) (pkgmodel.Connection, error) {
	legacy := legacyFieldsSet(cli, legacyCliAuth)

	if cli.Connection != nil {
		if len(legacy) > 0 {
			return nil, fmt.Errorf(
				"cli.connection is set alongside deprecated %s; remove the deprecated settings, "+
					"since cli.connection replaces them",
				strings.Join(legacy, " and "),
			)
		}
		return translateConnection(cli.Connection)
	}

	classic := &pkgmodel.ClassicConnection{URL: defaultClassicURL, Port: defaultClassicPort}
	if cli.API != nil {
		classic.URL = cli.API.URL
		classic.Port = int(cli.API.Port)
		warn(warnings, "cli.api is deprecated; migrate to cli.connection = new Classic { url; port }")
	}
	switch {
	case len(cli.Auth.Properties) > 0:
		classic.Auth = translateAuthConfig(&cli.Auth)
		warn(warnings, "cli.auth is deprecated; set auth inside cli.connection")
	case len(legacyCliAuth) > 0:
		// plugins.authentication already warned when it was applied.
		classic.Auth = legacyCliAuth
	}
	return classic, nil
}

func legacyFieldsSet(cli *pklmodel.CliConfig, legacyCliAuth json.RawMessage) []string {
	var set []string
	if cli.API != nil {
		set = append(set, "cli.api")
	}
	switch {
	case len(cli.Auth.Properties) > 0:
		set = append(set, "cli.auth")
	case len(legacyCliAuth) > 0:
		set = append(set, "plugins.authentication")
	}
	return set
}

func warn(warnings *[]string, msg string) {
	slog.Warn(msg)
	*warnings = append(*warnings, msg)
}

func translateConnection(decoded any) (pkgmodel.Connection, error) {
	switch conn := decoded.(type) {
	case *pklmodel.ClassicConnection:
		return &pkgmodel.ClassicConnection{
			URL:  conn.URL,
			Port: int(conn.Port),
			Auth: translateOptionalAuth(conn.Auth),
		}, nil

	case *pklmodel.HostedConnection:
		endpoint, err := canonicalHostedEndpoint(conn.Endpoint)
		if err != nil {
			return nil, err
		}
		if err := validateInstallation(conn.Installation); err != nil {
			return nil, err
		}
		auth := translateOptionalAuth(conn.Auth)
		if err := validateAuthUsable(auth); err != nil {
			return nil, err
		}
		return &pkgmodel.HostedConnection{
			Endpoint:     endpoint,
			Installation: conn.Installation,
			Auth:         auth,
		}, nil

	default:
		return nil, fmt.Errorf("cli.connection has an unsupported type %T", decoded)
	}
}

func translateOptionalAuth(obj *pklgo.Object) json.RawMessage {
	if obj == nil {
		return nil
	}
	return translateAuthConfig(obj)
}

// canonicalHostedEndpoint parses and canonicalises a hosted endpoint into an
// origin with no trailing slash, so request URLs can be built by joining a
// path onto it rather than by concatenating strings.
func canonicalHostedEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("cli.connection endpoint %q is not a valid URL: %w", raw, err)
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return "", fmt.Errorf("cli.connection endpoint %q must be an absolute https URL", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("cli.connection endpoint %q must not embed credentials", raw)
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("cli.connection endpoint %q must not carry a query string", raw)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("cli.connection endpoint %q must not carry a fragment", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("cli.connection endpoint %q must not carry a path", raw)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Host), ":443")
	return "https://" + host, nil
}

func validateInstallation(id string) error {
	if !installationRE.MatchString(id) {
		return fmt.Errorf("cli.connection installation %q must be a canonical lowercase UUID", id)
	}
	return nil
}

// validateAuthUsable rejects an auth block that satisfies the schema without
// naming a plugin. The type system can require the property, but not that its
// contents identify anything.
func validateAuthUsable(raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("cli.connection auth is required for a hosted connection")
	}
	if gjson.GetBytes(raw, "type").String() == "" {
		return fmt.Errorf("cli.connection auth must carry a non-empty type")
	}
	return nil
}
