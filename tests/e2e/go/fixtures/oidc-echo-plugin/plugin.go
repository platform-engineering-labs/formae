// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const (
	// audience the plugin asks its broker for, and the audience the role's
	// trust policy expects on the token it is handed.
	audience = "sts.amazonaws.com"

	// namespace this plugin serves, as its manifest declares it. Named in the
	// recorded failure so a test can tell whose pairing was missing.
	namespace = "OidcEcho"

	// assumeRoleARN is the standing role the minted token is exchanged for.
	// Its trust policy names the broker's issuer, subject, and audience.
	assumeRoleARN = "arn:aws:iam::942849037363:role/e2e-oidc-assume-role"

	// assumeRoleSessionName labels the STS session. Stable, so the assumed
	// role ARN a test reads back is stable too.
	assumeRoleSessionName = "e2e-oidc-echo"

	// awsRegion is where the STS call is made.
	awsRegion = "us-west-2"
)

// EchoPlugin serves OidcEcho::Tokens::Token. It holds no state beyond the
// token source the SDK installs, which resolves the paired broker per call.
type EchoPlugin struct {
	tokens plugin.OidcTokenSource
}

// SetOidcTokenSource satisfies plugin.OidcAware: the SDK calls it once at
// startup with the source every operation mints through.
func (p *EchoPlugin) SetOidcTokenSource(src plugin.OidcTokenSource) {
	p.tokens = src
}

func (p *EchoPlugin) RateLimit() pkgmodel.RateLimitConfig {
	return pkgmodel.RateLimitConfig{
		Scope:                            pkgmodel.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 5,
	}
}

func (p *EchoPlugin) DiscoveryFilters() []pkgmodel.MatchFilter { return nil }

func (p *EchoPlugin) LabelConfig() pkgmodel.LabelConfig {
	return pkgmodel.LabelConfig{DefaultQuery: "$.probeLabel"}
}

// tokenProperties asks the broker for a token, exchanges it at AWS STS, and
// renders the resource's properties around both outcomes. A failure is
// recorded in tokenError or stsError rather than failed outright, so a test
// asserting the unpaired case reads an exact message instead of whatever an
// operator error renders to. Real plugins fail closed here; this one is only
// proving the wiring.
func (p *EchoPlugin) tokenProperties(ctx context.Context, probeLabel string) (json.RawMessage, error) {
	var token, tokenError string

	switch {
	case p.tokens == nil:
		// The SDK installs the source on every OidcAware plugin, so this only
		// fires if that wiring broke.
		tokenError = fmt.Sprintf("no oidc token source installed: namespace %s", namespace)
	default:
		minted, err := p.tokens.IdentityToken(ctx, audience)
		if err != nil {
			tokenError = fmt.Sprintf("%s: namespace %s", err, namespace)
		} else {
			token = minted
		}
	}

	// With no token there is nothing to exchange, so the STS outputs stay
	// empty rather than carrying the error of a call that was never worth
	// making.
	var assumedRoleARN, expiration, stsError string
	if token != "" {
		assumedRoleARN, expiration, stsError = assumeRoleWithToken(ctx, token)
	}

	return json.Marshal(map[string]string{
		"probeLabel":        probeLabel,
		"token":             token,
		"tokenError":        tokenError,
		"stsAssumedRoleArn": assumedRoleARN,
		"stsExpiration":     expiration,
		"stsError":          stsError,
	})
}

// assumeRoleWithToken exchanges an identity token for role credentials at
// AWS STS and reports proof of the exchange: who the caller became and how
// long the credentials last. The access key and secret are deliberately not
// returned: they would land in the resource's properties, which is not a
// place credentials belong.
func assumeRoleWithToken(ctx context.Context, token string) (assumedRoleARN, expiration, stsError string) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(awsRegion))
	if err != nil {
		return "", "", fmt.Sprintf("loading aws config: %s", err)
	}

	out, err := sts.NewFromConfig(cfg).AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(assumeRoleARN),
		RoleSessionName:  aws.String(assumeRoleSessionName),
		WebIdentityToken: aws.String(token),
	})
	if err != nil {
		return "", "", fmt.Sprintf("assume role with web identity: %s", err)
	}

	if out.AssumedRoleUser != nil && out.AssumedRoleUser.Arn != nil {
		assumedRoleARN = *out.AssumedRoleUser.Arn
	}
	if out.Credentials != nil && out.Credentials.Expiration != nil {
		expiration = out.Credentials.Expiration.Format(time.RFC3339)
	}
	return assumedRoleARN, expiration, ""
}

// probeLabelOf reads the probeLabel property out of a properties document.
func probeLabelOf(properties json.RawMessage) (string, error) {
	var props struct {
		ProbeLabel string `json:"probeLabel"`
	}
	if len(properties) == 0 {
		return "", fmt.Errorf("no properties supplied")
	}
	if err := json.Unmarshal(properties, &props); err != nil {
		return "", fmt.Errorf("parsing properties: %w", err)
	}
	if props.ProbeLabel == "" {
		return "", fmt.Errorf("probeLabel is required")
	}
	return props.ProbeLabel, nil
}

func (p *EchoPlugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	probeLabel, err := probeLabelOf(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: failure(resource.OperationCreate, err)}, nil
	}

	properties, err := p.tokenProperties(ctx, probeLabel)
	if err != nil {
		return &resource.CreateResult{ProgressResult: failure(resource.OperationCreate, err)}, nil
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           probeLabel,
			ResourceProperties: properties,
		},
	}, nil
}

// Read reports the same shape of document Create did, minting a fresh token
// and exchanging it again: there is no store behind this resource, so every
// read redoes the work.
func (p *EchoPlugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	properties, err := p.tokenProperties(ctx, req.NativeID)
	if err != nil {
		return &resource.ReadResult{
			ResourceType: req.ResourceType,
			ErrorCode:    resource.OperationErrorCodeInternalFailure,
		}, nil
	}

	return &resource.ReadResult{
		ResourceType: req.ResourceType,
		Properties:   string(properties),
	}, nil
}

func (p *EchoPlugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	properties, err := p.tokenProperties(ctx, req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: failure(resource.OperationUpdate, err)}, nil
	}

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationUpdate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           req.NativeID,
			ResourceProperties: properties,
		},
	}, nil
}

// Delete has nothing to delete: the resource never existed anywhere.
func (p *EchoPlugin) Delete(_ context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        req.NativeID,
		},
	}, nil
}

// Status is never polled: every operation finishes synchronously.
func (p *EchoPlugin) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCheckStatus,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       req.RequestID,
			NativeID:        req.NativeID,
		},
	}, nil
}

// List returns nothing: the schema marks the resource type undiscoverable.
func (p *EchoPlugin) List(_ context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	return &resource.ListResult{}, nil
}

// failure renders an operation failure carrying err's message.
func failure(operation resource.Operation, err error) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:       operation,
		OperationStatus: resource.OperationStatusFailure,
		ErrorCode:       resource.OperationErrorCodeInvalidRequest,
		StatusMessage:   err.Error(),
	}
}
