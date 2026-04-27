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
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func PluginUninstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "uninstall <name>...",
		Aliases: []string{"remove"},
		Short:   "Uninstall plugins from the agent (and locally for auth plugins)",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			refs := make([]apimodel.PackageRef, 0, len(args))
			for _, name := range args {
				refs = append(refs, apimodel.PackageRef{Name: name})
			}

			resp, err := app.UninstallPlugins(apimodel.UninstallPluginsRequest{Packages: refs})
			if err != nil {
				return err
			}

			// Build the CLI-local manager before printing the agent results
			// to avoid duplicated output if orbital re-execs us under sudo.
			// See install.go for the full explanation.
			authNames := filterAuthOps(resp.Operations)
			var localMgr *CLIPluginManager
			if len(authNames) > 0 {
				localMgr, _ = NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories)
			}

			for _, op := range resp.Operations {
				fmt.Printf("  %s Removed %s from agent\n", display.Green("✓"), op.Name)
			}

			if localMgr != nil {
				if localErr := localMgr.LocalUninstall(authNames); localErr != nil {
					fmt.Printf("  %s CLI-side uninstall failed: %s\n", display.Gold("!"), localErr.Error())
				} else {
					for _, name := range authNames {
						fmt.Printf("  %s Removed %s from cli\n", display.Green("✓"), name)
					}
				}
			}

			if resp.RequiresRestart {
				fmt.Printf("\n  %s Restart the agent to unload the plugins: formae agent restart\n", display.Gold("!"))
			}
			return nil
		},
		SilenceErrors: true,
	}
	return c
}
