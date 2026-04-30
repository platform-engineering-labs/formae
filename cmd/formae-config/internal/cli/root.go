// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package cli wires the fcfg subcommands.
package cli

import (
	"errors"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
	"github.com/spf13/cobra"
)

// NewRootCmd returns the fcfg root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fcfg",
		Short:         "Manage named profiles for ~/.config/formae/",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newInitCmd(),
		newCurrentCmd(),
		newListCmd(),
	)
	return root
}

// ExitCodeFor maps an error returned from a subcommand to the documented
// fcfg exit code:
//
//	0 success
//	1 user error (invalid name, missing profile, overwrite without --force, etc.)
//	2 filesystem / permission / unexpected error
//	3 not initialized
func ExitCodeFor(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, profiles.ErrNotInitialized):
		return 3
	case errors.Is(err, errAborted),
		errors.Is(err, profiles.ErrInvalidName),
		errors.Is(err, profiles.ErrNotFound),
		errors.Is(err, profiles.ErrAlreadyExists),
		errors.Is(err, profiles.ErrIsActive),
		errors.Is(err, profiles.ErrNoConfigFile),
		errors.Is(err, profiles.ErrAlreadyInitialized):
		return 1
	default:
		return 2
	}
}
