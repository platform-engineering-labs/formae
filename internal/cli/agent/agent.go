// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package agent

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/agent"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the agent",
		Run: func(command *cobra.Command, args []string) {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
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
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			display.PrintBanner()
		},
		SilenceErrors: true,
	}
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
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			display.PrintBanner()
		},
		SilenceErrors: true,
	}
}

func AgentCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "agent",
		Short: "Agent management commands",
		Annotations: map[string]string{
			"type":     "Execution",
			"examples": "{{.Name}} {{.Command}} start  |  {{.Name}} {{.Command}} stop",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	start := startCmd()
	stop := stopCmd()

	command.AddCommand(start, stop)
	return command
}
