// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"errors"
	"os"
	"os/exec"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <a> [<b>]",
		Short: "Run `diff -u` between two profiles (or <a> vs the active profile)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			a := args[0]
			if err := profiles.ValidateName(a); err != nil {
				return err
			}
			pathA := s.ProfilePath(a)
			var pathB string
			if len(args) == 2 {
				if err := profiles.ValidateName(args[1]); err != nil {
					return err
				}
				pathB = s.ProfilePath(args[1])
			} else {
				active, err := s.Active()
				if err != nil {
					return err
				}
				pathB = s.ProfilePath(active)
			}
			c := exec.Command("diff", "-u", pathA, pathB)
			c.Stdout, c.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			err = c.Run()
			// Pass through diff(1)'s exit codes verbatim:
			//   0 = identical, 1 = differ, >1 = error.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.ExitCode())
			}
			return err
		},
	}
}
