// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package client

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/config"
)

// Client wraps Azure SDK clients
type Client struct {
	Config               *config.Config
	ResourceGroupsClient *armresources.ResourceGroupsClient
}

// NewClient creates a new Azure client wrapper
func NewClient(cfg *config.Config) (*Client, error) {
	ctx := context.Background()
	cred, err := cfg.ToAzureCredential(ctx)
	if err != nil {
		return nil, err
	}

	rgClient, err := armresources.NewResourceGroupsClient(cfg.SubscriptionId, cred, nil)
	if err != nil {
		return nil, err
	}

	return &Client{
		Config:               cfg,
		ResourceGroupsClient: rgClient,
	}, nil
}
