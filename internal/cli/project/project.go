// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package project

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/prompter"
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

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			return app.Projects.Init(command.Flags().Arg(0), schema, include)
		},
		SilenceErrors: true,
	}

	command.Flags().String("schema", "pkl", "Schema to use for the project (pkl)")
	command.Flags().StringArray("include", []string{}, "Packages to include (use @local suffix for local plugins, e.g. myplugin@local)")
	command.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")

	return command
}
