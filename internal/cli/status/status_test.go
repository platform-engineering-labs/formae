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
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrepareScreen_ThemesBannerViaApp verifies the watch-mode screen refresh
// renders its banner through the app-aware printBanner seam (which applies the
// configured theme) rather than the default-themed banner.PrintBanner(). The
// caller's app must reach printBanner so the logo follows the user's theme.
func TestPrepareScreen_ThemesBannerViaApp(t *testing.T) {
	origBanner := printBanner
	t.Cleanup(func() { printBanner = origBanner })

	var gotApp *app.App
	called := 0
	printBanner = func(a *app.App) { called++; gotApp = a }

	a := &app.App{}
	prepareScreen(a, "commands status")

	assert.Equal(t, 1, called, "prepareScreen must render the banner via the themed printBanner seam")
	assert.Same(t, a, gotApp, "prepareScreen must pass its app to printBanner so the banner uses the configured theme")
}

func TestRunStatusForHumans_TTYInteractiveOnlyWhenRunning(t *testing.T) {
	// On a TTY, runStatusForHumans opens the live TUI only when a matched
	// command is still running; when everything is terminal it prints a static
	// summary. Stub the package-level seams to drive the decision.
	origLaunch := launchTUI
	origIsTerminal := isTerminal
	origFetch := fetchCommandsStatus
	origBanner := printBanner
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		launchTUI = origLaunch
		fetchCommandsStatus = origFetch
		printBanner = origBanner
	})
	isTerminal = func(_ io.Writer) bool { return true }
	printBanner = func(_ *app.App) {}

	stubFetch := func(state string) {
		fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
			return &apimodel.ListCommandStatusResponse{
				Commands: []apimodel.Command{{CommandID: "c1", State: state}},
			}, nil, nil
		}
	}
	run := func(query string, single bool) int {
		calls := 0
		launchTUI = func(_ *app.App, _ *StatusOptions) error { calls++; return nil }
		err := runStatusForHumans(nil, &StatusOptions{Query: query, Single: single, OutputLayout: StatusOutputSummary})
		require.NoError(t, err)
		return calls
	}

	t.Run("single, running → live TUI", func(t *testing.T) {
		stubFetch("InProgress")
		assert.Equal(t, 1, run("id:c1", true), "a running re-attach should open the TUI")
	})

	t.Run("single, terminal → static, no TUI", func(t *testing.T) {
		stubFetch("Success")
		assert.Equal(t, 0, run("id:c1", true), "a finished re-attach should print static, not open the TUI")
	})

	t.Run("broad query → always the interactive list", func(t *testing.T) {
		stubFetch("Success") // terminal, but a browse (non-single) query still opens the list
		assert.Equal(t, 1, run("client:me", false), "a browse query should always open the TUI")
	})
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

func TestRunStatusForHumans_SingleDiscoversCommandIDWhenRunning(t *testing.T) {
	// When Single is set without an explicit CommandID (the no-argument
	// `command status` case), a running match must have its id captured onto
	// opts.CommandID so the TUI can focus it, even though the caller never
	// supplied one up front.
	origLaunch := launchTUI
	origIsTerminal := isTerminal
	origFetch := fetchCommandsStatus
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		launchTUI = origLaunch
		fetchCommandsStatus = origFetch
	})
	isTerminal = func(_ io.Writer) bool { return true }
	fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{
			Commands: []apimodel.Command{{CommandID: "discovered-1", State: "InProgress"}},
		}, nil, nil
	}
	var gotOpts *StatusOptions
	launchTUI = func(_ *app.App, opts *StatusOptions) error { gotOpts = opts; return nil }

	opts := &StatusOptions{Single: true, MaxResults: 1, OutputLayout: StatusOutputSummary}
	require.NoError(t, runStatusForHumans(nil, opts))

	require.NotNil(t, gotOpts)
	assert.Equal(t, "discovered-1", gotOpts.CommandID,
		"the fetched single command's id must be captured for the TUI to focus")
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

// TestRunStatus_ExportedEntryPointValidates verifies the exported RunStatus
// seam (the entry point the command package's `status` and `list`
// subcommands call into) applies the same option validation as the
// unexported RunE previously did inline.
func TestRunStatus_ExportedEntryPointValidates(t *testing.T) {
	opts := &StatusOptions{
		OutputConsumer: printer.Consumer("bogus"),
		OutputLayout:   StatusOutputSummary,
	}
	err := RunStatus(nil, opts)
	assert.Error(t, err)
	assert.Equal(t, "output consumer must be either 'human' or 'machine'", err.Error())
}

// TestExportedAgentStatsSeams verifies the small set of helpers the agent
// package's `agent status` subcommand reuses (rather than re-implementing
// the panel rendering) are exported and behave like their unexported
// counterparts.
func TestExportedAgentStatsSeams(t *testing.T) {
	th := ThemeFor(&app.App{})
	require.NotNil(t, th)

	stats := apimodel.Stats{Version: "1.2.3"}
	out := RenderAgentStats(th, stats, 120)
	assert.Contains(t, out, "1.2.3")

	assert.Equal(t, 100, TermWidth(io.Discard), "non-TTY writers fall back to width 100")
}
