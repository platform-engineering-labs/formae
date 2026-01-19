// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package usage

import apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"

type Sender interface {
	SendStats(stats *apimodel.Stats, devMode bool) error
}
