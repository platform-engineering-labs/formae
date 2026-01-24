// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package project

import (
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
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
			schemaLocation, _ := command.Flags().GetString("schema-location")

			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			return app.Projects.Init(command.Flags().Arg(0), schema, include, schemaLocation)
		},
		SilenceErrors: true,
	}

	command.Flags().String("schema", "pkl", "Schema to use for the project (pkl)")
	command.Flags().StringArray("include", []string{"aws"}, "Packages to include (aws)")
	command.Flags().String("schema-location", "remote", "Schema location: 'remote' (PKL registry) or 'local' (installed plugins)")

	return command
}
