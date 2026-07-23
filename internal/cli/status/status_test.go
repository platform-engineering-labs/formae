// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package status

import (
	"io"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStatusForHumans_UsesTUIOnlyForTTY(t *testing.T) {
	// runStatusForHumans consults package-level seams:
	//   var isTerminal = tui.IsTerminal
	//   var launchTUI  = launchStatusTUI
	// Override them in the test and assert the dispatch.
	calls := 0
	origLaunch := launchTUI
	origIsTerminal := isTerminal
	launchTUI = func(_ *app.App, _ *StatusOptions) error { calls++; return nil }
	isTerminal = func(_ io.Writer) bool { return true }
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		launchTUI = origLaunch
	})

	err := runStatusForHumans(nil, &StatusOptions{OutputLayout: StatusOutputSummary})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRunStatusForHumans_NonTTY_SkipsTUI(t *testing.T) {
	// When stdout is not a TTY, launchTUI must NOT be called, and printBanner
	// must be invoked to verify the seam works. Confirms the function takes the
	// real non-TTY path, not the TUI path.
	tuiCalls := 0
	bannerCalls := 0
	callOrder := []string{}

	origLaunch := launchTUI
	origIsTerminal := isTerminal
	origPrintBanner := printBanner

	launchTUI = func(_ *app.App, _ *StatusOptions) error {
		tuiCalls++
		callOrder = append(callOrder, "launchTUI")
		return nil
	}
	isTerminal = func(_ io.Writer) bool { return false }
	printBanner = func(_ *app.App) {
		bannerCalls++
		callOrder = append(callOrder, "printBanner")
	}

	t.Cleanup(func() {
		isTerminal = origIsTerminal
		launchTUI = origLaunch
		printBanner = origPrintBanner
	})

	// The panic is the downstream GetCommandsStatus dereferencing the unconfigured
	// app's nil Config — out of scope. bannerCalls==1 proves the non-TTY path was
	// entered and progressed past the banner.
	assert.Panics(t, func() {
		_ = runStatusForHumans(&app.App{}, &StatusOptions{OutputLayout: StatusOutputSummary})
	})
	assert.Equal(t, 0, tuiCalls, "launchTUI should not be called in non-TTY path")
	assert.Equal(t, 1, bannerCalls, "printBanner seam should be called once")
	assert.Equal(t, []string{"printBanner"}, callOrder)
}

func TestMaxResults_TUIKeepsFlagValue(t *testing.T) {
	// The RunE currently collapses MaxResults to 1 when no query is set;
	// the TUI path must keep the real flag value (default 10) so the
	// multi-command view has content. Assert via resolveMaxResults.
	assert.Equal(t, 10, resolveMaxResults("", 10, true /*tty human*/))
	assert.Equal(t, 1, resolveMaxResults("", 10, false /*machine or non-tty human*/))
	assert.Equal(t, 10, resolveMaxResults("state:InProgress", 10, false))
	// With a query, TTY or not, the flag value is respected.
	assert.Equal(t, 5, resolveMaxResults("foo", 5, true))
}

func TestValidateStatusOptions(t *testing.T) {
	t.Run("output-consumer should be human or machine", func(t *testing.T) {
		opts := &StatusOptions{
			OutputConsumer: printer.Consumer("invalid_consumer"),
		}
		err := validateStatusOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output consumer must be either 'human' or 'machine'", err.Error())
	})

	t.Run("output schema should be JSON or YAML for machine consumer", func(t *testing.T) {
		opts := &StatusOptions{
			OutputConsumer: "machine",
			OutputSchema:   "invalid_schema",
		}
		err := validateStatusOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output schema must be either 'json' or 'yaml' for machine consumer", err.Error())
	})

	t.Run("output layout should be detailed or summary", func(t *testing.T) {
		opts := &StatusOptions{
			OutputConsumer: "human",
			OutputLayout:   StatusOutput("invalid_layout"),
		}
		err := validateStatusOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output layout must be either 'detailed' or 'summary'", err.Error())
	})
}
