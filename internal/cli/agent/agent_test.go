// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package agent

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartCmd_PersistentPreRunThemesBanner verifies that `agent start` applies
// the configured CLI theme to the banner before printing it, so the logo
// wordmark follows the user's theme (rich/omarchy/…) instead of the default.
//
// The theme resolution itself loads config through the PKL schema plugin, which
// is exercised elsewhere; here we assert the wiring — that the command's
// lifecycle themes the banner at all — via the themeBanner seam.
func TestStartCmd_PersistentPreRunThemesBanner(t *testing.T) {
	orig := themeBanner
	t.Cleanup(func() { themeBanner = orig })

	var themed bool
	themeBanner = func(_ *cobra.Command) { themed = true }

	c := startCmd()
	require.NotNil(t, c.PersistentPreRun, "agent start must have a PersistentPreRun that prints the banner")
	c.PersistentPreRun(c, nil)

	assert.True(t, themed, "agent start must apply the configured theme to the banner before printing it")
}

// TestStopCmd_PersistentPreRunThemesBanner verifies the same for `agent stop`.
func TestStopCmd_PersistentPreRunThemesBanner(t *testing.T) {
	orig := themeBanner
	t.Cleanup(func() { themeBanner = orig })

	var themed bool
	themeBanner = func(_ *cobra.Command) { themed = true }

	c := stopCmd()
	require.NotNil(t, c.PersistentPreRun, "agent stop must have a PersistentPreRun that prints the banner")
	c.PersistentPreRun(c, nil)

	assert.True(t, themed, "agent stop must apply the configured theme to the banner before printing it")
}
