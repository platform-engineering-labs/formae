// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package all_test

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/datastore/aurora"
	"github.com/platform-engineering-labs/formae/internal/datastore/mssql"
	"github.com/platform-engineering-labs/formae/internal/datastore/postgres"
	"github.com/platform-engineering-labs/formae/internal/datastore/sqlite"
)

// Holding a data migration lease needs a session to pin. Which backends have one
// is the whole basis for where the migration runs, so it is asserted here rather
// than left to be discovered at boot.

func TestBackendsThatCanHoldADataMigrationLease(t *testing.T) {
	var _ datastore.DataMigrationCapable = sqlite.DatastoreSQLite{}
	var _ datastore.DataMigrationCapable = postgres.DatastorePostgres{}
	var _ datastore.DataMigrationCapable = &mssql.DatastoreMSSQL{}
}

// The Aurora Data API is stateless HTTP: there is no session to pin, so it
// cannot hold a lease and deliberately does not implement the capability. The
// migration logs that the backend is unsupported and skips, leaving the
// documented manual remediation as the route for an affected datastore.
//
// This is asserted rather than assumed: adding the capability to Aurora without
// a way to hold a session would let two agents migrate concurrently.
func TestAuroraCannotHoldADataMigrationLease(t *testing.T) {
	var ds any = &aurora.DatastoreAuroraDataAPI{}
	if _, capable := ds.(datastore.DataMigrationCapable); capable {
		t.Fatal("the Aurora Data API cannot pin a session, so it must not claim the lease capability")
	}
}
