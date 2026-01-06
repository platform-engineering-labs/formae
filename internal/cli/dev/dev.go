// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package dev

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
)

func syncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Force synchronization",
		RunE: func(command *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			if err := app.ForceSync(); err != nil {
				slog.Error(fmt.Sprintf("Error running the synchronization command: %v\n", err))
			}

			return nil
		},
		SilenceErrors: true,
	}
}

func discoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "discover",
		Short: "Force discovery",
		RunE: func(command *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			if err := app.ForceDiscover(); err != nil {
				slog.Error(fmt.Sprintf("Error forcing discovery: %v\n", err))
				return err
			}
			return nil
		},
		SilenceErrors: true,
	}
}

func awsSupportedTypes() *cobra.Command {
	return &cobra.Command{
		Use:   "aws-support [type]",
		Short: "List AWS plugin supported resource types or get schema for specific type",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			app, err := cmd.AppFromContext(command.Context(), "", "", command)
			if err != nil {
				return err
			}

			// Get the AWS resource plugin
			awsPlugin, err := app.PluginManager.ResourcePlugin("aws")
			if err != nil {
				return fmt.Errorf("failed to get AWS plugin: %w", err)
			}

			// If a specific type is provided, get its schema
			if len(args) == 1 {
				resourceType := args[0]
				schema, err := (*awsPlugin).SchemaForResourceType(resourceType)
				if err != nil {
					return fmt.Errorf("failed to get schema for resource type %s: %w", resourceType, err)
				}

				fmt.Printf("Schema for AWS resource type '%s':\n\n", resourceType)
				fmt.Printf("Identifier: %s\n", schema.Identifier)
				fmt.Printf("Tags: %s\n", schema.Tags)
				fmt.Printf("Nonprovisionable: %t\n", schema.Nonprovisionable)
				fmt.Printf("CreateOnly: %v\n", schema.CreateOnly())
				fmt.Printf("Fields: %v\n", schema.Fields)

				return nil
			}

			// Otherwise, list all supported resources
			resources := (*awsPlugin).SupportedResources()
			fmt.Printf("AWS Plugin supports %d resource types:\n\n", len(resources))

			for i, resource := range resources {
				fmt.Printf("%d. %s\n", i+1, resource.Type)
			}

			return nil
		},
		SilenceErrors: true,
	}
}

func DevCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "dev",
		Short: "home for the experimental commands during development",
		Annotations: map[string]string{
			"type": "DEVELOPMENT ONLY",
		},
		SilenceErrors: true,
	}

	command.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	sync := syncCmd()
	discover := discoverCmd()
	awsSupport := awsSupportedTypes()

	command.AddCommand(sync)
	command.AddCommand(discover)
	command.AddCommand(awsSupport)

	return command
}
