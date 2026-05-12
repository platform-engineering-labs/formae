// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

func PluginUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update [<name>[@<version>]...]",
		Short: "Update plugins on this host",
		Long: `Update plugins on this host's local orbital tree. With no
arguments, every installed plugin is considered for update.

formae plugin update runs orbital locally — it does not call the
agent. Run it on every host where the plugin is installed.`,
		RunE: func(cc *cobra.Command, args []string) error {
			channel, _ := cc.Flags().GetString("channel")
			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, channel)
			if err != nil {
				return err
			}
			if mgr == nil {
				return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
			}

			if err := mgr.LocalUpdate(args); err != nil {
				return err
			}

			if len(args) == 0 {
				fmt.Printf("  %s Updated all installed plugins\n", display.Green("✓"))
			} else {
				for _, name := range pluginNamesFromArgs(args) {
					fmt.Printf("  %s Updated %s\n", display.Green("✓"), name)
				}
			}
			fmt.Printf("\n  %s If this host runs the formae agent, restart it to load the updated plugins: formae agent restart\n", display.Gold("!"))
			return nil
		},
		SilenceErrors: true,
	}
	c.Flags().String("channel", "", "Update from a different channel")
	return c
}
