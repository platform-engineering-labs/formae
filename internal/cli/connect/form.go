// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/platform-engineering-labs/formae/internal/cli/cloudapi"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// The permission lines the confirmation states, verbatim per the design: what
// the trust grants when connect provisions it, and what it grants when the
// user brought the role themselves.
const (
	permissionsProvisioned = "PowerUserAccess + IAM management — the same permissions a self-hosted formae agent runs with"
	permissionsAsApplied   = "permissions as applied; not verified by the CLI"
)

// confirmFn runs one themed yes/no confirmation. A seam so the interactive
// consents are testable without a TTY.
var confirmFn = components.RunConfirm

// promptRoleArnFn is the in-sitting quick-create wait: a paste prompt titled
// with the RoleArn output name, not a poll — the CLI holds no AWS credentials
// on the quick-create path, so it cannot watch CREATE_COMPLETE.
var promptRoleArnFn = func(th *theme.Theme, expectedArn string) (string, error) {
	var arn string
	input := huh.NewInput().
		Title("RoleArn stack output").
		Description("Apply both stacks in the console, then paste the role stack's RoleArn output here.\nExpected: " + expectedArn).
		Value(&arn)
	if err := components.NewThemedForm(th, huh.NewGroup(input)).Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(arn), nil
}

// buildConnectForm composes the input form. Every question a flag already
// answered is skipped: the group hides itself when its value is set.
func buildConnectForm(th *theme.Theme, v *formValues, awsProfiles []string) *huh.Form {
	cloudGroup := huh.NewGroup(
		huh.NewSelect[string]().
			Title("Cloud").
			Description("The cloud the account lives in").
			Options(huh.NewOption("AWS", "aws")).
			Value(&v.Cloud),
	).WithHideFunc(func() bool { return v.Cloud != "" })

	accountGroup := huh.NewGroup(
		huh.NewInput().
			Title("AWS account id").
			Description("12 digits; always explicit, never inferred from credentials").
			Value(&v.Account).
			Validate(func(s string) error {
				if !accountRE.MatchString(strings.TrimSpace(s)) {
					return errors.New("an AWS account id is exactly 12 digits")
				}
				return nil
			}),
	).WithHideFunc(func() bool { return v.Account != "" })

	howGroup := huh.NewGroup(
		huh.NewSelect[string]().
			Title("How should the trust be established?").
			Options(
				huh.NewOption("Quick-create links: apply two CloudFormation stacks in the console", "quick-create"),
				huh.NewOption("Provision directly with a local AWS profile", "profile"),
				huh.NewOption("I already have a role: register it", "role-arn"),
			).
			Value(&v.How),
	).WithHideFunc(func() bool { return v.How != "" })

	profileQuestion := func() huh.Field {
		if len(awsProfiles) > 0 {
			profileOptions := make([]huh.Option[string], len(awsProfiles))
			for i, profile := range awsProfiles {
				profileOptions[i] = huh.NewOption(profile, profile)
			}
			return huh.NewSelect[string]().
				Title("AWS profile").
				Options(profileOptions...).
				Value(&v.ProfileAWS)
		}
		return huh.NewInput().
			Title("AWS profile").
			Description("No profiles found in the shared config; name one").
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a profile name is required for the local path")
				}
				return nil
			}).
			Value(&v.ProfileAWS)
	}
	profileGroup := huh.NewGroup(profileQuestion()).
		WithHideFunc(func() bool { return v.How != "profile" || v.ProfileAWS != "" })

	roleArnGroup := huh.NewGroup(
		huh.NewInput().
			Title("Role ARN").
			Description("The role's ARN (arn:aws:iam::<account>:role/<name>)").
			Value(&v.RoleArn).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a role ARN is required")
				}
				return nil
			}),
	).WithHideFunc(func() bool { return v.How != "role-arn" || v.RoleArn != "" })

	return components.NewThemedForm(th, cloudGroup, accountGroup, howGroup, profileGroup, roleArnGroup)
}

// confirmInteractive runs the consents an interactive run needs before it
// touches anything: the multi-installation warning, when the hint raised one,
// and the final confirmation stating the account, the subject, and what the
// trust grants.
func confirmInteractive(th *theme.Theme, account, subject, permissions string, elsewhere []cloudapi.ConnectedAccount) error {
	if len(elsewhere) > 0 {
		ok, err := confirmFn(th, "This account is already connected elsewhere. Connect it here too?",
			multiInstallationWarning(account, elsewhere))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("connect aborted: the account stays connected only where it already is")
		}
	}
	ok, err := confirmFn(th, fmt.Sprintf("Connect aws account %s?", account),
		fmt.Sprintf("subject: %s\npermissions: %s", subject, permissions))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("connect aborted before anything was created or registered")
	}
	return nil
}
