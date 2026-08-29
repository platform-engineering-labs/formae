// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package aurora

import (
	"fmt"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Generator persistence for Aurora is not yet implemented. The generators
// table exists (see migrations_postgres, which Aurora Data API also runs) so
// schema stays in lockstep across backends, but these methods exist only to
// satisfy datastore.Datastore until a following change implements them
// against the shared dstest suite.

func (d *DatastoreAuroraDataAPI) CreateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for aurora")
}

func (d *DatastoreAuroraDataAPI) UpdateGenerator(_ pkgmodel.Generator, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for aurora")
}

func (d *DatastoreAuroraDataAPI) DeleteGenerator(_, _ string) (string, error) {
	return "", fmt.Errorf("generator persistence is not yet implemented for aurora")
}

func (d *DatastoreAuroraDataAPI) GetGenerator(_, _ string) (pkgmodel.Generator, error) {
	return nil, fmt.Errorf("generator persistence is not yet implemented for aurora")
}

func (d *DatastoreAuroraDataAPI) LoadGeneratorsByStack(_ string) ([]pkgmodel.Generator, error) {
	return nil, fmt.Errorf("generator persistence is not yet implemented for aurora")
}
