// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package project

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func fakeInstalledPlugins(plugins map[string]string) func(*app.App) (map[string]string, error) {
	return func(_ *app.App) (map[string]string, error) {
		return plugins, nil
	}
}

func fakeInstalledPluginsErr(err error) func(*app.App) (map[string]string, error) {
	return func(_ *app.App) (map[string]string, error) {
		return nil, err
	}
}

func fakeLocalScan(plugins map[string]string) func(string) (map[string]string, error) {
	return func(_ string) (map[string]string, error) {
		return plugins, nil
	}
}

func fakeLocalScanErr(err error) func(string) (map[string]string, error) {
	return func(_ string) (map[string]string, error) {
		return nil, err
	}
}

// withSeams swaps the package-level seams and returns a restore function.
func withSeams(
	installed func(*app.App) (map[string]string, error),
	local func(string) (map[string]string, error),
) func() {
	origInstalled := installedPluginsFn
	origLocal := localScanFn
	installedPluginsFn = installed
	localScanFn = local
	return func() {
		installedPluginsFn = origInstalled
		localScanFn = origLocal
	}
}

// ── pluginChoices — source matrix ────────────────────────────────────────────

// D10-A: agent returns plugins → choices from agent, fromLocalScan=false
func TestPluginChoices_AgentReturnsPlugins(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{"aws": "", "azure": ""}),
		fakeLocalScan(map[string]string{"local-only": ""}),
	)
	defer restore()

	choices, fromLocalScan, err := pluginChoices(nil, "~/.pel/formae/plugins")
	require.NoError(t, err)
	assert.False(t, fromLocalScan, "fromLocalScan must be false when agent responds")
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = c.Name
		assert.False(t, c.Local, "agent-sourced choices must have Local=false")
	}
	assert.ElementsMatch(t, []string{"aws", "azure"}, names)
}

// D10-B: agent errors + local scan has plugins → Local=true, fromLocalScan=true
func TestPluginChoices_AgentErrorLocalFallback(t *testing.T) {
	restore := withSeams(
		fakeInstalledPluginsErr(errors.New("connection refused")),
		fakeLocalScan(map[string]string{"aws": "", "myplugin": ""}),
	)
	defer restore()

	choices, fromLocalScan, err := pluginChoices(nil, "~/.pel/formae/plugins")
	require.NoError(t, err)
	assert.True(t, fromLocalScan, "fromLocalScan must be true on local fallback")
	for _, c := range choices {
		assert.True(t, c.Local, "all choices must have Local=true when from local scan")
	}
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = c.Name
	}
	assert.ElementsMatch(t, []string{"aws", "myplugin"}, names)
}

// D10-C: both agent and local scan are empty → empty choices
func TestPluginChoices_BothEmpty(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{}),
		fakeLocalScan(map[string]string{}),
	)
	defer restore()

	choices, _, err := pluginChoices(nil, "~/.pel/formae/plugins")
	require.NoError(t, err)
	assert.Empty(t, choices)
}

// D10-E: agent returns empty map with NO error, local scan is non-empty →
// must return agent result (empty choices, fromLocalScan=false), not fall to local scan.
func TestPluginChoices_AgentEmptyNoError_DoesNotFallToLocalScan(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{}),
		fakeLocalScan(map[string]string{"aws": "", "myplugin": ""}),
	)
	defer restore()

	choices, fromLocalScan, err := pluginChoices(nil, "~/.pel/formae/plugins")
	require.NoError(t, err)
	assert.False(t, fromLocalScan, "fromLocalScan must be false when agent responds without error")
	assert.Empty(t, choices, "choices must be empty when agent returns empty (must not fall to local scan)")
	for _, c := range choices {
		assert.False(t, c.Local, "no choice should have Local=true when agent responded")
	}
}

// D10-D: agent errors and local scan also errors → hard error
func TestPluginChoices_BothFail(t *testing.T) {
	restore := withSeams(
		fakeInstalledPluginsErr(errors.New("no agent")),
		fakeLocalScanErr(errors.New("no dir")),
	)
	defer restore()

	_, _, err := pluginChoices(nil, "~/.pel/formae/plugins")
	require.Error(t, err)
}

// ── @local suffix on local selections ────────────────────────────────────────

// R16: Local=true choices produce "name@local" include values.
func TestRunPluginSelect_LocalSelectionsGetAtLocalSuffix(t *testing.T) {
	restore := withSeams(
		fakeInstalledPluginsErr(errors.New("no agent")),
		fakeLocalScan(map[string]string{"aws": "", "azure": ""}),
	)
	defer restore()

	// Stub isInteractive to true.
	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	defer func() { isInteractive = origInteractive }()

	// Stub runMultiSelect to return "aws" as selected.
	origMultiSelect := runMultiSelect
	runMultiSelect = func(_ *theme.Theme, _ []pluginChoice, _ bool, _ string) ([]string, error) {
		return []string{"aws"}, nil
	}
	defer func() { runMultiSelect = origMultiSelect }()

	th := theme.New("formae")
	includes, err := runPluginSelect(th, nil, "~/.pel/formae/plugins", "pkl")
	require.NoError(t, err)
	require.Len(t, includes, 1)
	assert.Equal(t, "aws@local", includes[0], "local selection must carry @local suffix")
}

// Agent-sourced selections must NOT get the @local suffix.
func TestRunPluginSelect_AgentSelectionsNoAtLocal(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{"aws": "", "azure": ""}),
		fakeLocalScan(map[string]string{}),
	)
	defer restore()

	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	defer func() { isInteractive = origInteractive }()

	origMultiSelect := runMultiSelect
	runMultiSelect = func(_ *theme.Theme, _ []pluginChoice, _ bool, _ string) ([]string, error) {
		return []string{"aws"}, nil
	}
	defer func() { runMultiSelect = origMultiSelect }()

	th := theme.New("formae")
	includes, err := runPluginSelect(th, nil, "~/.pel/formae/plugins", "pkl")
	require.NoError(t, err)
	require.Len(t, includes, 1)
	assert.Equal(t, "aws", includes[0], "agent selection must NOT carry @local suffix")
}

// ── empty plugin list → distinct "no installed plugins found" confirm ─────────

// R17: empty list → confirm copy is about "no installed plugins" (not "no plugins selected").
func TestRunPluginSelect_EmptyList_DistinctConfirmCopy(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{}),
		fakeLocalScan(map[string]string{}),
	)
	defer restore()

	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	defer func() { isInteractive = origInteractive }()

	var capturedTitle string
	origRunConfirm := runConfirm
	runConfirm = func(_ *theme.Theme, title, _ string) (bool, error) {
		capturedTitle = title
		return true, nil
	}
	defer func() { runConfirm = origRunConfirm }()

	// runMultiSelect should NOT be called.
	origMultiSelect := runMultiSelect
	multiSelectCalled := false
	runMultiSelect = func(_ *theme.Theme, _ []pluginChoice, _ bool, _ string) ([]string, error) {
		multiSelectCalled = true
		return nil, nil
	}
	defer func() { runMultiSelect = origMultiSelect }()

	th := theme.New("formae")
	_, _ = runPluginSelect(th, nil, "~/.pel/formae/plugins", "pkl")

	assert.False(t, multiSelectCalled, "multi-select must not run when plugin list is empty")
	// Assert the empty-list copy is distinct from the selected-none copy.
	assert.NotContains(t, capturedTitle, "No plugins selected",
		"empty-list confirm title must differ from selected-none confirm title")
	assert.Contains(t, capturedTitle, "No installed plugins",
		"empty-list confirm title must mention 'No installed plugins'")
}

// When nothing is selected after multi-select, the consequence confirm copy is "No plugins selected."
func TestRunPluginSelect_NothingSelected_ConsequenceConfirmCopy(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{"aws": ""}),
		fakeLocalScan(map[string]string{}),
	)
	defer restore()

	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	defer func() { isInteractive = origInteractive }()

	// Multi-select returns nothing.
	origMultiSelect := runMultiSelect
	runMultiSelect = func(_ *theme.Theme, _ []pluginChoice, _ bool, _ string) ([]string, error) {
		return []string{}, nil
	}
	defer func() { runMultiSelect = origMultiSelect }()

	var capturedTitle string
	origRunConfirm := runConfirm
	runConfirm = func(_ *theme.Theme, title, _ string) (bool, error) {
		capturedTitle = title
		return true, nil
	}
	defer func() { runConfirm = origRunConfirm }()

	th := theme.New("formae")
	_, _ = runPluginSelect(th, nil, "~/.pel/formae/plugins", "pkl")

	assert.Contains(t, capturedTitle, "No plugins selected",
		"selected-none confirm title must say 'No plugins selected'")
	// And it must differ from the empty-list title.
	assert.NotContains(t, capturedTitle, "No installed plugins",
		"selected-none title must NOT say 'No installed plugins'")
}

// ── --yes flag: empty include, no prompt ─────────────────────────────────────

// R15: ProjectInitCmd --yes flag usage must mention "without plugins".
func TestProjectInitCmd_YesFlagUsageMentionsWithoutPlugins(t *testing.T) {
	cmd := ProjectInitCmd()
	yesFlag := cmd.Flags().Lookup("yes")
	require.NotNil(t, yesFlag)
	assert.Contains(t, strings.ToLower(yesFlag.Usage), "without plugins",
		"--yes usage string must mention 'without plugins'")
}

// ── fromLocalScan label in multi-select title ─────────────────────────────────

// When fromLocalScan=true, the title passed to runMultiSelect should contain the local note.
func TestRunPluginSelect_LocalScanTitleNote(t *testing.T) {
	restore := withSeams(
		fakeInstalledPluginsErr(errors.New("no agent")),
		fakeLocalScan(map[string]string{"aws": ""}),
	)
	defer restore()

	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	defer func() { isInteractive = origInteractive }()

	var capturedFromLocalScan bool
	origMultiSelect := runMultiSelect
	runMultiSelect = func(_ *theme.Theme, _ []pluginChoice, fromLocal bool, _ string) ([]string, error) {
		capturedFromLocalScan = fromLocal
		return []string{"aws"}, nil
	}
	defer func() { runMultiSelect = origMultiSelect }()

	th := theme.New("formae")
	_, _ = runPluginSelect(th, nil, "~/.pel/formae/plugins", "pkl")

	assert.True(t, capturedFromLocalScan,
		"fromLocalScan=true must be passed to runMultiSelect when source is local scan")
}

// ── schema is threaded through to multi-select (Finding 3) ───────────────────

// D10 / View 1: the schema value from the call site must reach runMultiSelect.
func TestRunPluginSelect_SchemaThreadedToMultiSelect(t *testing.T) {
	restore := withSeams(
		fakeInstalledPlugins(map[string]string{"aws": ""}),
		fakeLocalScan(map[string]string{}),
	)
	defer restore()

	origInteractive := isInteractive
	isInteractive = func() bool { return true }
	defer func() { isInteractive = origInteractive }()

	var capturedSchema string
	origMultiSelect := runMultiSelect
	runMultiSelect = func(_ *theme.Theme, _ []pluginChoice, _ bool, schema string) ([]string, error) {
		capturedSchema = schema
		return []string{"aws"}, nil
	}
	defer func() { runMultiSelect = origMultiSelect }()

	th := theme.New("formae")
	_, _ = runPluginSelect(th, nil, "~/.pel/formae/plugins", "pkl")

	assert.Equal(t, "pkl", capturedSchema, "schema passed to runPluginSelect must reach runMultiSelect")
}
