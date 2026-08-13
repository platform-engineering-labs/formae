// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/rdsdata"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// setChainCredentials puts known credentials on the standard AWS credential
// chain and keeps resolution off the network, so a test can tell chain
// credentials apart from any the datastore supplies itself.
func setChainCredentials(t *testing.T, accessKeyID, secretAccessKey string) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", accessKeyID)
	t.Setenv("AWS_SECRET_ACCESS_KEY", secretAccessKey)
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func TestAuroraClientOptions(t *testing.T) {
	t.Run("configured endpoint becomes the client base endpoint", func(t *testing.T) {
		cfg := &pkgmodel.AuroraDataAPIConfig{Endpoint: "http://localhost:8080"}

		var o rdsdata.Options
		auroraClientOptions(cfg)(&o)

		if o.BaseEndpoint == nil {
			t.Fatal("BaseEndpoint = nil, want the configured endpoint")
		}
		if *o.BaseEndpoint != "http://localhost:8080" {
			t.Errorf("BaseEndpoint = %q, want %q", *o.BaseEndpoint, "http://localhost:8080")
		}
	})

	t.Run("empty endpoint leaves SDK endpoint resolution in place", func(t *testing.T) {
		cfg := &pkgmodel.AuroraDataAPIConfig{}

		var o rdsdata.Options
		auroraClientOptions(cfg)(&o)

		if o.BaseEndpoint != nil {
			t.Errorf("BaseEndpoint = %q, want nil", *o.BaseEndpoint)
		}
	})
}

func TestLoadAuroraAWSConfigRegion(t *testing.T) {
	t.Run("configured region overrides the environment", func(t *testing.T) {
		setChainCredentials(t, "chain-key", "chain-secret")
		t.Setenv("AWS_REGION", "eu-west-1")

		awsCfg, err := loadAuroraAWSConfig(context.Background(), &pkgmodel.AuroraDataAPIConfig{Region: "us-east-2"})
		if err != nil {
			t.Fatalf("loadAuroraAWSConfig() error = %v", err)
		}

		if awsCfg.Region != "us-east-2" {
			t.Errorf("Region = %q, want %q", awsCfg.Region, "us-east-2")
		}
	})

	t.Run("empty region is left to the environment", func(t *testing.T) {
		setChainCredentials(t, "chain-key", "chain-secret")
		t.Setenv("AWS_REGION", "eu-west-1")

		awsCfg, err := loadAuroraAWSConfig(context.Background(), &pkgmodel.AuroraDataAPIConfig{})
		if err != nil {
			t.Fatalf("loadAuroraAWSConfig() error = %v", err)
		}

		if awsCfg.Region != "eu-west-1" {
			t.Errorf("Region = %q, want %q", awsCfg.Region, "eu-west-1")
		}
	})
}

// The endpoint selects where Data API requests go; it must not also decide
// which credentials sign them. An operator pointing the datastore at a
// non-default endpoint keeps the standard AWS credential chain.
func TestLoadAuroraAWSConfigUsesCredentialChain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
	}{
		{name: "endpoint set", endpoint: "http://localhost:8080"},
		{name: "endpoint empty", endpoint: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setChainCredentials(t, "chain-key", "chain-secret")
			t.Setenv("AWS_REGION", "us-east-1")

			awsCfg, err := loadAuroraAWSConfig(context.Background(), &pkgmodel.AuroraDataAPIConfig{Endpoint: tc.endpoint})
			if err != nil {
				t.Fatalf("loadAuroraAWSConfig() error = %v", err)
			}

			creds, err := awsCfg.Credentials.Retrieve(context.Background())
			if err != nil {
				t.Fatalf("Credentials.Retrieve() error = %v", err)
			}

			if creds.AccessKeyID != "chain-key" || creds.SecretAccessKey != "chain-secret" {
				t.Errorf("resolved credentials = %q/%q, want the chain's %q/%q",
					creds.AccessKeyID, creds.SecretAccessKey, "chain-key", "chain-secret")
			}
		})
	}
}
