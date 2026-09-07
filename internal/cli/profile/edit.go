// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/spf13/cobra"
)

// editorCommand splits the EDITOR env-var into a command name and its
// arguments, then appends path as the final argument. If editor is empty or
// all-whitespace it falls back to "vi".
func editorCommand(editor, path string) (name string, args []string) {
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		fields = []string{"vi"}
	}
	return fields[0], append(fields[1:], path)
}

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
				if err := store.ValidateName(args[0]); err != nil {
					return err
				}
				path = s.ProfilePath(args[0])
				// Confirm the profile exists; fail with ErrNotFound otherwise.
				if _, err := os.Stat(path); err != nil {
					if os.IsNotExist(err) {
						return fmt.Errorf("%w: %s", store.ErrNotFound, args[0])
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
			name, args := editorCommand(os.Getenv("EDITOR"), path)
			c := exec.Command(name, args...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			return c.Run()
		},
	}
}
