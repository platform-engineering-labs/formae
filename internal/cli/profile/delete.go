// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile (cannot be the active one)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			if err := s.Delete(args[0]); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if isTerminal(w) {
				th := theme.New("")
				_, _ = fmt.Fprintln(w, renderAck(th, "deleted "+args[0]))
			} else {
				_, _ = fmt.Fprintf(w, "deleted %s\n", args[0])
			}
			return nil
		},
	}
}
