// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package config

import (
	"context"
	"encoding/json"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Config represents Azure plugin configuration
type Config struct {
	SubscriptionId  string `json:"SubscriptionId"`
	TenantId        string `json:"TenantId,omitempty"`
	DefaultLocation string `json:"DefaultLocation,omitempty"`
}

// ToAzureCredential creates an Azure credential using DefaultAzureCredential
func (c *Config) ToAzureCredential(ctx context.Context) (azcore.TokenCredential, error) {
	var opts *azidentity.DefaultAzureCredentialOptions
	if c.TenantId != "" {
		opts = &azidentity.DefaultAzureCredentialOptions{
			TenantID: c.TenantId,
		}
	}
	return azidentity.NewDefaultAzureCredential(opts)
}

// FromTarget extracts Config from a Target
func FromTarget(target *pkgmodel.Target) *Config {
	if target == nil || target.Config == nil {
		return &Config{}
	}
	config := &Config{}
	_ = json.Unmarshal(target.Config, config)
	return config
}
