// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
