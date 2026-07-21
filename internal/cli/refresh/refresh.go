// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package refresh

import (
	"fmt"
	"log/slog"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/spf13/cobra"
)

// RefreshCmd refreshes the locally cached package index for every configured
// repository across all channels. It writes to the package store, so on a
// stock (root-owned) install it elevates via sudo.
func RefreshCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the local package index for all channels",
		Long: `Refresh the locally cached package index for every configured
repository across all channels (stable and dev).

The non-destructive commands (plugin list/search/info, update list) read
this cache without sudo, so their data can be stale until you refresh. On
a stock install the package store is root-owned, so refresh may prompt for
sudo.`,
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("config")

			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			for _, channel := range constants.AllChannels {
				var orb *mgr.Manager
				if len(app.Config.Artifacts.Repositories) > 0 {
					orb, err = opsmgr.NewFromRepositories(slog.Default(), app.Config.Artifacts.Repositories, channel, true, true)
				} else {
					orb, err = opsmgr.New(slog.Default(), app.Config.Artifacts.URL, channel, true, true)
				}
				if err != nil {
					return err
				}

				if !orb.Ready() {
					return fmt.Errorf("no managed installation root detected at: %s", orb.Path())
				}

				fmt.Printf("refreshing %s...\n", channel)
				if err := orb.Refresh(); err != nil {
					return err
				}
			}

			fmt.Println("done.")
			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)

	return command
}
