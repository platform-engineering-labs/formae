// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

// pluginNamesFromArgs returns the bare plugin names from "name@version"
// args. Orbital install accepts the @version form on the wire too, so
// this is mainly for echoing the requested set back to the user.
func pluginNamesFromArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		name, _, _ := strings.Cut(a, "@")
		out = append(out, name)
	}
	return out
}

func PluginInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install <name>[@<version>]...",
		Short: "Install plugins on this host",
		Long: `Install plugins on this host's local orbital tree.

formae plugin install runs orbital locally — it does not call the agent.
Resource plugins must be installed on the host that runs the agent (run
this command there, typically under sudo). Auth plugins run on both
sides of the CLI⇄agent exchange, so install them on every host that
runs either.`,
		Args: cobra.MinimumNArgs(1),
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

			// orbital may re-exec under sudo when the tree path is
			// privileged. The re-exec restarts main and re-runs this
			// command, so we keep all user-visible output below the
			// install call to avoid duplicated lines.
			if err := mgr.LocalInstall(args); err != nil {
				return err
			}

			for _, name := range pluginNamesFromArgs(args) {
				fmt.Printf("  %s Installed %s\n", display.Green("✓"), name)
			}
			fmt.Printf("\n  %s If this host runs the formae agent, restart it to load the new plugins: formae agent restart\n", display.Gold("!"))
			return nil
		},
		SilenceErrors: true,
	}
	c.Flags().String("channel", "", "Install from a different channel")
	return c
}
