// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package update

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/agent"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
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
			channel, _ := cmd.Flags().GetString("channel")
			configFile, _ := cmd.Flags().GetString("config")
			version := cmd.Flags().Arg(0)

			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			var orb *mgr.Manager
			if len(app.Config.Artifacts.Repositories) > 0 {
				orb, err = opsmgr.NewFromRepositoriesFiltered(slog.Default(), app.Config.Artifacts.Repositories, channel, pkgmodel.RepositoryTypeBinary)
			} else {
				orb, err = opsmgr.New(slog.Default(), app.Config.Artifacts.URL, channel)
			}
			if err != nil {
				return err
			}

			// init root if needed
			if !orb.Ready() {
				fmt.Printf("no managed installation root detected at: %s\n", orb.Path)
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

			available, err := orb.AvailableFor("formae")
			if err != nil {
				return err
			}

			var candidate *records.Package
			var hasUpdate bool
			var hasVersion bool

			if version == "" {
				if hasUpdate, candidate = available.HasUpdate(); !hasUpdate {
					fmt.Println("no updates available")
					return nil
				}
			} else {
				v := &ops.Version{}
				err := v.Parse(version)
				if err != nil {
					return fmt.Errorf("could not parse version: %w", err)
				}

				if hasVersion, candidate = available.HasVersion(v); !hasVersion {
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

	command.Flags().String("channel", "", "Override update channel")
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
			channel, _ := cmd.Flags().GetString("channel")
			configFile, _ := cmd.Flags().GetString("config")

			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			var orb *mgr.Manager
			if len(app.Config.Artifacts.Repositories) > 0 {
				orb, err = opsmgr.NewFromRepositoriesFiltered(slog.Default(), app.Config.Artifacts.Repositories, channel, pkgmodel.RepositoryTypeBinary)
			} else {
				orb, err = opsmgr.New(slog.Default(), app.Config.Artifacts.URL, channel)
			}
			if err != nil {
				return err
			}

			if !orb.Ready() {
				return fmt.Errorf("no managed installation root detected at: %s\n", orb.Path)
			}

			available, err := orb.AvailableFor("formae")
			if err != nil {
				return err
			}

			fmt.Print("available formae versions:\n\n")
			for _, entry := range available.Available {
				fmt.Printf("  %s\n", entry.Version.String())
			}

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().String("channel", "", "Override update channel")
	command.Flags().String("config", "", "Path to config file")

	return command
}
