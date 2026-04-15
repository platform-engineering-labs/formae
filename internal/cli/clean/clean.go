// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package clean

import (
	"fmt"
	"log/slog"

	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/spf13/cobra"
)

func CleanCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "clean",
		Short: "Clean up old software versions",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			configFile, _ := cmd.Flags().GetString("config")

			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			orb, err := opsmgr.New(slog.Default(), app.Config.Artifacts.URL, "")
			if err != nil {
				return err
			}

			if !orb.Ready() {
				return fmt.Errorf("no managed installation root detected at: %s\n", orb.Path)
			}

			if all {
				err := orb.Clear()
				if err != nil {
					return err
				}
			} else {
				err := orb.Clean()
				if err != nil {
					return err
				}
			}

			fmt.Println("done.")

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().Bool("all", false, "Also remove update metadata")
	command.Flags().String("config", "", "Path to config file")

	return command
}
