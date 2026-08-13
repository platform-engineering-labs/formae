// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/configview"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func newShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show [<name>]",
		Short: "Print a profile's resolved configuration",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			consumer, schema, err := resolveOutput(cc)
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}

			name := ""
			if len(args) == 1 {
				name = args[0]
				if err := store.ValidateName(name); err != nil {
					return err
				}
			} else if name, err = s.Active(); err != nil {
				return err
			}

			a := &app.App{}
			if err := a.LoadConfig(s.ProfilePath(name), ""); err != nil {
				return fmt.Errorf("failed to read profile %q: %w", name, err)
			}

			if consumer == printer.ConsumerMachine {
				view := configview.From(name, a.Config, configview.Redacted)
				p := printer.NewMachineReadablePrinter[map[string]any](cc.OutOrStdout(), schema)
				return p.Print(&view)
			}

			// The theme follows the active profile, like every other command:
			// it is the user's environment, not a property of the profile
			// being displayed. Resolved only on a TTY, so piped output stays a
			// pure read of the named profile.
			w := cc.OutOrStdout()
			th := theme.New("formae")
			if isTerminal(w) {
				th = applyTheme(cc)
			}
			banner.PrintBanner()

			// The shown profile's own warnings, including its deprecation
			// notices, go to stderr so they never enter piped output.
			printConfigWarnings(cc.ErrOrStderr(), th, a.Config.Warnings)

			view := configview.From(name, a.Config, configview.HumanMask)
			_, _ = fmt.Fprintln(w, renderConfigView(th, view))
			return nil
		},
	}
	addOutputFlags(c)
	return c
}
