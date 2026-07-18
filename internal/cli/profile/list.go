// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package profile

import (
	"errors"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/spf13/cobra"
)

// listOutput is the machine-readable shape of `profile list`.
type listOutput struct {
	Active   *string  `json:"active" yaml:"active"`
	Profiles []string `json:"profiles" yaml:"profiles"`
}

func newListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List all profiles, marking the active one with *",
		Args:  cobra.NoArgs,
		RunE: func(cc *cobra.Command, args []string) error {
			consumer, schema, err := resolveOutput(cc)
			if err != nil {
				return err
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			names, err := s.List()
			if err != nil {
				return err
			}
			active, err := s.Active()
			if err != nil && !errors.Is(err, store.ErrNotInitialized) {
				return err
			}

			if consumer == printer.ConsumerMachine {
				out := listOutput{Profiles: names}
				if active != "" {
					out.Active = &active
				}
				p := printer.NewMachineReadablePrinter[listOutput](cc.OutOrStdout(), schema)
				return p.Print(&out)
			}

			w := cc.OutOrStdout()
			if isTerminal(w) {
				th := theme.New("formae")
				_, _ = fmt.Fprintln(w, renderProfileList(th, names, active))
			} else {
				for _, n := range names {
					marker := "  "
					if n == active {
						marker = "* "
					}
					_, _ = fmt.Fprintf(w, "%s%s\n", marker, n)
				}
			}
			return nil
		},
	}
	addOutputFlags(c)
	return c
}
