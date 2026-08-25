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

// defaultRegion is what the local path uses when neither the flag nor the
// profile names a region.
//
// Nothing this path touches is regional. It asks STS which account the
// credentials belong to, then creates an IAM role and the account-global OIDC
// provider that role trusts: STS answers identically from any region, and IAM
// has one global endpoint. A region is required only because an SDK client
// cannot be constructed without one.
//
// So it is defaulted rather than demanded. Credentials in the shared
// credentials file with no region beside them is an ordinary setup, and
// refusing it reports every such profile as unavailable — which withdraws the
// direct-provision path from the connect flow entirely and leaves the console
// link as the only way through, for a preference nothing downstream reads.
const defaultRegion = "us-east-1"

// verifyCaller confirms, before any IAM call, that the profile's credentials
// authenticate to the stated account in the commercial partition.
func verifyCaller(ctx context.Context, profile, region, statedAccount string) (verifiedCaller, error) {
	cfg, account, arn, err := resolveCaller(ctx, profile, region)
	if err != nil {
		return verifiedCaller{}, err
	}
	if account != statedAccount {
		return verifiedCaller{}, printer.Fail(printer.CodeAccountMismatch,
			fmt.Sprintf("profile %q authenticates to account %s, not the stated %s",
				profile, account, statedAccount), nil)
	}
	return verifiedCaller{Account: statedAccount, Arn: arn, Cfg: cfg}, nil
}

// resolveCaller loads the profile's config and asks STS who its credentials
// belong to, without comparing against any stated account. It is the part
// verifyCaller shares with a resolve-only reader (the profiles listing),
// which reports the account rather than confirms it: the client construction
// and the classification of what can go wrong along the way live here once.
func resolveCaller(ctx context.Context, profile, region string) (aws.Config, string, string, error) {
	cfg, err := loadAWSConfig(ctx, profile, region)
	if err != nil {
		return aws.Config{}, "", "", classifySSO(err, profile)
	}
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	client := sts.NewFromConfig(cfg, func(o *sts.Options) {
		if stsEndpoint != "" {
			o.BaseEndpoint = &stsEndpoint
		}
	})
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return aws.Config{}, "", "", classifySSO(err, profile)
	}
	if !strings.HasPrefix(aws.ToString(out.Arn), "arn:aws:") {
		return aws.Config{}, "", "", printer.Fail(printer.CodeUnsupportedPartition,
			"the credentials belong to a non-commercial AWS partition, which connect does not support", nil)
	}
	return cfg, aws.ToString(out.Account), aws.ToString(out.Arn), nil
}

// unavailableReason turns a resolveCaller failure into the text the profiles
// listing reports beside a profile it could not resolve. It is derived from
// the failure's kind, never the failure's own message: the message on an
// undeclared error can quote request detail from the AWS SDK, so only the
// codes we ourselves classified get their own wording, and everything else
// (including a resolution that timed out) shares one generic reason.
func unavailableReason(err error) string {
	var f *printer.Failure
	if errors.As(err, &f) {
		switch f.Code {
		case printer.CodeSSOLoginRequired:
			return "the SSO session has expired"
		case printer.CodeUnsupportedPartition:
			return "the credentials belong to a non-commercial AWS partition"
		}
	}
	return "could not resolve this profile's credentials"
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
