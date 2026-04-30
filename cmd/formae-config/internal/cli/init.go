// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/platform-engineering-labs/formae/cmd/formae-config/internal/profiles"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var name string
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Convert formae.conf.pkl into a profile and replace it with a symlink",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(),
					"Convert %s into profile %q? [Y/n] ", s.ConfigPath(), name)
				r := bufio.NewReader(os.Stdin)
				line, _ := r.ReadString('\n')
				line = strings.ToLower(strings.TrimSpace(line))
				if line != "" && line != "y" && line != "yes" {
					return errAborted
				}
			}
			if err := s.Init(name); err != nil {
				if errors.Is(err, profiles.ErrAlreadyInitialized) {
					fmt.Fprintln(cmd.OutOrStdout(), "already initialized")
					return nil
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"initialized: %s -> profiles/%s.pkl\n", s.ConfigPath(), name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "default", "profile name")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
