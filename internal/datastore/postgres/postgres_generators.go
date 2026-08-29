// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Generator persistence for Postgres is not yet implemented. The generators
// table exists (see migrations_postgres) so schema stays in lockstep across
// backends, but these methods exist only to satisfy datastore.Datastore until
// a following change implements them against the shared dstest suite.

func (d DatastorePostgres) CreateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for postgres")
}

func (d DatastorePostgres) UpdateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for postgres")
}

func (d DatastorePostgres) DeleteGenerator(_, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for postgres")
}

func (d DatastorePostgres) GetGenerator(_, _ string) (pkgmodel.Generator, error) {
	return nil, fmt.Errorf("generator persistence is not yet implemented for postgres")
}

func (d DatastorePostgres) LoadGeneratorsByStack(_ string) ([]pkgmodel.Generator, error) {
	return nil, fmt.Errorf("generator persistence is not yet implemented for postgres")
}
