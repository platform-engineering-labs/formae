// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/ccx"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

type Secret struct {
	cfg *config.Config
}

var _ prov.Provisioner = &Secret{}

func init() {
	registry.Register("AWS::SecretsManager::Secret",
		[]resource.Operation{
			resource.OperationRead,
			resource.OperationCreate,
			resource.OperationUpdate,
			resource.OperationCheckStatus,
			resource.OperationDelete},
		func(cfg *config.Config) prov.Provisioner {
			return &Secret{cfg: cfg}
		})
}

// Read enhances Cloud Control read with actual secret value
func (s *Secret) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	ccxClient, err := ccx.NewClient(s.cfg)
	if err != nil {
		slog.Error("SecretsManager: Failed to create ccx client", "error", err)
		return nil, err
	}

	result, err := ccxClient.ReadResource(ctx, request)
	if err != nil {
		slog.Error("SecretsManager: Cloud Control ReadResource failed", "error", err)
		return nil, err
	}

	// Don't bother enriching with secret value when RedactSensitive is set
	if request.RedactSensitive {
		return result, nil
	}

	awsCfg, err := s.cfg.ToAwsConfig(ctx)
	if err != nil {
		slog.Error("SecretsManager: Failed to create AWS config", "error", err)
		return nil, err
	}

	secretsClient := secretsmanager.NewFromConfig(awsCfg)

	secret, err := secretsClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &request.NativeID,
	})
	if err != nil {
		// Don't fail the read - just return Cloud Control result without secret value
		slog.Warn("SecretsManager: GetSecretValue failed, returning Cloud Control result only",
			"error", err, "secretID", request.NativeID)
		return result, nil
	}

	var props map[string]any
	raw := strings.TrimSpace(result.Properties)
	if raw == "" {
		props = map[string]any{}
	} else {
		if err := json.Unmarshal([]byte(raw), &props); err != nil {
			slog.Warn("SecretsManager: properties not valid JSON, defaulting to empty", "error", err)
			props = map[string]any{}
		}
	}

	if secret.SecretString != nil {
		props["SecretString"] = *secret.SecretString
	}
	if secret.SecretBinary != nil {
		props["SecretBinary"] = secret.SecretBinary
	}

	completeProps, err := json.Marshal(props)
	if err != nil {
		slog.Error("SecretsManager: Failed to marshal complete properties", "error", err)
		return result, nil
	}

	result.Properties = string(completeProps)
	return result, nil
}

func (s *Secret) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	ccxClient, err := ccx.NewClient(s.cfg)
	if err != nil {
		slog.Error("SecretsManager: Create failed to create ccx client", "error", err)
		return nil, err
	}

	return ccxClient.CreateResource(ctx, request)
}

func (s *Secret) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	ccxClient, err := ccx.NewClient(s.cfg)
	if err != nil {
		slog.Error("SecretsManager: Update failed to create ccx client", "error", err)
		return nil, err
	}

	return ccxClient.UpdateResource(ctx, request)
}

func (s *Secret) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	ccxClient, err := ccx.NewClient(s.cfg)
	if err != nil {
		slog.Error("SecretsManager: Delete failed to create ccx client", "error", err)
		return nil, err
	}

	return ccxClient.DeleteResource(ctx, request)
}

func (s *Secret) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	ccxClient, err := ccx.NewClient(s.cfg)
	if err != nil {
		slog.Error("SecretsManager: Status failed to create ccx client", "error", err)
		return nil, err
	}

	return ccxClient.StatusResource(ctx, request, s.Read)
}

func (s *Secret) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	return nil, fmt.Errorf("list not implemented for secret provisioner - cloudcontrol natively supports this operation")
}
