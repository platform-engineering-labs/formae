// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

type ListOptions struct {
	OutputConsumer printer.Consumer
	OutputSchema   string
}

func PluginListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins on this host",
		Long:  `List the plugins installed on this host with their version.`,
		RunE: func(cc *cobra.Command, args []string) error {
			opts := &ListOptions{}
			consumer, _ := cc.Flags().GetString("output-consumer")
			opts.OutputConsumer = printer.Consumer(consumer)
			opts.OutputSchema, _ = cc.Flags().GetString("output-schema")

			app, err := cmd.AppFromContext(cc.Context(), "", "", cc)
			if err != nil {
				return err
			}

			return runList(app, opts)
		},
		SilenceErrors: true,
	}
	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)
	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the result output (json | yaml)")
	return command
}

func runList(app *app.App, opts *ListOptions) error {
	if err := validateListOptions(opts); err != nil {
		return err
	}
	if opts.OutputConsumer == printer.ConsumerHuman {
		return runListForHumans(app, opts)
	}
	return runListForMachines(app, opts)
}

func validateListOptions(opts *ListOptions) error {
	if opts.OutputConsumer != printer.ConsumerHuman && opts.OutputConsumer != printer.ConsumerMachine {
		return cmd.FlagErrorf("output-consumer must be 'human' or 'machine'")
	}
	if opts.OutputConsumer == printer.ConsumerMachine {
		if opts.OutputSchema != "json" && opts.OutputSchema != "yaml" {
			return cmd.FlagErrorf("output-schema must be either 'json' or 'yaml' for machine consumer")
		}
	}
	return nil
}

func runListForHumans(app *app.App, opts *ListOptions) error {
	// Two best-effort sources of truth for what's installed on this
	// host: the agent (free, no sudo) and a local read of the orbital
	// tree. On a single-host deployment they're reading the same store
	// and agree by definition; on a CLI box the agent may be
	// unreachable; on a split deployment the auth-plugin set may
	// diverge across hosts. The renderer only annotates rows when the
	// two arms disagree — matching rows render plain so the typical
	// single-host case isn't noisy.
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
}

func runListForMachines(app *app.App, opts *ListOptions) error {
	agentPlugins, agentNote := fetchAgentPlugins(app)
	localPlugins, localNote := fetchLocalPlugins(app)

	if agentNote != "" && localNote != "" {
		return fmt.Errorf("couldn't list installed plugins:\n  - agent: %s\n  - local: %s", agentNote, localNote)
	}

	merged := mergePluginsByName(agentPlugins, localPlugins)
	resp := &apimodel.ListPluginsResponse{Plugins: merged}
	p := printer.NewMachineReadablePrinter[apimodel.ListPluginsResponse](os.Stdout, opts.OutputSchema)
	return p.Print(resp)
}

// mergePluginsByName returns a deduplicated list (by lowercase name)
// drawn from both arms. The agent arm wins on duplicate names because
// it is the authoritative installed view on the agent host; the local
// arm fills in plugins only present on the CLI host.
func mergePluginsByName(agent, local []apimodel.Plugin) []apimodel.Plugin {
	seen := make(map[string]bool, len(agent))
	out := make([]apimodel.Plugin, 0, len(agent)+len(local))
	for _, p := range agent {
		seen[strings.ToLower(p.Name)] = true
		out = append(out, p)
	}
	for _, p := range local {
		if seen[strings.ToLower(p.Name)] {
			continue
		}
		out = append(out, p)
	}
	return out
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
