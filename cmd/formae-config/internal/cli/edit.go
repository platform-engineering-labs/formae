// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [<name>]",
		Short: "Open a profile in $EDITOR (or the active profile if no name given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			var path string
			if len(args) == 1 {
				if err := profiles.ValidateName(args[0]); err != nil {
					return err
				}
				path = s.ProfilePath(args[0])
				// Confirm the profile exists; fail with ErrNotFound otherwise.
				if _, err := os.Stat(path); err != nil {
					if os.IsNotExist(err) {
						return fmt.Errorf("%w: %s", profiles.ErrNotFound, args[0])
					}
					return err
				}
			} else {
				active, err := s.Active()
				if err != nil {
					return err
				}
				path = s.ProfilePath(active)
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, path)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			return c.Run()
		},
	}
}
