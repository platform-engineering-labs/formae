// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package agent

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/agent"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/nag"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/util"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// themeBanner resolves the configured CLI theme from the command's
// --config/--profile flags and applies it to the banner, so the printed logo
// wordmark follows the user's theme. The agent subcommands print their banner
// from PersistentPreRun (before the app/config is loaded in Run), so they can't
// use (*app.App).PrintBanner; they resolve the theme straight from config the
// same way the root help screen does. Package var so tests can observe the call.
var themeBanner = func(c *cobra.Command) {
	banner.SetTheme(cmd.ResolveConfiguredTheme(c))
}

func startCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "start",
		Short: "Start the agent",
		Run: func(command *cobra.Command, args []string) {
			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				slog.Error(err.Error())
				return
			}

			// Make ~ nice in flag default view because go does not expand them
			if app.Config.Agent.Datastore.DatastoreType == pkgmodel.SqliteDatastore {
				app.Config.Agent.Datastore.Sqlite.FilePath = util.ExpandHomePath(app.Config.Agent.Datastore.Sqlite.FilePath)
			}

			app.Config.Agent.Logging.FilePath = util.ExpandHomePath(app.Config.Agent.Logging.FilePath)

			// Ensure agent ID
			err = config.Config.EnsureAgentID()
			if err != nil {
				slog.Error("Error starting agent: %v\n", "error", err)
				return
			}

			agentID, err := config.Config.AgentID()
			if err != nil {
				slog.Error("Error retrieving agent ID: %v\n", "error", err)
				return
			}

			a := agent.New(app.Config, agentID)

			if err := a.Start(); err != nil {
				slog.Error("Error starting agent: %v\n", "error", err)
				return
			}

			a.Wait()
		},
		PersistentPreRun: func(command *cobra.Command, args []string) {
			themeBanner(command)
			banner.PrintBanner()
		},
		SilenceErrors: true,
	}

	cmd.AddConfigFlags(command)

	return command
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the agent",
		Run: func(cmd *cobra.Command, args []string) {
			a := agent.Agent{}
			if err := a.Stop(); err != nil {
				slog.Error("Error stopping agent: %v\n", "error", err)
				return
			}
		},
		PersistentPreRun: func(command *cobra.Command, args []string) {
			themeBanner(command)
			banner.PrintBanner()
		},
		SilenceErrors: true,
	}
}

// statusCmd is `agent status`, the successor of the deprecated `formae
// status agent`. It reuses the stats panel rendering and watch loop exported
// from internal/cli/status rather than re-implementing them.
func statusCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Receive the agent status",
		RunE: func(command *cobra.Command, args []string) error {
			_consumer, _ := command.Flags().GetString("output-consumer")
			consumer := printer.Consumer(_consumer)
			schema, _ := command.Flags().GetString("output-schema")
			watch, _ := command.Flags().GetBool("watch")
			switch consumer {
			case printer.ConsumerMachine:
				if schema != "json" && schema != "yaml" {
					return fmt.Errorf("unsupported schema: %s", schema)
				}
			}

			configFile, _ := command.Flags().GetString("config")
			app, err := cmd.AppFromContext(command.Context(), configFile, "", command)
			if err != nil {
				return err
			}

			if consumer == printer.ConsumerHuman {
				app.PrintBanner()
			}

			stats, nags, err := app.Stats()
			if err != nil {
				return err
			}

			// if machine consumer, create machine printer, print and return nil
			if consumer == printer.ConsumerMachine {
				p := printer.NewMachineReadablePrinter[apimodel.Stats](os.Stdout, schema)
				err = p.Print(stats)
				if err != nil {
					return err
				}
				return nil
			}

			fmt.Println(status.RenderAgentStats(status.ThemeFor(app), *stats, status.TermWidth(os.Stdout)))

			if consumer != printer.ConsumerMachine && !watch {
				nag.MaybePrintNags(status.ThemeFor(app), nags)
			}

			if watch && consumer == printer.ConsumerHuman { // machine consumer can't watch
				nag.MaybePrintNags(status.ThemeFor(app), nags)
				return status.WatchStats(app)
			}

			return nil
		},
		Annotations:   map[string]string{},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	command.Flags().String("output-consumer", string(printer.ConsumerHuman), "Consumer of the command result (human | machine)")
	command.Flags().String("output-schema", "json", "The schema to use for the machine output (json | yaml)")
	command.Flags().Bool("watch", false, "Continuously refresh and print the status until completion")
	cmd.AddConfigFlags(command)

	return command
}

func AgentCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "agent",
		Short: "Agent management commands",
		Annotations: map[string]string{
			"type":     "Execution",
			"examples": "{{.Name}} {{.Command}} start  |  {{.Name}} {{.Command}} stop  |  {{.Name}} {{.Command}} status",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	start := startCmd()
	stop := stopCmd()
	agentStatus := statusCmd()

	command.AddCommand(start, stop, agentStatus)
	return command
}
