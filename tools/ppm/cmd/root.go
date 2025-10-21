// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"ppm"

	"github.com/spf13/cobra"
)

func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ppm",
		Short: "PEL Package Manager (simple)",
		Long:  "PEL Package Manager (simple)",
	}

	cmd.PersistentFlags().String("uri", ppm.BinaryRepository, "URI of the repository")

	cmd.AddCommand(PkgCmd())
	cmd.AddCommand(RepoCmd())

	return cmd
}
