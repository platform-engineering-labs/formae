// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package mssql

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Generator persistence for MSSQL is not yet implemented. The generators
// table exists (see migrations_mssql) so schema stays in lockstep across
// backends, but these methods exist only to satisfy datastore.Datastore until
// a following change implements them against the shared dstest suite.

func (d *DatastoreMSSQL) CreateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for mssql")
}

func (d *DatastoreMSSQL) UpdateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for mssql")
}

func (d *DatastoreMSSQL) DeleteGenerator(_, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for mssql")
}

func (d *DatastoreMSSQL) GetGenerator(_, _ string) (pkgmodel.Generator, error) {
	return nil, fmt.Errorf("generator persistence is not yet implemented for mssql")
}

func (d *DatastoreMSSQL) LoadGeneratorsByStack(_ string) ([]pkgmodel.Generator, error) {
	return nil, fmt.Errorf("generator persistence is not yet implemented for mssql")
}
