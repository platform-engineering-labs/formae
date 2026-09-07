// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	provxaws "github.com/platform-engineering-labs/oox/provx/aws"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// provisioner is provx narrowed to the one call the local path makes.
type provisioner interface {
	Create(ctx context.Context) (*provxaws.Result, error)
}

// newProvisioner is the seam tests substitute; production constructs provx
// with the server-produced subject and role name verbatim — connect has no
// naming knowledge of its own. provx parses the issuer internally.
var newProvisioner = func(ctx context.Context, caller verifiedCaller,
	subject, roleName, issuer string) (provisioner, error) {
	return provxaws.New(ctx, caller.Cfg.Credentials, caller.Cfg.Region,
		caller.Account, subject, roleName, issuer)
}

// runLocal is the --profile-aws path: verify the caller with STS, provision
// the trust through provx, and register the resulting role.
func runLocal(cc *cobra.Command, opts options, consumer printer.Consumer, schema string) error {
	caller, err := verifyCaller(cc.Context(), opts.ProfileAWS, opts.Account)
	if err != nil {
		return err
	}

	s, err := openSession(cc.Context(), opts)
	if err != nil {
		return err
	}

	warnings := s.Warnings
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, "aws", opts.Account, s.InstallationID)
	if len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Account, elsewhere))
	}
	if interactiveRun(opts, consumer) {
		th := clicmd.ResolveConfiguredTheme(cc)
		if err := confirmInteractive(th, "aws", "account", opts.Account, s.Setup.CloudSubject, permissionsProvisioned, elsewhere); err != nil {
			return err
		}
	}

	// The subject, role name, and issuer travel verbatim from the setup read.
	p, err := newProvisioner(cc.Context(), caller, s.Setup.CloudSubject, s.Setup.CloudRoleName, s.Platform.Issuer)
	if err != nil {
		return classifyProvision(err)
	}
	result, err := p.Create(cc.Context())
	if err != nil {
		return classifyProvision(err)
	}
	for _, policy := range result.DetachedPolicies {
		warnings = append(warnings, fmt.Sprintf("detached the drifted policy %s while converging the role", policy))
	}
	for _, policy := range result.DeletedInline {
		warnings = append(warnings, fmt.Sprintf("deleted the drifted inline policy %s while converging the role", policy))
	}

	// Non-atomicity accepted and stated: a provision-succeeded/registration-
	// failed run leaves the role standing, and re-running this command (or
	// finishing with --role-arn) converges.
	status, err := s.register(cc.Context(), opts.Account, result.RoleArn)
	if err != nil {
		return err
	}

	v := registeredDocument(status, opts.Account, result.RoleArn, warnings)
	if consumer == printer.ConsumerMachine {
		return emitRegistered(cc.OutOrStdout(), schema, v)
	}
	return printRegisteredHuman(cc.OutOrStdout(), isInteractive(), clicmd.ResolveConfiguredTheme(cc), v, s.InstallationID)
}

// classifyProvision maps provx's typed errors onto declared codes. Anything
// untyped is provision_failed with the honest what-stands message: re-running
// converges, so the message says so instead of promising a rollback.
func classifyProvision(err error) error {
	var mismatch *provxaws.AccountMismatchError
	if errors.As(err, &mismatch) {
		return printer.Fail(printer.CodeAccountMismatch,
			"the credentials authenticate to a different account than the one stated", nil)
	}
	var collision *provxaws.RoleCollisionError
	if errors.As(err, &collision) {
		return printer.Fail(printer.CodeRoleCollision,
			"a role with the expected name exists but is not one connect owns; delete it (or its stack) and re-run, or connect with --role-arn", nil)
	}
	var conflict *provxaws.ProviderConflictError
	if errors.As(err, &conflict) {
		return printer.Fail(printer.CodeProviderConflict,
			"the formae OIDC identity provider exists with an unexpected configuration", nil)
	}
	return printer.Fail(printer.CodeProvisionFailed,
		"provisioning did not complete; what was created stands, and re-running this command converges it", nil)
}
