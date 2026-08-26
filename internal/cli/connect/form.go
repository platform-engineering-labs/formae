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
	// The GCP equivalent, named the way the design insists on: near-owner. A
	// principal that can edit resources and rewrite project IAM can grant
	// itself more, so describing it as "editor" alone would undersell what is
	// being handed over.
	permissionsProvisionedGCP = "editor + project IAM admin — near-owner, and the same permissions a self-hosted formae agent runs with"
	permissionsAsApplied      = "permissions as applied; not verified by the CLI"
)

// confirmFn runs one themed yes/no confirmation. A seam so the interactive
// consents are testable without a TTY.
var confirmFn = components.RunConfirm

// confirmProviderExistsFn asks whether the shared identity provider already
// exists in the account. Asked only when the connection hint knows the
// account — a fresh account skips the question and creates the provider. A
// seam so the flow is testable without a TTY.
var confirmProviderExistsFn = func(th *theme.Theme, account string) (bool, error) {
	exists := true
	confirm := huh.NewConfirm().
		Title("Account " + account + " looks connected to formae already").
		Description("IAM allows one formae identity provider per account. Does it already exist?").
		Affirmative("It exists — create the role only").
		Negative("First connection — create both").
		Value(&exists)
	if err := components.NewThemedForm(th, huh.NewGroup(confirm)).Run(); err != nil {
		return false, err
	}
	return exists, nil
}

// promptRoleArnFn is the in-sitting quick-create wait: a paste prompt titled
// with the RoleArn output name, not a poll — the CLI holds no AWS credentials
// on the quick-create path, so it cannot watch CREATE_COMPLETE.
var promptRoleArnFn = func(th *theme.Theme, expectedArn string) (string, error) {
	var arn string
	input := huh.NewInput().
		Title("RoleArn stack output").
		Description("Apply the stack in the console, then press Enter once it shows CREATE_COMPLETE.\n" +
			"Expected: " + expectedArn + "\n" +
			"Paste the RoleArn output only if it differs.").
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
			Options(huh.NewOption("AWS", "aws"), huh.NewOption("GCP", "gcp")).
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
	).WithHideFunc(func() bool { return v.Cloud != "aws" || v.Account != "" })

	// GCP asks for a project and nothing else. There is no "how" question,
	// because GCP has one path: no console flow exists to offer as an
	// alternative to provisioning with local credentials.
	projectGroup := huh.NewGroup(
		huh.NewInput().
			Title("GCP project id").
			Description("Always explicit, never inferred from credentials").
			Value(&v.Project).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a project id is required")
				}
				return nil
			}),
	).WithHideFunc(func() bool { return v.Cloud != "gcp" || v.Project != "" })

	howGroup := huh.NewGroup(
		huh.NewSelect[string]().
			Title("How should the trust be established?").
			Options(
				huh.NewOption("Quick-create links: apply two CloudFormation stacks in the console", "quick-create"),
				huh.NewOption("Provision directly with a local AWS profile", "profile"),
				huh.NewOption("I already have a role: register it", "role-arn"),
			).
			Value(&v.How),
	).WithHideFunc(func() bool { return v.Cloud != "aws" || v.How != "" })

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
		WithHideFunc(func() bool { return v.Cloud != "aws" || v.How != "profile" || v.ProfileAWS != "" })

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
	).WithHideFunc(func() bool { return v.Cloud != "aws" || v.How != "role-arn" || v.RoleArn != "" })

	return components.NewThemedForm(th, cloudGroup, accountGroup, projectGroup, howGroup, profileGroup, roleArnGroup)
}

// confirmInteractive runs the consents an interactive run needs before it
// touches anything: the multi-installation warning, when the hint raised one,
// and the final confirmation stating the account, the subject, and what the
// trust grants.
// cloud and noun name the thing being connected in its own words: an aws
// account, a gcp project. Asking "Connect aws account my-project?" over a GCP
// run is wrong in a prompt whose entire job is to state plainly what is about
// to happen.
func confirmInteractive(th *theme.Theme, cloud, noun, account, subject, permissions string,
	elsewhere []cloudapi.ConnectedAccount) error {
	if len(elsewhere) > 0 {
		ok, err := confirmFn(th, fmt.Sprintf("This %s is already connected elsewhere. Connect it here too?", noun),
			multiInstallationWarning(account, elsewhere))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("connect aborted: the account stays connected only where it already is")
		}
	}
	ok, err := confirmFn(th, fmt.Sprintf("Connect %s %s %s?", cloud, noun, account),
		fmt.Sprintf("subject: %s\npermissions: %s", subject, permissions))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("connect aborted before anything was created or registered")
	}
	return nil
}
