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

// parsePackageRefs parses "name" or "name@version" strings into PackageRefs.
func parsePackageRefs(args []string) []apimodel.PackageRef {
	refs := make([]apimodel.PackageRef, 0, len(args))
	for _, arg := range args {
		name, version, _ := strings.Cut(arg, "@")
		refs = append(refs, apimodel.PackageRef{Name: name, Version: version})
	}
	return refs
}

// filterAuthOps returns the names of auth-type plugin operations.
func filterAuthOps(ops []apimodel.PluginOperation) []string {
	var names []string
	for _, op := range ops {
		if op.Type == "auth" {
			names = append(names, op.Name)
		}
	}
	return names
}

func PluginInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install <name>[@<version>]...",
		Short: "Install plugins on the agent (and locally for auth plugins)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			req := apimodel.InstallPluginsRequest{Packages: parsePackageRefs(args)}
			resp, err := app.InstallPlugins(req)
			if err != nil {
				return err
			}

			for _, op := range resp.Operations {
				fmt.Printf("  %s Installed %s %s on agent\n", display.Green("✓"), op.Name, op.Version)
			}

			// Dual-install for auth plugins
			authNames := filterAuthOps(resp.Operations)
			if len(authNames) > 0 {
				localMgr, err := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories)
				if err != nil {
					fmt.Printf("  %s CLI-side install failed: %s\n", display.Gold("!"), err.Error())
					fmt.Printf("  Retry with: formae plugin install %s\n", strings.Join(authNames, " "))
					return err
				}
				if localMgr != nil {
					if localErr := localMgr.LocalInstall(authNames); localErr != nil {
						fmt.Printf("  %s CLI-side install failed: %s\n", display.Gold("!"), localErr.Error())
						fmt.Printf("  Agent has the plugins. Retry with: formae plugin install %s\n", strings.Join(authNames, " "))
						return localErr
					}
					for _, name := range authNames {
						fmt.Printf("  %s Installed %s on cli\n", display.Green("✓"), name)
					}
				}
			}

			if resp.RequiresRestart {
				fmt.Printf("\n  %s Restart the agent to load the new plugins: formae agent restart\n", display.Gold("!"))
			}
			return nil
		},
		SilenceErrors: true,
	}
	return c
}
