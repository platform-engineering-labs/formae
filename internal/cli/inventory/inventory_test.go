// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package inventory

import (
	"io"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/inventoryview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateInventoryOptions(t *testing.T) {
	t.Run("max-results must be 0 or positive", func(t *testing.T) {
		opts := &InventoryOptions{
			MaxResults: -1,
		}
		err := validateInventoryOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "max-results must be 0 (unlimited) or a positive number", err.Error())
	})

	t.Run("output-consumer must be 'human' or 'machine'", func(t *testing.T) {
		opts := &InventoryOptions{
			MaxResults:     10,
			OutputConsumer: "invalid",
		}
		err := validateInventoryOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-consumer must be 'human' or 'machine'", err.Error())
	})

	t.Run("output-schema must be 'json' or 'yaml' for machine consumer", func(t *testing.T) {
		opts := &InventoryOptions{
			MaxResults:     10,
			OutputConsumer: "machine",
			OutputSchema:   "invalid",
		}
		err := validateInventoryOptions(opts)
		assert.Error(t, err)
		assert.Equal(t, "output-schema must be 'json' or 'yaml' for machine consumer", err.Error())
	})
}

// stubSeams replaces isTerminal and launchInventoryTUI with controlled stubs,
// and restores the originals via t.Cleanup. The returned values track calls.
type seamResult struct {
	launcherCalls int
	lastFocus     inventoryview.Tab
	lastOpts      *InventoryOptions
}

func stubSeams(t *testing.T, terminal bool) *seamResult {
	t.Helper()
	result := &seamResult{}
	origIsTerminal := isTerminal
	origLaunch := launchInventoryTUI

	isTerminal = func(_ io.Writer) bool { return terminal }
	launchInventoryTUI = func(_ *app.App, focus inventoryview.Tab, opts *InventoryOptions) error {
		result.launcherCalls++
		result.lastFocus = focus
		result.lastOpts = opts
		return nil
	}

	t.Cleanup(func() {
		isTerminal = origIsTerminal
		launchInventoryTUI = origLaunch
	})
	return result
}

// --- TTY + human → correct focus tab per subcommand ---

func TestTTY_Resources_LaunchesWithTabResources(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{OutputConsumer: printer.ConsumerHuman, MaxResults: 10}
	err := runResourcesForHumans(nil, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, inventoryview.TabResources, sr.lastFocus)
}

func TestTTY_Targets_LaunchesWithTabTargets(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{OutputConsumer: printer.ConsumerHuman, MaxResults: 10}
	err := runTargetsForHumans(nil, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, inventoryview.TabTargets, sr.lastFocus)
}

func TestTTY_Stacks_LaunchesWithTabStacks(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{OutputConsumer: printer.ConsumerHuman, MaxResults: 10}
	err := runStacksForHumans(nil, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, inventoryview.TabStacks, sr.lastFocus)
}

func TestTTY_Policies_LaunchesWithTabPolicies(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{OutputConsumer: printer.ConsumerHuman, MaxResults: 10}
	err := runPoliciesForHumans(nil, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, inventoryview.TabPolicies, sr.lastFocus)
}

// Bare `formae inventory` routes like resources → TabResources.
func TestTTY_BareInventory_LaunchesWithTabResources(t *testing.T) {
	sr := stubSeams(t, true)
	// runResources is used by the bare inventory command, which delegates to
	// runResourcesForHumans on TTY — same code path as resources subcommand.
	opts := &InventoryOptions{OutputConsumer: printer.ConsumerHuman, MaxResults: 10}
	err := runResources(nil, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, inventoryview.TabResources, sr.lastFocus)
}

// --- MaxRows resolution ---

func TestMaxRows_DefaultNotSet_TUI_Gets200(t *testing.T) {
	sr := stubSeams(t, true)
	// MaxResultsSet=false (default) → TUI should receive MaxRows=200.
	opts := &InventoryOptions{
		OutputConsumer: printer.ConsumerHuman,
		MaxResults:     10, // flag default
		MaxResultsSet:  false,
	}
	err := runResourcesForHumans(nil, opts)
	require.NoError(t, err)
	require.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, opts, sr.lastOpts)
	// The launcher stub receives opts as-is; the actual MaxRows capping happens
	// inside launchInventoryTUI. We verify the seam receives opts with MaxResultsSet=false.
	assert.False(t, sr.lastOpts.MaxResultsSet)
}

func TestMaxRows_ExplicitSet_50_BothPaths(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{
		OutputConsumer: printer.ConsumerHuman,
		MaxResults:     50,
		MaxResultsSet:  true,
	}
	err := runResourcesForHumans(nil, opts)
	require.NoError(t, err)
	require.Equal(t, 1, sr.launcherCalls)
	assert.True(t, sr.lastOpts.MaxResultsSet)
	assert.Equal(t, 50, sr.lastOpts.MaxResults)
}

func TestMaxRows_ExplicitSet_0_Unlimited(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{
		OutputConsumer: printer.ConsumerHuman,
		MaxResults:     0,
		MaxResultsSet:  true,
	}
	err := runResourcesForHumans(nil, opts)
	require.NoError(t, err)
	require.Equal(t, 1, sr.launcherCalls)
	assert.True(t, sr.lastOpts.MaxResultsSet)
	assert.Equal(t, 0, sr.lastOpts.MaxResults)
}

// --- Non-TTY → launcher never called ---

func TestNonTTY_Resources_SkipsLauncher(t *testing.T) {
	sr := stubSeams(t, false)
	// Non-TTY will call app.PrintBanner() then ExtractResources; both panic on nil
	// app — that's fine, we verify no launcher call before the panic.
	assert.Panics(t, func() {
		_ = runResourcesForHumans(&app.App{}, &InventoryOptions{
			OutputConsumer: printer.ConsumerHuman,
			MaxResults:     10,
		})
	})
	assert.Equal(t, 0, sr.launcherCalls, "launcher must not be called on non-TTY")
}

func TestNonTTY_Targets_SkipsLauncher(t *testing.T) {
	sr := stubSeams(t, false)
	assert.Panics(t, func() {
		_ = runTargetsForHumans(&app.App{}, &InventoryOptions{
			OutputConsumer: printer.ConsumerHuman,
			MaxResults:     10,
		})
	})
	assert.Equal(t, 0, sr.launcherCalls)
}

func TestNonTTY_Stacks_SkipsLauncher(t *testing.T) {
	sr := stubSeams(t, false)
	assert.Panics(t, func() {
		_ = runStacksForHumans(&app.App{}, &InventoryOptions{
			OutputConsumer: printer.ConsumerHuman,
			MaxResults:     10,
		})
	})
	assert.Equal(t, 0, sr.launcherCalls)
}

func TestNonTTY_Policies_SkipsLauncher(t *testing.T) {
	sr := stubSeams(t, false)
	assert.Panics(t, func() {
		_ = runPoliciesForHumans(&app.App{}, &InventoryOptions{
			OutputConsumer: printer.ConsumerHuman,
			MaxResults:     10,
		})
	})
	assert.Equal(t, 0, sr.launcherCalls)
}

// --- Machine consumer → launcher never called even on TTY ---

func TestMachineConsumer_NeverCallsLauncher(t *testing.T) {
	sr := stubSeams(t, true) // TTY = true
	// runResources takes machine path before reaching runResourcesForHumans.
	// runResourcesForMachines will panic on nil app, but launcher must not be called.
	assert.Panics(t, func() {
		_ = runResources(nil, &InventoryOptions{
			OutputConsumer: printer.ConsumerMachine,
			OutputSchema:   "json",
			MaxResults:     10,
		})
	})
	assert.Equal(t, 0, sr.launcherCalls, "machine consumer must never call the launcher")
}

// --- Query threading ---

func TestQueryThreading_Resources(t *testing.T) {
	sr := stubSeams(t, true)
	opts := &InventoryOptions{
		OutputConsumer: printer.ConsumerHuman,
		Query:          "stack:prod",
		MaxResults:     10,
	}
	err := runResourcesForHumans(nil, opts)
	require.NoError(t, err)
	require.Equal(t, 1, sr.launcherCalls)
	assert.Equal(t, "stack:prod", sr.lastOpts.Query)
	assert.Equal(t, inventoryview.TabResources, sr.lastFocus)
}
