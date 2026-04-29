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
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func PluginUpgradeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "upgrade [<name>[@<version>]...]",
		Short: "Upgrade plugins on the agent (and locally for auth plugins)",
		Long:  "Upgrade one or more plugins. If no arguments are given, all installed plugins are upgraded.",
		RunE: func(cc *cobra.Command, args []string) error {
			channel, _ := cc.Flags().GetString("channel")
			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			req := apimodel.UpgradePluginsRequest{
				Packages: parsePackageRefs(args),
				Channel:  channel,
			}
			resp, err := app.UpgradePlugins(req)
			if err != nil {
				return err
			}

			if len(resp.Operations) == 0 {
				fmt.Println("  All plugins are up to date.")
				return nil
			}

			// Build the CLI-local manager before printing the agent results
			// to avoid duplicated output if orbital re-execs us under sudo.
			// See install.go for the full explanation.
			authNames := filterAuthOps(resp.Operations)
			var localMgr *CLIPluginManager
			if len(authNames) > 0 {
				localMgr, err = NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories)
				if err != nil {
					fmt.Printf("  %s CLI-side upgrade failed: %s\n", display.Gold("!"), err.Error())
					fmt.Printf("  Retry with: formae plugin upgrade %s\n", strings.Join(authNames, " "))
					return err
				}
			}

			for _, op := range resp.Operations {
				fmt.Printf("  %s Upgraded %s to %s on agent\n", display.Green("✓"), op.Name, op.Version)
			}

			if localMgr != nil {
				if localErr := localMgr.LocalUpgrade(authNames); localErr != nil {
					fmt.Printf("  %s CLI-side upgrade failed: %s\n", display.Gold("!"), localErr.Error())
					fmt.Printf("  Agent has the updated plugins. Retry with: formae plugin upgrade %s\n", strings.Join(authNames, " "))
					return localErr
				}
				for _, name := range authNames {
					fmt.Printf("  %s Upgraded %s on cli\n", display.Green("✓"), name)
				}
			}

			if resp.RequiresRestart {
				fmt.Printf("\n  %s Restart the agent to load the updated plugins: formae agent restart\n", display.Gold("!"))
			}
			return nil
		},
		SilenceErrors: true,
	}
	c.Flags().String("channel", "", "Upgrade from a different channel")
	return c
}
