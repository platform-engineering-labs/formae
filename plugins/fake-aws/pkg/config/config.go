// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"encoding/json"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type Config struct {
	Region string `json:"Region"`
}

func FromTarget(target *pkgmodel.Target) *Config {
	if target == nil || target.Config == nil {
		return &Config{}
	}
	config := &Config{}
	_ = json.Unmarshal(target.Config, config)

	return config
}
