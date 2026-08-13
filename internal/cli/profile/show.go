// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/configview"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
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

			// PrintBanner also surfaces the loaded profile's config warnings,
			// including its deprecation notices, on stderr.
			a.PrintBanner()
			view := configview.From(name, a.Config, configview.HumanMask)
			_, _ = fmt.Fprintln(cc.OutOrStdout(), renderConfigView(a.Theme(), view))
			return nil
		},
	}
	addOutputFlags(c)
	return c
}
