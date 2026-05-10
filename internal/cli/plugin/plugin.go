// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
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

			// Two best-effort sources of truth for what's installed on
			// this host: the agent (free, no sudo) and a local read of
			// the orbital tree. On a single-host deployment they're
			// reading the same store and agree by definition; on a CLI
			// box the agent may be unreachable; on a split deployment
			// the auth-plugin set may diverge across hosts. The renderer
			// only annotates rows when the two arms disagree — matching
			// rows render plain so the typical single-host case isn't
			// noisy.
			agentPlugins, agentNote := fetchAgentPlugins(app)
			localPlugins, localNote := fetchLocalPlugins(app)

			agentReached := agentNote == ""
			localScanned := localNote == ""

			if !agentReached && !localScanned {
				return fmt.Errorf("couldn't list installed plugins:\n  - agent: %s\n  - local: %s", agentNote, localNote)
			}

			fmt.Print(renderPluginList(agentPlugins, localPlugins, agentReached, localScanned))
			if agentReached && !localScanned {
				fmt.Printf("\n  %s Local view skipped: %s\n", display.Grey("ℹ"), localNote)
			}
			if !agentReached && localScanned {
				fmt.Printf("\n  %s Agent unreachable; showing local view only: %s\n", display.Grey("ℹ"), agentNote)
			}
			return nil
		},
		SilenceErrors: true,
	}
	return command
}

// fetchAgentPlugins returns (plugins, "") on success, or (nil, reason)
// when the agent is unreachable or the API call failed. Agent-side
// listing is read-only and works without sudo, so it's the cheap
// path; the local arm is a fallback for hosts where no agent is
// reachable.
func fetchAgentPlugins(app *app.App) ([]apimodel.Plugin, string) {
	client, cerr := app.NewClient()
	if cerr != nil {
		return nil, cerr.Error()
	}
	resp, lerr := client.ListPlugins("installed", "", "", "", "")
	if lerr != nil {
		return nil, lerr.Error()
	}
	return resp.Plugins, ""
}

// fetchLocalPlugins reads the local orbital tree directly. Skipped
// when the tree is root-owned and we're not — orbital would re-exec
// under sudo just to read, and `list` shouldn't force a privilege
// prompt. Returns ("re-run under sudo …") in that case so the caller
// can surface a clear hint.
func fetchLocalPlugins(app *app.App) ([]apimodel.Plugin, string) {
	if opsmgr.TreeRequiresElevation() {
		return nil, "tree is root-owned, re-run under sudo to read /opt/pel"
	}
	mgr, merr := NewCLIPluginManager(slog.Default(), app.Config.Artifacts.Repositories, "")
	if merr != nil {
		return nil, merr.Error()
	}
	if mgr == nil {
		return nil, "no artifact repositories configured"
	}
	plugins, lerr := mgr.ListInstalled()
	if lerr != nil {
		return nil, lerr.Error()
	}
	return plugins, ""
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
