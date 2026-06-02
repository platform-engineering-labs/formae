// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package mssql is the Microsoft SQL Server backend for the formae datastore.
// AuthMode picks SQL username/password or Azure AD workload identity.
package mssql

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/microsoft/go-mssqldb/azuread"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	defaultPort     = 1433
	sqlserverDriver = "sqlserver"
)

func init() {
	datastore.DefaultRegistry.Register(pkgmodel.MSSQLDatastore, func(ctx context.Context, cfg *pkgmodel.DatastoreConfig, agentID string) (datastore.Datastore, error) {
		return NewDatastoreMSSQL(ctx, cfg, agentID)
	})
}

// BuildConnection returns the database/sql driver name and DSN. An empty
// AuthMode defaults to SQL auth so a minimal Pkl config still opens.
func BuildConnection(cfg pkgmodel.MSSQLConfig) (driverName, dsn string, err error) {
	authMode := cfg.AuthMode
	if authMode == "" {
		authMode = pkgmodel.MSSQLAuthSQL
	}

	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}

	u := url.URL{
		Scheme: sqlserverDriver,
		Host:   fmt.Sprintf("%s:%d", cfg.Host, port),
	}

	q := url.Values{}
	if cfg.Database != "" {
		q.Set("database", cfg.Database)
	}
	if cfg.Encrypt {
		q.Set("encrypt", "true")
	}
	if cfg.TrustServerCertificate {
		q.Set("trustservercertificate", "true")
	}

	switch authMode {
	case pkgmodel.MSSQLAuthSQL:
		driverName = sqlserverDriver
		// Empty user/password would leave a dangling "@" in the URL.
		if cfg.User != "" || cfg.Password != "" {
			u.User = url.UserPassword(cfg.User, cfg.Password)
		}
	case pkgmodel.MSSQLAuthWorkloadIdentity:
		driverName = azuread.DriverName
		q.Set("fedauth", azuread.ActiveDirectoryWorkloadIdentity)
	default:
		return "", "", fmt.Errorf("mssql: unrecognized AuthMode %q (want %q or %q)", authMode, pkgmodel.MSSQLAuthSQL, pkgmodel.MSSQLAuthWorkloadIdentity)
	}

	u.RawQuery = q.Encode()

	dsn = u.String()
	if cfg.ConnectionParams != "" {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn = dsn + sep + cfg.ConnectionParams
	}

	return driverName, dsn, nil
}
