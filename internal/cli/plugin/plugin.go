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
			"examples": "{{.Name}} {{.Command}} list\n{{.Name}} {{.Command}} search aws\n{{.Name}} {{.Command}} install aws\n{{.Name}} {{.Command}} init",
		},
		SilenceErrors: true,
	}

	command.AddCommand(PluginListCmd())
	command.AddCommand(PluginSearchCmd())
	command.AddCommand(PluginInfoCmd())
	command.AddCommand(PluginInstallCmd())
	command.AddCommand(PluginUninstallCmd())
	command.AddCommand(PluginUpgradeCmd())
	command.AddCommand(PluginInitCmd())

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}

func PluginListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		RunE: func(command *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			client, err := app.NewClient()
			if err != nil {
				return err
			}

			resp, err := client.ListPlugins("installed", "", "", "", "")
			if err != nil {
				return err
			}

			fmt.Print(renderPluginList(resp.Plugins))
			return nil
		},
		SilenceErrors: true,
	}
	return command
}

func PluginSearchCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "search [<query>]",
		Short: "Search available plugins",
		RunE: func(cc *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			category, _ := cc.Flags().GetString("category")
			typ, _ := cc.Flags().GetString("type")
			channel, _ := cc.Flags().GetString("channel")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			client, err := app.NewClient()
			if err != nil {
				return err
			}

			resp, err := client.ListPlugins("available", query, category, typ, channel)
			if err != nil {
				return err
			}

			fmt.Print(renderPluginSearch(resp.Plugins))
			return nil
		},
		SilenceErrors: true,
	}
	c.Flags().String("category", "", "Filter by category")
	c.Flags().String("type", "", "Filter by plugin type (resource|auth)")
	c.Flags().String("channel", "", "Search a different channel")
	return c
}

func PluginInfoCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "info <name>",
		Short: "Show detailed plugin information",
		Args:  cobra.ExactArgs(1),
		RunE: func(cc *cobra.Command, args []string) error {
			channel, _ := cc.Flags().GetString("channel")
			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			client, err := app.NewClient()
			if err != nil {
				return err
			}

			resp, err := client.GetPlugin(args[0], channel)
			if err != nil {
				return err
			}
			if resp == nil {
				return fmt.Errorf("plugin '%s' not found", args[0])
			}

			fmt.Print(renderPluginInfo(&resp.Plugin))
			return nil
		},
		SilenceErrors: true,
	}
	c.Flags().String("channel", "", "Query a different channel")
	return c
}
