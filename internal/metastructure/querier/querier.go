// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package querier

import (
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type Querier interface {
	QueryStatus(query string, clientId string, n int) ([]forma_command.FormaCommand, error)
	QueryResources(query string) ([]pkgmodel.Resource, error)
}
