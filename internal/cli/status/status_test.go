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

// TestCompatDispatch_NoQuery verifies the deprecated `status command` alias,
// invoked with no --query at all, reproduces `command status` semantics: a
// single result, collapsed to one for non-TTY/machine callers exactly like
// the old verb's "most recent command" duty.
func TestCompatDispatch_NoQuery(t *testing.T) {
	opts := compatDispatch("", 10)
	assert.True(t, opts.Single, "no query must route to single-command semantics")
	assert.Equal(t, "", opts.Query)
	assert.Equal(t, "", opts.CommandID)
	assert.Equal(t, 1, opts.MaxResults, "no query must collapse to one result")
	assert.Equal(t, "status command", opts.HeaderCommand,
		"the deprecated alias must keep showing the verb the user actually typed")
}

// TestCompatDispatch_BareIDQuery verifies `--query 'id:<ksuid>'` alone routes
// to `command status <id>` semantics: single-command mode with the id
// preserved, so the reattach/static-print shortcut still applies.
func TestCompatDispatch_BareIDQuery(t *testing.T) {
	opts := compatDispatch("id:3Hrx15wROBJnYK2T5oEXKErKMVf", 10)
	assert.True(t, opts.Single, "a bare id query must route to single-command semantics")
	assert.Equal(t, "id:3Hrx15wROBJnYK2T5oEXKErKMVf", opts.Query)
	assert.Equal(t, "3Hrx15wROBJnYK2T5oEXKErKMVf", opts.CommandID,
		"the id must be extracted so the TUI can focus it")
	assert.Equal(t, 1, opts.MaxResults)
	assert.Equal(t, "status command", opts.HeaderCommand,
		"the deprecated alias must keep showing the verb the user actually typed")
}

// TestCompatDispatch_OtherQuery verifies any other query routes to `command
// list` semantics: browse mode, honoring the caller's --max-results.
func TestCompatDispatch_OtherQuery(t *testing.T) {
	opts := compatDispatch("client:me status:InProgress", 25)
	assert.False(t, opts.Single, "a non-bare-id query must route to list semantics")
	assert.Equal(t, "client:me status:InProgress", opts.Query)
	assert.Equal(t, "", opts.CommandID)
	assert.Equal(t, 25, opts.MaxResults, "list mode must honor --max-results, not collapse it")
	assert.Equal(t, "status command", opts.HeaderCommand,
		"the deprecated alias must keep showing the verb the user actually typed, even though it routes to command list's semantics underneath")
}

// TestRunStatusForHumans_TTYUnknownIDIsNotFound verifies that on a TTY, a
// `command status <id>` query for a nonexistent command exits with a
// not-found error instead of printing "(no commands)" and succeeding.
func TestRunStatusForHumans_TTYUnknownIDIsNotFound(t *testing.T) {
	origIsTerminal := isTerminal
	origFetch := fetchCommandsStatus
	origLaunch := launchTUI
	origBanner := printBanner
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		fetchCommandsStatus = origFetch
		launchTUI = origLaunch
		printBanner = origBanner
	})
	isTerminal = func(_ io.Writer) bool { return true }
	printBanner = func(_ *app.App) {}
	fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{}}, nil, nil
	}
	tuiCalls := 0
	launchTUI = func(_ *app.App, _ *StatusOptions) error { tuiCalls++; return nil }

	opts := &StatusOptions{
		Single:         true,
		CommandID:      "unknown-id",
		Query:          "id:unknown-id",
		MaxResults:     1,
		OutputLayout:   StatusOutputSummary,
		FailIfNotFound: true,
	}
	err := runStatusForHumans(&app.App{}, opts)
	require.Error(t, err, "an unknown command id must exit non-zero, not print '(no commands)'")
	assert.Contains(t, err.Error(), "unknown-id")
	assert.Equal(t, 0, tuiCalls, "the not-found error must be returned instead of opening the TUI")
}

// TestRunStatusForHumans_NonTTYUnknownIDIsNotFound verifies the same
// not-found behavior on the non-TTY (print-and-exit) path.
func TestRunStatusForHumans_NonTTYUnknownIDIsNotFound(t *testing.T) {
	origIsTerminal := isTerminal
	origFetch := fetchCommandsStatus
	origBanner := printBanner
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		fetchCommandsStatus = origFetch
		printBanner = origBanner
	})
	isTerminal = func(_ io.Writer) bool { return false }
	printBanner = func(_ *app.App) {}
	fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{}}, nil, nil
	}

	opts := &StatusOptions{
		Single:         true,
		CommandID:      "unknown-id",
		Query:          "id:unknown-id",
		MaxResults:     1,
		OutputLayout:   StatusOutputSummary,
		FailIfNotFound: true,
	}
	err := runStatusForHumans(&app.App{}, opts)
	require.Error(t, err, "an unknown command id must exit non-zero, not print '(no commands)'")
	assert.Contains(t, err.Error(), "unknown-id")
}

// TestRunStatusForHumans_DeprecatedAliasToleratesUnmatchedID verifies the
// deprecated `status command --query id:X` alias (which never sets
// FailIfNotFound) keeps its pre-existing tolerant behavior: an id that
// matches nothing still prints "(no commands)" and succeeds. Callers poll a
// just-submitted command by id this way, including a no-op command the
// server never persisted, and must not see that turned into an error.
func TestRunStatusForHumans_DeprecatedAliasToleratesUnmatchedID(t *testing.T) {
	origIsTerminal := isTerminal
	origFetch := fetchCommandsStatus
	origBanner := printBanner
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		fetchCommandsStatus = origFetch
		printBanner = origBanner
	})
	isTerminal = func(_ io.Writer) bool { return false }
	printBanner = func(_ *app.App) {}
	fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{}}, nil, nil
	}

	opts := compatDispatch("id:not-yet-persisted", 10)
	require.False(t, opts.FailIfNotFound,
		"the deprecated alias must never set FailIfNotFound")
	opts.OutputLayout = StatusOutputSummary
	err := runStatusForHumans(&app.App{}, opts)
	require.NoError(t, err, "the deprecated alias must keep tolerating an unmatched id")
}

// TestRunStatusForHumans_NoCommandsEverIsNotAnError verifies a bare `command
// status` with no id, when nothing has ever run, stays a legitimate empty
// result (prints "(no commands)", exits 0) rather than a not-found error:
// only an explicitly-requested, unmatched id is an error.
func TestRunStatusForHumans_NoCommandsEverIsNotAnError(t *testing.T) {
	origIsTerminal := isTerminal
	origFetch := fetchCommandsStatus
	origBanner := printBanner
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		fetchCommandsStatus = origFetch
		printBanner = origBanner
	})
	isTerminal = func(_ io.Writer) bool { return false }
	printBanner = func(_ *app.App) {}
	fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{}}, nil, nil
	}

	opts := &StatusOptions{Single: true, MaxResults: 1, OutputLayout: StatusOutputSummary}
	err := runStatusForHumans(&app.App{}, opts)
	require.NoError(t, err, "no commands ever run is a legitimate empty result, not a not-found error")
}

// TestRunStatusForMachines_UnknownIDIsNotFound verifies the machine-output
// path also fails loudly on an unknown command id rather than printing an
// empty result and exiting 0.
func TestRunStatusForMachines_UnknownIDIsNotFound(t *testing.T) {
	origFetch := fetchCommandsStatus
	t.Cleanup(func() { fetchCommandsStatus = origFetch })
	fetchCommandsStatus = func(_ *app.App, _ string, _ int) (*apimodel.ListCommandStatusResponse, []string, error) {
		return &apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{}}, nil, nil
	}

	opts := &StatusOptions{
		CommandID:      "unknown-id",
		Query:          "id:unknown-id",
		OutputConsumer: printer.ConsumerMachine,
		OutputSchema:   "json",
		OutputLayout:   StatusOutputSummary,
		FailIfNotFound: true,
	}
	err := runStatusForMachines(&app.App{}, opts)
	require.Error(t, err, "an unknown command id must exit non-zero for machine consumers too")
	assert.Contains(t, err.Error(), "unknown-id")
}

// TestCompatDispatch_WildcardIDQueryIsNotBare verifies a wildcarded id query
// (which cannot single out one command) is not mistaken for the bare-id
// case, so it still routes to list semantics.
func TestCompatDispatch_WildcardIDQueryIsNotBare(t *testing.T) {
	opts := compatDispatch("id:abc*", 10)
	assert.False(t, opts.Single, "a wildcarded id query must route to list semantics")
	assert.Equal(t, 10, opts.MaxResults)
}
