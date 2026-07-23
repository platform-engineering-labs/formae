// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package refresh

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/tui"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/components"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/opsmgr"
	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/spf13/cobra"
)

// refreshIsTerminal is a package seam so tests can force piped (non-TTY) behavior.
var refreshIsTerminal = tui.IsTerminal

// refreshErrorCounter wraps a slog.Handler and counts records logged at ERROR
// level. orbital's Refresh() logs per-repository fetch/validation failures
// (o.Error(...)) but always returns nil, so refresh counts those records to
// detect failures the API swallows rather than printing a false "done.".
type refreshErrorCounter struct {
	slog.Handler
	count *atomic.Int64
}

func (h refreshErrorCounter) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.count.Add(1)
	}
	return h.Handler.Handle(ctx, r)
}

// themeFor resolves the active theme from the app config.
// The name falls back to "formae" for nil configs (theme.New nil-guards internally).
func themeFor(a *app.App) *theme.Theme {
	name := ""
	if a != nil && a.Config != nil {
		name = a.Config.Cli.Theme
	}
	return theme.New(name)
}

// ackLine emits a single acknowledgment line to w. On a TTY it renders with
// lipgloss styling; when piped it writes plain text so output stays ANSI-free.
func ackLine(w io.Writer, tty bool, th *theme.Theme, m components.AckMarker, text string) {
	if tty {
		_, _ = fmt.Fprintln(w, components.AckLine(th, m, text))
		return
	}
	_, _ = fmt.Fprintln(w, components.AckLinePlain(m, text))
}

// RefreshCmd refreshes the locally cached package index for every configured
// repository across all channels. It writes to the package store, so on a
// stock (root-owned) install it elevates via sudo.
func RefreshCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the local package index for all channels",
		Long: `Refresh the locally cached package index for every configured
repository across all channels (stable and dev).

The non-destructive commands (plugin list/search/info, update list) read
this cache without sudo, so their data can be stale until you refresh. On
a stock install the package store is root-owned, so refresh may prompt for
sudo.`,
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("config")

			a, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}
			a.PrintBanner()

			// orbital's Refresh() always returns nil, logging per-repository
			// fetch failures at ERROR level instead. Count those so we don't
			// report success when a repo/channel was not actually warmed.
			var refreshErrors atomic.Int64
			logger := slog.New(refreshErrorCounter{Handler: slog.Default().Handler(), count: &refreshErrors})

			// refreshFn warms one channel and returns the number of repository
			// errors logged while doing so (delta on the shared counter).
			refreshFn := func(channel string) (int, error) {
				before := refreshErrors.Load()
				var orb *mgr.Manager
				var err error
				if len(a.Config.Artifacts.Repositories) > 0 {
					orb, err = opsmgr.NewFromRepositories(logger, a.Config.Artifacts.Repositories, channel, true, true)
				} else {
					orb, err = opsmgr.New(logger, a.Config.Artifacts.URL, channel, true, true)
				}
				if err != nil {
					return 0, err
				}
				if !orb.Ready() {
					return 0, fmt.Errorf("no managed installation root detected at: %s", orb.Path())
				}
				if err := orb.Refresh(); err != nil {
					return 0, err
				}
				return int(refreshErrors.Load() - before), nil
			}

			return runRefresh(os.Stdout, themeFor(a), constants.AllChannels, refreshFn)
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	clicmd.AddConfigFlags(command)

	return command
}

// runRefresh is the testable core of the refresh flow. It renders one animated
// step per channel (spinner on a TTY, result-line only when piped) and a final
// acknowledgment when every channel warmed cleanly. Repository errors logged
// during a channel's refresh surface as a per-channel warning and, in
// aggregate, a returned error so the exit code reflects the stale cache.
func runRefresh(w io.Writer, th *theme.Theme, channels []string, refreshFn func(channel string) (int, error)) error {
	tty := refreshIsTerminal(w)
	var totalErrors int

	for _, channel := range channels {
		step := components.StartStep(w, th, fmt.Sprintf("refreshing %s…", channel))
		delta, err := refreshFn(channel)
		if err != nil {
			step.Fail(fmt.Sprintf("failed to refresh %s", channel))
			return err
		}
		if delta > 0 {
			totalErrors += delta
			step.Warn(fmt.Sprintf("refreshed %s (%d repository error(s))", channel, delta))
			continue
		}
		step.Done(fmt.Sprintf("refreshed %s", channel))
	}

	if totalErrors > 0 {
		return fmt.Errorf("refresh completed with %d repository error(s); some package metadata may be stale — see the log for details", totalErrors)
	}

	ackLine(w, tty, th, components.AckDone, "package index up to date")
	return nil
}
