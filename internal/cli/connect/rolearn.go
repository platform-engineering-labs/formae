// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package connect

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// runRegisterOnly is the --role-arn path: trust already exists — an applied
// quick-create stack, or a role the user made themselves — so the run
// validates the ARN, reads the coordinates, and registers. Validation happens
// before any control-plane write; the soft name comparison happens after the
// setup read, as a warning.
func runRegisterOnly(cc *cobra.Command, opts options, consumer printer.Consumer, schema string) error {
	parsed, err := parseRoleArn(opts.RoleArn, opts.Account)
	if err != nil {
		return err
	}

	s, err := openSession(cc.Context(), opts)
	if err != nil {
		return err
	}

	warnings := s.Warnings
	if w := warnOnNameMismatch(parsed.RoleName, s.Setup.CloudRoleName); w != "" {
		warnings = append(warnings, w)
	}
	elsewhere := connectedElsewhere(s.Setup.AccountsConnectedHint, opts.Account, s.InstallationID)
	if len(elsewhere) > 0 {
		// In --no-input the warning rides the machine document and the run
		// proceeds; interactively it is confirmed below.
		warnings = append(warnings, multiInstallationWarning(opts.Account, elsewhere))
	}
	if interactiveRun(opts, consumer) {
		th := clicmd.ResolveConfiguredTheme(cc)
		if err := confirmInteractive(th, opts.Account, s.Setup.CloudSubject, permissionsAsApplied, elsewhere); err != nil {
			return err
		}
	}

	status, err := s.register(cc.Context(), opts.Account, parsed.Arn)
	if err != nil {
		return err
	}

	v := registeredDocument(status, opts.Account, parsed.Arn, warnings)
	if consumer == printer.ConsumerMachine {
		return emitRegistered(cc.OutOrStdout(), schema, v)
	}
	return printRegisteredHuman(cc.OutOrStdout(), v, s.InstallationID)
}

// printRegisteredHuman renders the same facts as prose.
func printRegisteredHuman(w io.Writer, v registeredView, installationID string) error {
	verb := "registered"
	if v.Status == statusAlreadyRegistered {
		verb = "already registered"
	}
	if _, err := fmt.Fprintf(w, "✓ %s aws account %s on installation %s\n", verb, v.Account, installationID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  role: %s\n", v.RoleArn); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "  The registration is declared by you, not verified by formae."); err != nil {
		return err
	}
	for _, warning := range v.Warnings {
		if _, err := fmt.Fprintf(w, "  warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}
