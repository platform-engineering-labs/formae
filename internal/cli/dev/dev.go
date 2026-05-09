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

	command.AddCommand(sync)
	command.AddCommand(discover)

	return command
}
