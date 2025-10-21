// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type FormaCommandConfig struct {
	Mode     pkgmodel.FormaApplyMode `mapstructure:"mode" default:"reconcile" description:"forma apply mode (reconcile | patch)"`
	Force    bool                    `mapstructure:"force" default:"false" description:"overwrite any changes since the last reconcile without warning"`
	Simulate bool                    `mapstructure:"simulate" default:"false" description:"simulate the forma command rather than make actual changes"`
}
