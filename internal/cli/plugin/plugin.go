// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

func PluginCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "plugin",
		Short: "Execute commands on plugins",
		Annotations: map[string]string{
			"type":     "Plugins",
			"examples": "{{.Name}} {{.Command}} list\n{{.Name}} {{.Command}} init",
		},
		SilenceErrors: true,
	}

	command.AddCommand(PluginListCmd())
	command.AddCommand(PluginInitCmd())

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}

func PluginListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List resource plugins registered with the agent",
		RunE: func(command *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			stats, _, err := app.Stats()
			if err != nil {
				return err
			}

			if len(stats.Plugins) == 0 {
				fmt.Println("No resource plugins registered with the agent.")
				return nil
			}

			fmt.Println(display.LightBlue("Resource Plugins"))
			for _, p := range stats.Plugins {
				fmt.Printf("  %s %s\n",
					display.Green(p.Namespace),
					display.Grey(p.Version))
			}

			return nil
		},
		SilenceErrors: true,
	}

	return command
}
