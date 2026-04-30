// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package cli wires the fcfg subcommands.
package cli

import "github.com/spf13/cobra"

// NewRootCmd returns the fcfg root command. Subcommands are added in subsequent
// tasks via init() blocks in their respective files.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fcfg",
		Short:         "Manage named profiles for ~/.config/formae/",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return root
}

// ExitCodeFor maps an error returned from a subcommand to the documented
// exit code. Subsequent tasks expand this as new error sentinels are added.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
