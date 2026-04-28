// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package project

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
	"github.com/platform-engineering-labs/formae/internal/util"
)

func ProjectCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "project",
		Short: "Work with formae projects",
		Annotations: map[string]string{
			"type":     "Projects",
			"examples": "{{.Name}} {{.Command}} init",
		},
		SilenceErrors: true,
	}

	command.AddCommand(ProjectInitCmd())

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	return command
}

func ProjectInitCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize a project",
		RunE: func(command *cobra.Command, args []string) error {
			schema, _ := command.Flags().GetString("schema")
			include, _ := command.Flags().GetStringArray("include")
			yes, _ := command.Flags().GetBool("yes")
			pluginDir, _ := command.Flags().GetString("plugin-dir")

			// Confirm with user if no plugins specified
			if len(include) == 0 && !yes {
				fmt.Println(display.Gold("No plugins specified.") + " The project will not include any cloud provider schemas.")
				fmt.Println(display.Grey("  To add plugins, use: --include aws --include gcp"))
				fmt.Println()

				p := prompter.NewBasicPrompter()
				if !p.Confirm("Continue without plugins?", false) {
					fmt.Print(display.Red("\nProject initialization aborted\n"))
					return nil
				}
				fmt.Println()
			}

			// Non-@local includes need plugin versions from the agent —
			// after the multi-source plugin-discovery refactor, plugins
			// live on the agent box, not the CLI box. Skip the agent
			// query if every include is @local (no version needed).
			var installedVersions map[string]string
			if needsAgent(include) {
				configFile, _ := command.Flags().GetString("config")
				appCtx, err := cmd.AppFromContext(command.Context(), configFile, "", command)
				if err != nil {
					return err
				}
				installedVersions, err = appCtx.InstalledResourcePluginVersions()
				if err != nil {
					return fmt.Errorf("listing installed plugins: %w", err)
				}
			}

			projects := &app.Projects{}
			return projects.Init(command.Flags().Arg(0), schema, include, util.ExpandHomePath(pluginDir), installedVersions)
		},
		SilenceErrors: true,
	}

	command.Flags().String("schema", "pkl", "Schema to use for the project (pkl)")
	command.Flags().StringArray("include", []string{}, "Packages to include (use @local suffix for local plugins, e.g. myplugin@local)")
	command.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")
	command.Flags().String("plugin-dir", "~/.pel/formae/plugins", "Directory to scan for @local plugin schemas")
	command.Flags().String("config", "", "Path to config file")

	return command
}

// needsAgent reports whether resolving the given include list requires asking
// the agent for installed plugin versions. Includes ending in @local resolve
// against pluginsDir on disk and don't need an agent.
func needsAgent(include []string) bool {
	for _, inc := range include {
		if !strings.HasSuffix(strings.ToLower(inc), "@local") {
			return true
		}
	}
	return false
}
