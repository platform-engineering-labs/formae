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

func PluginUninstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "uninstall <name>...",
		Aliases: []string{"remove"},
		Short:   "Uninstall plugins from this host",
		Long: `Remove plugins from this host's local orbital tree.

formae plugin uninstall runs orbital locally — it does not call the
agent. Run it on the host where the plugin is installed; for auth
plugins, run it on every host where the plugin lives.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			mgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, "")
			if err != nil {
				return err
			}
			if mgr == nil {
				return fmt.Errorf("no artifact repositories configured; set artifacts.repositories in your formae config")
			}

			if err := mgr.LocalUninstall(args); err != nil {
				return err
			}

			for _, name := range args {
				fmt.Printf("  %s Removed %s\n", display.Green("✓"), name)
			}
			fmt.Printf("\n  %s If this host runs the formae agent, restart it to unload the plugins: formae agent restart\n", display.Gold("!"))
			return nil
		},
		SilenceErrors: true,
	}
	return c
}
