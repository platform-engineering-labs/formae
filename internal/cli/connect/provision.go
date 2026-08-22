// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// provisioner is provx narrowed to the one call the local path makes.
type provisioner interface {
	Create(ctx context.Context) (*provisionResult, error)
}

// provisionResult is the slice of provx's result the local path consumes:
// the role it stands behind, and the policies it had to detach to converge.
type provisionResult struct {
	RoleArn          string
	DetachedPolicies []string
}

// newProvisioner is the seam the provx integration fills: production will
// construct provx with the server-produced subject and role name verbatim —
// connect has no naming knowledge of its own. Nil until that module lands,
// and the local path declares exactly that while it is.
var newProvisioner func(ctx context.Context, caller verifiedCaller,
	subject, roleName, issuer string) (provisioner, error)

// runLocal is the --profile-aws path: verify the caller with STS, provision
// the trust through provx, and register the resulting role.
func runLocal(cc *cobra.Command, opts options, consumer printer.Consumer, schema string) error {
	if newProvisioner == nil {
		return printer.Fail(printer.CodeProvisionFailed,
			"local provisioning lands with the provx integration; connect with --quick-create or --role-arn meanwhile", nil)
	}

	caller, err := verifyCaller(cc.Context(), opts.ProfileAWS, opts.Region, opts.Account)
	if err != nil {
		return err
	}

	s, err := openSession(cc.Context(), opts)
	if err != nil {
		return err
	}

	warnings := s.Warnings
	if elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, opts.Account, s.InstallationID); len(elsewhere) > 0 {
		warnings = append(warnings, multiInstallationWarning(opts.Account, elsewhere))
	}

	// The subject, role name, and issuer travel verbatim from the setup read;
	// provx parses the issuer internally.
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
	return printRegisteredHuman(cc.OutOrStdout(), v, s.InstallationID)
}

// classifyProvision maps provisioning failures onto declared codes. The typed
// provx errors (account mismatch, role collision, provider conflict) join
// this classification with the provx integration; anything untyped is
// provision_failed with the honest what-stands message: re-running converges,
// so the message says so instead of promising a rollback.
func classifyProvision(err error) error {
	var f *printer.Failure
	if errors.As(err, &f) {
		return err
	}
	return printer.Fail(printer.CodeProvisionFailed,
		"provisioning did not complete; what was created stands, and re-running this command converges it", nil)
}
