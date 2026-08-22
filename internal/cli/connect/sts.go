// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// verifiedCaller is what the local path may proceed with: the stated account,
// confirmed against the credentials, in the commercial partition.
type verifiedCaller struct {
	Account string
	Arn     string
	Cfg     aws.Config // carried into the provisioner
}

// loadAWSConfig loads the shared config for the profile. The region option is
// added only when the flag was passed: an unconditional WithRegion("") would
// clobber the region the profile itself carries.
var loadAWSConfig = func(ctx context.Context, profile, region string) (aws.Config, error) {
	loadOptions := []func(*config.LoadOptions) error{
		config.WithSharedConfigProfile(profile),
	}
	if region != "" {
		loadOptions = append(loadOptions, config.WithRegion(region))
	}
	return config.LoadDefaultConfig(ctx, loadOptions...)
}

// stsEndpoint overrides where GetCallerIdentity is sent; empty in production.
var stsEndpoint string

// verifyCaller confirms, before any IAM call, that the profile's credentials
// authenticate to the stated account in the commercial partition.
func verifyCaller(ctx context.Context, profile, region, statedAccount string) (verifiedCaller, error) {
	cfg, err := loadAWSConfig(ctx, profile, region)
	if err != nil {
		return verifiedCaller{}, classifySSO(err, profile)
	}
	if cfg.Region == "" {
		return verifiedCaller{}, printer.Fail(printer.CodeProvisionFailed,
			"no region: pass --region or set one on the AWS profile", nil)
	}
	client := sts.NewFromConfig(cfg, func(o *sts.Options) {
		if stsEndpoint != "" {
			o.BaseEndpoint = &stsEndpoint
		}
	})
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return verifiedCaller{}, classifySSO(err, profile)
	}
	if !strings.HasPrefix(aws.ToString(out.Arn), "arn:aws:") {
		return verifiedCaller{}, printer.Fail(printer.CodeUnsupportedPartition,
			"the credentials belong to a non-commercial AWS partition, which connect does not support", nil)
	}
	if aws.ToString(out.Account) != statedAccount {
		return verifiedCaller{}, printer.Fail(printer.CodeAccountMismatch,
			fmt.Sprintf("profile %q authenticates to account %s, not the stated %s",
				profile, aws.ToString(out.Account), statedAccount), nil)
	}
	return verifiedCaller{Account: statedAccount, Arn: aws.ToString(out.Arn), Cfg: cfg}, nil
}

// classifySSO turns an expired SSO session into the one failure whose remedy
// is a command the user can paste, and leaves everything else alone.
func classifySSO(err error, profile string) error {
	var invalid *ssocreds.InvalidTokenError
	if errors.As(err, &invalid) {
		return printer.Fail(printer.CodeSSOLoginRequired,
			"the AWS SSO session for this profile has expired",
			map[string]any{"command": "aws sso login --profile " + profile})
	}
	return err
}
