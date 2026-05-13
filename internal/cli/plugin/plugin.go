// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

func PluginCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "plugin",
		Short: "Execute commands on plugins",
		Annotations: map[string]string{
			"type":     "Plugins",
			"examples": "{{.Name}} {{.Command}} list\n{{.Name}} {{.Command}} search aws\n{{.Name}} {{.Command}} install aws\n{{.Name}} {{.Command}} init",
		},
		SilenceErrors: true,
	}

	command.AddCommand(PluginListCmd())
	command.AddCommand(PluginSearchCmd())
	command.AddCommand(PluginInfoCmd())
	command.AddCommand(PluginInstallCmd())
	command.AddCommand(PluginUninstallCmd())
	command.AddCommand(PluginUpdateCmd())
	command.AddCommand(PluginInitCmd())

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}
