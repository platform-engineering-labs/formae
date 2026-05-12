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
			"examples": "{{.Name}} {{.Command}} install aws\n{{.Name}} {{.Command}} init",
		},
		SilenceErrors: true,
	}

	// list/search/info are temporarily unregistered: they depend on the
	// agent's plugin manager, which only wires up when the orbital tree
	// is reachable without sudo elevation. Until the deployment story
	// is settled (agent running as the tree owner on a pel-owned tree),
	// exposing them just produces opaque 503s. Re-register the three
	// AddCommand calls below when the agent reliably has a plugin
	// manager wired up.
	command.AddCommand(PluginInstallCmd())
	command.AddCommand(PluginUninstallCmd())
	command.AddCommand(PluginUpdateCmd())
	command.AddCommand(PluginInitCmd())

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}
