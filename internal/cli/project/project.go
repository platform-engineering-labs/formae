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
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
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

			th := theme.New("")

			// Interactive plugin multi-select (D10):
			// Only when no --include is specified and --yes is not set.
			if len(include) == 0 && !yes {
				if !isInteractive() {
					// Non-TTY without --include or --yes: return a non-zero error.
					return fmt.Errorf("interactive input requires a TTY — pass --include or --yes")
				}

				// Resolve the app for the agent call. We may not have a
				// config flag yet (project init is typically run before the
				// agent exists), so we attempt a best-effort connection and
				// fall back to the local scan on failure.
				configFile, _ := command.Flags().GetString("config")
				var appCtx *app.App
				if appCtx2, err := cmd.AppFromContext(command.Context(), configFile, "", command); err == nil {
					appCtx = appCtx2
				}

				selectedIncludes, err := runPluginSelect(th, appCtx, pluginDir, schema)
				if err != nil {
					return err
				}
				include = selectedIncludes
			}

			// Emit styled summary: "Project initialized at <path>"
			// followed by a FieldList with Schema and Plugins.
			path := command.Flags().Arg(0)
			if path == "" {
				path = "."
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
			if err := projects.Init(path, schema, include, util.ExpandHomePath(pluginDir), installedVersions); err != nil {
				return err
			}

			// Styled summary output.
			pluginsVal := strings.Join(include, ", ")
			if pluginsVal == "" {
				pluginsVal = "(none)"
			}
			fmt.Printf("\nProject initialized at %s\n\n", path)
			fmt.Println(components.FieldList(th, [][2]string{
				{"Schema", schema},
				{"Plugins", pluginsVal},
			}))
			fmt.Println()

			return nil
		},
		SilenceErrors: true,
	}

	command.Flags().String("schema", "pkl", "Schema to use for the project (pkl)")
	command.Flags().StringArray("include", []string{}, "Packages to include (use @local suffix for local plugins, e.g. myplugin@local)")
	command.Flags().BoolP("yes", "y", false, "Skip the plugin prompt and proceed without plugins")
	command.Flags().String("plugin-dir", "~/.pel/formae/plugins", "Directory to scan for @local plugin schemas")
	cmd.AddConfigFlags(command)

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
