// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func swapRunPluginInitFn(t *testing.T, capture *PluginInitOptions) {
	t.Helper()
	original := runPluginInitFn
	runPluginInitFn = func(_ context.Context, opts *PluginInitOptions) error {
		*capture = *opts
		return nil
	}
	t.Cleanup(func() { runPluginInitFn = original })
}

func TestPluginInitCmd_FlagBinding_Hub(t *testing.T) {
	var captured PluginInitOptions
	swapRunPluginInitFn(t, &captured)

	cmd := PluginInitCmd()
	cmd.SetArgs([]string{
		"--no-input",
		"--name", "myplugin",
		"--namespace", "MYNS",
		"--description", "desc",
		"--author", "me",
		"--module-path", "github.com/x/y",
		"--hub", "https://example.com",
	})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "https://example.com", captured.Hub)
}

func TestPluginInitCmd_FlagBinding_NoAvailabilityCheck(t *testing.T) {
	var captured PluginInitOptions
	swapRunPluginInitFn(t, &captured)

	cmd := PluginInitCmd()
	cmd.SetArgs([]string{
		"--no-input",
		"--name", "myplugin",
		"--namespace", "MYNS",
		"--description", "desc",
		"--author", "me",
		"--module-path", "github.com/x/y",
		"--no-availability-check",
	})
	require.NoError(t, cmd.Execute())

	assert.True(t, captured.NoAvailabilityCheck)
}

func TestPluginInitCmd_FlagBinding_AllowConflict(t *testing.T) {
	var captured PluginInitOptions
	swapRunPluginInitFn(t, &captured)

	cmd := PluginInitCmd()
	cmd.SetArgs([]string{
		"--no-input",
		"--name", "myplugin",
		"--namespace", "MYNS",
		"--description", "desc",
		"--author", "me",
		"--module-path", "github.com/x/y",
		"--allow-conflict",
	})
	require.NoError(t, cmd.Execute())

	assert.True(t, captured.AllowConflict)
}

func TestPluginInitCmd_FlagBinding_Defaults(t *testing.T) {
	var captured PluginInitOptions
	swapRunPluginInitFn(t, &captured)

	cmd := PluginInitCmd()
	cmd.SetArgs([]string{
		"--no-input",
		"--name", "myplugin",
		"--namespace", "MYNS",
		"--description", "desc",
		"--author", "me",
		"--module-path", "github.com/x/y",
	})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "", captured.Hub)
	assert.False(t, captured.NoAvailabilityCheck)
	assert.False(t, captured.AllowConflict)
}
