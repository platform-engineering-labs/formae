// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package update

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/platform-engineering-labs/formae/internal/agent"
	"github.com/platform-engineering-labs/formae/internal/cli/app"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/spf13/cobra"
)

// Package seams — replaced in tests to avoid TTY / network / process calls.
var (
	isInteractive = tui.IsInteractive
	runConfirm    = components.RunConfirm
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

// updateSeams bundles injectable dependencies for the interactive update flow.
// Production code uses the package-level vars; tests supply stubs.
type updateSeams struct {
	isInteractiveFn func() bool
	runConfirmFn    func(*theme.Theme, string, string) (bool, error)
	stopAgentFn     func() error
	installFn       func(pkg string) error
}

// defaultSeams returns the production wiring for updateSeams given an orbital manager
// and a resolved candidate package.
func defaultSeams(orb *mgr.Manager, candidate *records.Package) updateSeams {
	return updateSeams{
		isInteractiveFn: isInteractive,
		runConfirmFn:    runConfirm,
		stopAgentFn: func() error {
			ag := agent.Agent{}
			err := ag.Stop()
			if err != nil && strings.Contains(err.Error(), "agent is not running") {
				return nil
			}
			return err
		},
		installFn: func(pkg string) error {
			return orb.Install(pkg)
		},
	}
}

// ackLine emits a single acknowledgment line. On a TTY it renders with
// lipgloss styling; when piped it writes plain text.
func ackLine(w io.Writer, tty bool, th *theme.Theme, m components.AckMarker, text string) {
	if tty {
		_, _ = fmt.Fprintln(w, components.AckLine(th, m, text))
		return
	}
	_, _ = fmt.Fprintln(w, components.AckLinePlain(m, text))
}

// runInitConfirmDecision asks the user whether to initialize the managed root
// when none is detected. Returns (true, nil) to proceed, (false, nil) to
// abort, or a non-nil error on D8 violation.
//
// D8 policy: non-TTY without --yes → error.
func runInitConfirmDecision(w io.Writer, th *theme.Theme, s updateSeams, path string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if !s.isInteractiveFn() {
		return false, fmt.Errorf("interactive input requires a TTY — pass --yes to proceed non-interactively")
	}
	title := fmt.Sprintf("No managed installation root at %s. Initialize?", path)
	ok, err := s.runConfirmFn(th, title, "")
	if err != nil {
		return false, err
	}
	return ok, nil
}

// runUpdateFlow is the testable core of the interactive update flow.
//
// D8 policy: non-TTY without --yes → error; non-TTY with --yes → proceed.
// Consequence sentence is printed BEFORE the confirm prompt (D-order).
func runUpdateFlow(w io.Writer, th *theme.Theme, s updateSeams, version, candidateID string, yes bool) error {
	// tty is used for output styling only (ackLine, StepLine rendering).
	tty := tui.IsTerminal(w)

	if !yes {
		if !s.isInteractiveFn() {
			return fmt.Errorf("interactive input requires a TTY — pass --yes to proceed non-interactively")
		}
		// Print the consequence sentence BEFORE the confirm.
		_, _ = fmt.Fprintln(w, "Updating stops the local formae agent while the new version installs.")
		ok, err := s.runConfirmFn(th, fmt.Sprintf("Update to %s?", version), "")
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}

	// Step 1: stop agent.
	step := components.StartStep(w, th, "stopping formae agent…")
	if err := s.stopAgentFn(); err != nil {
		step.Fail("failed to stop formae agent")
		return err
	}
	step.Done("stopped formae agent")

	// Step 2: install (single blocking call — one step as prescribed by D9).
	step = components.StartStep(w, th, fmt.Sprintf("installing formae %s…", version))
	if err := s.installFn(candidateID); err != nil {
		step.Fail(fmt.Sprintf("failed to install formae %s", version))
		return err
	}
	step.Done(fmt.Sprintf("installed formae %s", version))

	// Restart hint.
	ackLine(w, tty, th, components.AckWarn, "restart the agent when ready: formae agent start")

	// Final done line with release notes URL.
	releaseURL := fmt.Sprintf("https://github.com/platform-engineering-labs/formae/releases/tag/%s", version)
	_, _ = fmt.Fprintf(w, "\nDone. Release notes: %s\n", releaseURL)

	return nil
}

func UpdateCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "update [version]",
		Short: "Manage formae binary updates",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			channel, _ := cmd.Flags().GetString("channel")
			configFile, _ := cmd.Flags().GetString("config")
			version := cmd.Flags().Arg(0)
			yes, _ := cmd.Flags().GetBool("yes")

			a, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}
			a.PrintBanner()

			var orb *mgr.Manager
			if len(a.Config.Artifacts.Repositories) > 0 {
				orb, err = opsmgr.NewFromRepositoriesFiltered(slog.Default(), a.Config.Artifacts.Repositories, channel, true, true, pkgmodel.RepositoryTypeBinary)
			} else {
				orb, err = opsmgr.New(slog.Default(), a.Config.Artifacts.URL, channel, true, true)
			}
			if err != nil {
				return err
			}

			th := themeFor(a)

			// Init root if needed — D8 gated confirm.
			if !orb.Ready() {
				seams := updateSeams{
					isInteractiveFn: isInteractive,
					runConfirmFn:    runConfirm,
					// stopAgentFn and installFn are not used in the init path.
				}
				proceed, err := runInitConfirmDecision(os.Stdout, th, seams, orb.Path(), yes)
				if err != nil {
					return err
				}
				if !proceed {
					return nil
				}

				_, err = orb.Initialize()
				if err != nil {
					return err
				}
			}

			err = orb.Refresh()
			if err != nil {
				return err
			}

			available, err := orb.AvailableFor("formae")
			if err != nil {
				return err
			}

			var candidate *records.Package
			var hasUpdate bool
			var hasVersion bool

			if version == "" {
				if hasUpdate, candidate = available.HasUpdate(); !hasUpdate {
					fmt.Println("no updates available")
					return nil
				}
			} else {
				v := &ops.Version{}
				err := v.Parse(version)
				if err != nil {
					return fmt.Errorf("could not parse version: %w", err)
				}

				if hasVersion, candidate = available.HasVersion(v); !hasVersion {
					return fmt.Errorf("could not find formae version: %s", version)
				}
			}

			seams := defaultSeams(orb, candidate)
			return runUpdateFlow(os.Stdout, th, seams, candidate.Version.Short(), candidate.Id().String(), yes)
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.AddCommand(UpdateListCmd())

	command.Flags().String("channel", "", "Override update channel")
	command.Flags().Bool("yes", false, "Proceed without interactive confirmations")
	clicmd.AddConfigFlags(command)

	return command
}

// formatAvailableVersions renders the `update list` output for the formae
// package: an "installed" line when a version is installed, then the distinct
// available versions (annotating the installed one). It reads the full
// candidate list directly rather than orbital's AvailableForSimple, which
// skips the candidate at index 0 — so a cold index (nothing installed) would
// omit the newest version or render an empty list.
//
// The human `update list` path now renders a themed list (see renderVersionList),
// which uses the same full-candidate-list read. This plain renderer is retained
// (with its cold-index tests) as the guard for that correctness fix and for the
// machine/plain output wired up in the follow-up styling pass.
//
//nolint:unused // retained for the plain/machine path (see comment above)
func formatAvailableVersions(available []*records.Package) string {
	var installed *records.Package
	for _, pkg := range available {
		if pkg != nil && pkg.Installed && pkg.Version != nil {
			installed = pkg
			break
		}
	}

	var b strings.Builder
	if installed != nil {
		fmt.Fprintf(&b, "installed: %s (%s)\n\n", installed.Version.Short(), installed.Version.Timestamp.String())
	}

	b.WriteString("available versions:\n\n")
	seen := make(map[string]bool, len(available))
	for _, entry := range available {
		if entry == nil || entry.Version == nil {
			continue
		}
		short := entry.Version.Short()
		if seen[short] {
			continue
		}
		seen[short] = true
		if installed != nil && entry.Version.Semver().EQ(installed.Version.Semver()) {
			age := "Newer"
			if entry.Version.LT(installed.Version) {
				age = "Older"
			}
			fmt.Fprintf(&b, "  %s %s: (%s)\n", short, age, entry.Version.Timestamp.String())
		} else {
			fmt.Fprintf(&b, "  %s\n", short)
		}
	}
	return b.String()
}

func UpdateListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List available formae versions",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} update list",
		},
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			channel, _ := cmd.Flags().GetString("channel")
			configFile, _ := cmd.Flags().GetString("config")

			a, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}
			a.PrintBanner()

			var orb *mgr.Manager
			if len(a.Config.Artifacts.Repositories) > 0 {
				orb, err = opsmgr.NewFromRepositoriesFiltered(slog.Default(), a.Config.Artifacts.Repositories, channel, false, false, pkgmodel.RepositoryTypeBinary)
			} else {
				orb, err = opsmgr.New(slog.Default(), a.Config.Artifacts.URL, channel, false, false)
			}
			if err != nil {
				return err
			}

			if !orb.Ready() {
				return fmt.Errorf("no managed installation root detected at: %s\n", orb.Path())
			}

			available, err := orb.AvailableFor("formae")
			if err != nil {
				return err
			}

			// Themed rendering fed by AvailableFor's FULL candidate list. Do NOT
			// use AvailableForSimple: it drops the index-0 candidate, which #555's
			// formatAvailableVersions (kept + tested for the plain path) exists to
			// guard against. Installed may be absent on a cold index.
			var installedShort string
			var installedDate time.Time
			seen := make(map[string]bool, len(available.Available))
			versions := make([]string, 0, len(available.Available))
			for _, pkg := range available.Available {
				if pkg == nil || pkg.Version == nil {
					continue
				}
				short := pkg.Version.Short()
				if pkg.Installed && installedShort == "" {
					installedShort = short
					installedDate = pkg.Version.Timestamp
				}
				if seen[short] {
					continue
				}
				seen[short] = true
				versions = append(versions, short)
			}

			th := themeFor(a)
			fmt.Print(renderVersionList(th, installedShort, installedDate, versions))

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().String("channel", "", "Override update channel")
	clicmd.AddConfigFlags(command)

	return command
}
