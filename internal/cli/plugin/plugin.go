// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
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
		Short: "List all active plugins",
		RunE: func(command *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			for _, plugin := range app.Plugins.List() {
				fmt.Printf("%s %s %s\n", (*plugin).Name(), (*plugin).Version(), (*plugin).Type())
			}

			return nil
		},
		SilenceErrors: true,
	}

	return command
}
