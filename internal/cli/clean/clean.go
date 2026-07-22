// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package clean

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/spf13/cobra"
)

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

func CleanCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "clean",
		Short: "Clean up old software versions",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			configFile, _ := cmd.Flags().GetString("config")

			a, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}
			a.PrintBanner()

			orb, err := opsmgr.New(slog.Default(), a.Config.Artifacts.URL, "", true, true)
			if err != nil {
				return err
			}

			if !orb.Ready() {
				return fmt.Errorf("no managed installation root detected at: %s", orb.Path())
			}

			return runClean(os.Stdout, themeFor(a), all, orb.Clean, orb.Clear)
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().Bool("all", false, "Also remove update metadata")
	clicmd.AddConfigFlags(command)

	return command
}

// runClean is the testable core of the clean flow. It renders a single animated
// step (spinner on a TTY, result-line only when piped) for the cleanup and
// resolves it to ✓ on success or ✗ on failure. With all=true it clears update
// metadata too (orbital's Clear); otherwise it prunes superseded versions
// (orbital's Clean).
func runClean(w io.Writer, th *theme.Theme, all bool, cleanFn, clearFn func() error) error {
	label := "removing old versions…"
	doneLabel := "removed old versions"
	op := cleanFn
	if all {
		label = "removing old versions and update metadata…"
		doneLabel = "removed old versions and update metadata"
		op = clearFn
	}

	step := components.StartStep(w, th, label)
	if err := op(); err != nil {
		step.Fail("failed to clean up old versions")
		return err
	}
	step.Done(doneLabel)
	return nil
}
