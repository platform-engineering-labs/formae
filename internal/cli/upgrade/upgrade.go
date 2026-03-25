// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package upgrade

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/masterminds/semver"
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/agent"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/pkg/ppm"
)

func UpgradeCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Install or list available formae binary updates",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			version, _ := cmd.Flags().GetString("version")
			configFile, _ := cmd.Flags().GetString("config")
			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			if !ppm.Sys.IsPrivilegedUser() {
				fmt.Println("this command requires a privileged user: authentication may be required")

				upgradeArgs := []string{cmd.Name()}
				if version != "" {
					upgradeArgs = append(upgradeArgs, "--version", version)
				}

				err := ppm.Sys.InvokeSelfWithSudo(upgradeArgs...)
				if err != nil {
					return fmt.Errorf("could not escalate to privileged user: %w", err)
				}
			}

			repoUri, err := url.Parse(app.Config.Artifacts.URL)
			if err != nil {
				return fmt.Errorf("could not parse repository URL: %s", app.Config.Artifacts.URL)
			}

			manager, err := ppm.NewManager(
				&ppm.Config{
					Repo: &ppm.RepoConfig{
						Uri:      repoUri,
						Username: app.Config.Artifacts.Username,
						Password: app.Config.Artifacts.Password,
					},
				}, formae.DefaultInstallPrefix)
			if err != nil {
				return err
			}

			var candidate *ppm.PkgEntry

			if version == "" {
				candidate, err = manager.HasUpdate("formae", semver.MustParse(formae.Version))
				if err != nil {
					return err
				}

				if candidate == nil {
					fmt.Println("no update available")
					return nil
				}
			} else {
				parsedVersion, err := semver.NewVersion(version)
				if err != nil {
					return fmt.Errorf("error parsing version %q: ", version)
				}

				candidate, err = manager.HasEntry("formae", parsedVersion)
				if err != nil {
					return fmt.Errorf("no candidate for version %q: ", parsedVersion)
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

			fmt.Printf("installing formae version %s\n", candidate.Version.String())
			err = manager.Install(candidate)
			if err != nil {
				return err
			}

			copier, err := ppm.NewPluginCopier()
			if err != nil {
				return fmt.Errorf("failed to create plugin copier: %w", err)
			}

			err = copier.InstallExecutables(formae.DefaultInstallPrefix, candidate.Version.String())
			if err != nil {
				return fmt.Errorf("failed to install plugin executables: %w", err)
			}

			err = copier.InstallResourcePlugins(formae.DefaultInstallPrefix)
			if err != nil {
				return fmt.Errorf("failed to install resource plugins: %w", err)
			}

			fmt.Println("done.")

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.AddCommand(UpgradeListCmd())

	command.Flags().String("config", "", "Path to config file")
	command.Flags().String("version", "", "Version to install")

	return command
}

func UpgradeListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List available formae upgrades",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} upgrade list",
		},
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("config")
			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			repoUri, err := url.Parse(app.Config.Artifacts.URL)
			if err != nil {
				return fmt.Errorf("could not parse repository URL: %s", app.Config.Artifacts.URL)
			}

			manager, err := ppm.NewManager(
				&ppm.Config{
					Repo: &ppm.RepoConfig{
						Uri:      repoUri,
						Username: app.Config.Artifacts.Username,
						Password: app.Config.Artifacts.Password,
					},
				}, formae.DefaultInstallPrefix)
			if err != nil {
				return err
			}

			updates, err := manager.AvailableVersions("formae", true)
			if err != nil {
				return err
			}

			fmt.Print("Available formae versions:\n\n")
			for _, entry := range updates {
				fmt.Printf(" %12v  %s\n", entry.Version, entry.Sha256)
			}

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().String("config", "", "Path to config file")

	return command
}
