// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConnStr_TCPHost(t *testing.T) {
	got := BuildConnStr("localhost", 5432, "postgres", "secret", "mydb")
	want := "postgres://postgres:secret@localhost:5432/mydb"

	assert.Equal(t, want, got)
}

func TestBuildConnStr_RemoteTCPHost(t *testing.T) {
	got := BuildConnStr("formae-pg.postgres.database.azure.com", 5432, "admin", "pass", "formae")
	want := "postgres://admin:pass@formae-pg.postgres.database.azure.com:5432/formae"

	assert.Equal(t, want, got)
}

func TestBuildConnStr_UnixSocket(t *testing.T) {
	got := BuildConnStr("/var/run/postgresql", 5432, "postgres", "secret", "mydb")
	want := "host=/var/run/postgresql user=postgres password=secret dbname=mydb"

	assert.Equal(t, want, got)
}

func TestBuildConnStr_CloudSQLSocket(t *testing.T) {
	got := BuildConnStr("/cloudsql/my-project:us-central1:my-instance", 5432, "postgres", "secret", "formae")
	want := "host=/cloudsql/my-project:us-central1:my-instance user=postgres password=secret dbname=formae"

	assert.Equal(t, want, got)
}

// Reserved characters in passwords (e.g. those produced by RDS-managed master
// credential generation) must be percent-encoded so that the resulting URI
// parses unambiguously and pgx recovers the original password byte-for-byte
// during SASL auth.
func TestBuildConnStr_PasswordWithReservedChars(t *testing.T) {
	cases := []struct {
		name string
		pw   string
	}{
		{"rds-style", "Ql1b!9<(u2eWPA#4!bUBXAZos*L("},
		{"colon-and-at", "p:a@s/s?w#o[r]d"},
		{"percent-and-plus", "abc%def+ghi"},
		{"empty", ""},
		{"only-reserved", "!*'();:@&=+$,/?#[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := BuildConnStr("db.example.com", 5432, "formae", tc.pw, "formae")
			cfg, err := pgx.ParseConfig(s)
			require.NoError(t, err, "pgx must accept the produced connection string")
			assert.Equal(t, tc.pw, cfg.Password, "password must round-trip through pgx parser")
			assert.Equal(t, "formae", cfg.User)
			assert.Equal(t, "db.example.com", cfg.Host)
			assert.Equal(t, uint16(5432), cfg.Port)
			assert.Equal(t, "formae", cfg.Database)
		})
	}
}

func TestBuildConnStr_UserWithReservedChars(t *testing.T) {
	s := BuildConnStr("db.example.com", 5432, "user@with:reserved", "pw", "db")
	cfg, err := pgx.ParseConfig(s)
	require.NoError(t, err)
	assert.Equal(t, "user@with:reserved", cfg.User)
	assert.Equal(t, "pw", cfg.Password)
}
