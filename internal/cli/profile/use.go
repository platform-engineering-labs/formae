// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/spf13/cobra"
)

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			if err := s.Use(args[0]); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if isTerminal(w) {
				th := theme.New("formae")
				_, _ = fmt.Fprintln(w, renderAck(th, "switched to "+args[0]))
			} else {
				_, _ = fmt.Fprintf(w, "switched to %s\n", args[0])
			}
			return nil
		},
	}
}
