// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new profile from the starter template (does not switch)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			if err := s.Create(args[0], force); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if isTerminal(w) {
				th := theme.New("")
				_, _ = fmt.Fprintln(w, renderAck(th, "created "+args[0]))
			} else {
				_, _ = fmt.Fprintf(w, "created %s\n", args[0])
			}
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing profile")
	return c
}
