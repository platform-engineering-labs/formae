// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package mssql

import (
	"net/url"
	"strings"
	"testing"

	"github.com/microsoft/go-mssqldb/azuread"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestBuildConnection_SQLAuth(t *testing.T) {
	cfg := pkgmodel.MSSQLConfig{
		Host:     "formae.database.windows.net",
		Port:     1433,
		Database: "formae",
		AuthMode: pkgmodel.MSSQLAuthSQL,
		User:     "formae_user",
		Password: "p@ssword",
		Encrypt:  true,
	}

	driver, dsn, err := BuildConnection(cfg)
	require.NoError(t, err)
	assert.Equal(t, "sqlserver", driver)

	parsed, err := msdsn.Parse(dsn)
	require.NoError(t, err, "go-mssqldb must accept the produced DSN")
	assert.Equal(t, "formae.database.windows.net", parsed.Host)
	assert.Equal(t, uint64(1433), parsed.Port)
	assert.Equal(t, "formae", parsed.Database)
	assert.Equal(t, "formae_user", parsed.User)
	assert.Equal(t, "p@ssword", parsed.Password, "password must round-trip through msdsn.Parse")
}

func TestBuildConnection_WorkloadIdentity(t *testing.T) {
	cfg := pkgmodel.MSSQLConfig{
		Host:     "formae.database.windows.net",
		Port:     1433,
		Database: "formae",
		AuthMode: pkgmodel.MSSQLAuthWorkloadIdentity,
		Encrypt:  true,
	}

	driver, dsn, err := BuildConnection(cfg)
	require.NoError(t, err)
	assert.Equal(t, azuread.DriverName, driver, "workload identity must use the azuread driver")

	parsed, err := msdsn.Parse(dsn)
	require.NoError(t, err)
	assert.Equal(t, "formae.database.windows.net", parsed.Host)
	assert.Equal(t, "formae", parsed.Database)
	assert.Empty(t, parsed.User, "workload-identity DSN must not carry a SQL user")
	assert.Empty(t, parsed.Password, "workload-identity DSN must not carry a SQL password")
	assert.Contains(
		t, strings.ToLower(dsn), "fedauth=activedirectoryworkloadidentity",
		"fedauth must select ActiveDirectoryWorkloadIdentity",
	)
}

// Port 0 in the config means "use the default" (1433). Forcing operators to
// repeat the well-known port in every config file would be noise.
func TestBuildConnection_DefaultsPortTo1433(t *testing.T) {
	cfg := pkgmodel.MSSQLConfig{
		Host:     "formae.database.windows.net",
		Database: "formae",
		AuthMode: pkgmodel.MSSQLAuthSQL,
		User:     "u",
		Password: "p",
	}

	_, dsn, err := BuildConnection(cfg)
	require.NoError(t, err)

	parsed, err := msdsn.Parse(dsn)
	require.NoError(t, err)
	assert.Equal(t, uint64(1433), parsed.Port)
}

func TestBuildConnection_TrustServerCertificateAndConnectionParams(t *testing.T) {
	cfg := pkgmodel.MSSQLConfig{
		Host:                   "formae.database.windows.net",
		Port:                   1433,
		Database:               "formae",
		AuthMode:               pkgmodel.MSSQLAuthSQL,
		User:                   "u",
		Password:               "p",
		Encrypt:                true,
		TrustServerCertificate: true,
		ConnectionParams:       "connection+timeout=30&app+name=formae",
	}

	_, dsn, err := BuildConnection(cfg)
	require.NoError(t, err)

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	assert.Equal(t, "true", q.Get("trustservercertificate"))
	assert.Equal(t, "30", q.Get("connection timeout"))
	assert.Equal(t, "formae", q.Get("app name"))
}

func TestBuildConnection_PasswordWithReservedChars(t *testing.T) {
	cases := []struct {
		name string
		pw   string
	}{
		{"colon-and-at", "p:a@s/s?w#o[r]d"},
		{"percent-and-plus", "abc%def+ghi"},
		{"empty", ""},
		{"only-reserved", "!*'();:@&=+$,/?#[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := pkgmodel.MSSQLConfig{
				Host:     "formae.database.windows.net",
				Port:     1433,
				Database: "formae",
				AuthMode: pkgmodel.MSSQLAuthSQL,
				User:     "formae",
				Password: tc.pw,
			}
			_, dsn, err := BuildConnection(cfg)
			require.NoError(t, err)
			parsed, err := msdsn.Parse(dsn)
			require.NoError(t, err, "msdsn must accept DSN")
			assert.Equal(t, tc.pw, parsed.Password, "password must round-trip")
			assert.Equal(t, "formae", parsed.User)
		})
	}
}

func TestBuildConnection_UnknownAuthMode(t *testing.T) {
	cfg := pkgmodel.MSSQLConfig{
		Host:     "formae.database.windows.net",
		Database: "formae",
		AuthMode: "not-a-real-mode",
	}
	_, _, err := BuildConnection(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-real-mode")
}

func TestBuildConnection_EmptyAuthModeDefaultsToSQL(t *testing.T) {
	// AuthMode unset is the zero value; treat as SQL auth so an operator who
	// just sets Host/User/Password/Database in Pkl doesn't have to know about
	// the workload-identity selector.
	cfg := pkgmodel.MSSQLConfig{
		Host:     "formae.database.windows.net",
		Database: "formae",
		User:     "u",
		Password: "p",
	}
	driver, _, err := BuildConnection(cfg)
	require.NoError(t, err)
	assert.Equal(t, "sqlserver", driver)
}
