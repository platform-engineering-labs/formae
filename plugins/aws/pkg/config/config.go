// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type Config struct {
	Region  string `json:"Region"`
	Profile string `json:"Profile"`
}

func (c *Config) ToAwsConfig(ctx context.Context) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error

	opts = append(opts, awsconfig.WithRegion(c.Region))
	if c.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(c.Profile))
	}

	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

// FromTargetConfig parses the target configuration JSON into a Config struct
func FromTargetConfig(targetConfig json.RawMessage) *Config {
	if targetConfig == nil {
		return &Config{}
	}
	config := &Config{}
	_ = json.Unmarshal(targetConfig, config)

	return config
}

// FromTarget extracts config from a Target struct (used by TargetBehavior interface)
func FromTarget(target *pkgmodel.Target) *Config {
	if target == nil || target.Config == nil {
		return &Config{}
	}
	return FromTargetConfig(target.Config)
}
