// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package update

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/agent"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/spf13/cobra"
)

func UpdateCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "update [version]",
		Short: "Manage formae binary updates",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			version := cmd.Flags().Arg(0)

			configFile, _ := cmd.Flags().GetString("config")
			_, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			binPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}
			root := filepath.Dir(filepath.Dir(binPath))

			//TODO add configuration to config
			cfg := opsmgr.TreeConfig
			cfg.Repositories[0].Uri.Fragment = "dev"
			orb, err := mgr.New(slog.Default(), root, opsmgr.TreeConfig)
			if err != nil {
				return err
			}

			// init root if needed
			if !orb.Ready() {
				fmt.Printf("no managed installation root detected at: %s\n", root)
				fmt.Print("initialize? [y/n]: ")
				var response string

				_, err := fmt.Scanln(&response)
				if strings.ToLower(response) != "y" || err != nil {
					return nil
				}

				_, err = orb.Initialize()
				if err != nil {
					return err
				}
			}

			err = orb.Refresh()
			if err != nil {
				return err
			}

			available, err := orb.Available()
			if err != nil {
				return err
			}

			var candidate *records.Package
			var hasUpdate bool

			if version == "" {
				if hasUpdate, candidate = available["formae"].HasUpdate(); !hasUpdate {
					fmt.Println("no updates available")
					return nil
				}
			} else {
				v := &ops.Version{}
				err := v.Parse(version)
				if err != nil {
					return fmt.Errorf("could not parse version: %w", err)
				}

				for _, pkg := range available["formae"].Available {
					if pkg.Version.EXQ(v) {
						candidate = pkg
						break
					}
					if pkg.Version.EQ(v) {
						candidate = pkg
						break
					}
				}

				if candidate == nil {
					return fmt.Errorf("could not find formae version: %s", version)
				}
			}

			fmt.Println("stopping formae agent...")
			ag := agent.Agent{}
			err = ag.Stop()
			if err != nil {
				if !strings.Contains(err.Error(), "agent is not running") {
					return err
				}
			}

			fmt.Printf("installing formae version %s\n", candidate.Version.Short())

			err = orb.Install(candidate.Id().String())
			if err != nil {
				return err
			}

			fmt.Println("done.")

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.AddCommand(UpdateListCmd())

	command.Flags().String("config", "", "Path to config file")

	return command
}

func UpdateListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List available formae versions",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} update list",
		},
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("config")
			_, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			binPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}
			root := filepath.Dir(filepath.Dir(binPath))

			//TODO add configuration to config
			cfg := opsmgr.TreeConfig
			cfg.Repositories[0].Uri.Fragment = "dev"
			orb, err := mgr.New(slog.Default(), root, opsmgr.TreeConfig)
			if err != nil {
				return err
			}

			// init root if needed
			if !orb.Ready() {
				fmt.Printf("no managed installation root detected at: %s\n", root)
			}

			available, err := orb.Available()
			if err != nil {
				return err
			}

			fmt.Print("available formae versions:\n\n")
			for _, entry := range available["formae"].Available {
				fmt.Printf("  %s\n", entry.Version.String())
			}

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().String("config", "", "Path to config file")

	return command
}
