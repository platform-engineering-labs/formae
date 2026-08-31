// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"context"
	"errors"
	"log/slog"

	provxgcp "github.com/platform-engineering-labs/oox/provx/gcp"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// gcpProvisioner is provx narrowed to the one call the local path makes.
type gcpProvisioner interface {
	Create(ctx context.Context) (*provxgcp.Result, error)
}

// newGCPProvisioner is the seam tests substitute. Production constructs provx
// with the server-produced subject verbatim: connect has no naming knowledge
// of its own, and inventing a subject here would produce trust the issuer
// never mints for.
var newGCPProvisioner = func(ctx context.Context, project, subject, issuer string) (gcpProvisioner, error) {
	tenantID, installationID, err := splitSubject(subject)
	if err != nil {
		return nil, err
	}
	return provxgcp.New(ctx, slog.Default(), project, tenantID, installationID, issuer)
}

// provisionGCP converges the project's federation and reports what it created.
func provisionGCP(ctx context.Context, project, subject, issuer string) (*provxgcp.Result, error) {
	p, err := newGCPProvisioner(ctx, project, subject, issuer)
	if err != nil {
		return nil, classifyGCPProvision(err)
	}
	result, err := p.Create(ctx)
	if err != nil {
		return nil, classifyGCPProvision(err)
	}
	return result, nil
}

// splitSubject takes the server-produced subject apart into the two ids provx
// composes it from. The subject is authoritative and travels verbatim: this
// only reverses the composition, and refuses anything it does not recognise
// rather than guessing.
func splitSubject(subject string) (tenantID, installationID string, err error) {
	const prefix = "fai:"
	rest, ok := cutPrefix(subject, prefix)
	if !ok {
		return "", "", printer.Fail(printer.CodeProvisionFailed,
			"the control plane named a cloud subject this build does not recognise", nil)
	}
	tenantID, installationID, ok = cut(rest, "/")
	if !ok || tenantID == "" || installationID == "" {
		return "", "", printer.Fail(printer.CodeProvisionFailed,
			"the control plane named a cloud subject this build does not recognise", nil)
	}
	return tenantID, installationID, nil
}

// classifyGCPProvision maps provx's typed errors onto declared codes.
//
// The distinction between a disabled API and a denied permission is the point:
// both arrive from Google as HTTP 403, and the remedies have nothing in
// common. Telling someone to enable an API that is already enabled is the most
// confusing way available to waste their time.
func classifyGCPProvision(err error) error {
	var disabled *provxgcp.APIDisabledError
	if errors.As(err, &disabled) {
		return printer.Fail(printer.CodeApiDisabled,
			"a Google API this connection needs is not enabled on the project; enable it and re-run",
			map[string]any{"api": disabled.API})
	}

	var denied *provxgcp.PermissionDeniedError
	if errors.As(err, &denied) {
		details := map[string]any{}
		if denied.Permission != "" {
			details["permission"] = denied.Permission
		}
		return printer.Fail(printer.CodeNotAuthorized,
			"these credentials may not create the federation this connection needs in that project", details)
	}

	var policy *provxgcp.OrgPolicyError
	if errors.As(err, &policy) {
		return printer.Fail(printer.CodeNotAuthorized,
			"an organization policy refused this change; neither a permission grant nor enabling an API will lift it", nil)
	}

	var unreachable *provxgcp.ProjectUnreachableError
	if errors.As(err, &unreachable) {
		// Deliberately not a credentials failure: signing in again returns the
		// same principal, and would overwrite credentials configured on
		// purpose.
		return printer.Fail(printer.CodeProjectUnreachable,
			"the project could not be read with these credentials; check the project id, and that this principal can see it", nil)
	}

	var notOurs *provxgcp.ProviderNotOursError
	if errors.As(err, &notOurs) {
		return printer.Fail(printer.CodeProviderConflict,
			"a workload identity provider of the name formae uses already exists in this project and trusts a different issuer; "+
				"formae will not modify it", nil)
	}

	return printer.Fail(printer.CodeProvisionFailed,
		"provisioning did not complete; what was created stands, and re-running this command converges it", nil)
}

// cutPrefix and cut keep this file free of a strings import for two one-liners
// whose behaviour is worth stating at the call site.
func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func cut(s, sep string) (before, after string, found bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
