// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cmd_test

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// TestRethemeUsage_TemplatesFollowTheme verifies that re-theming rebuilds the
// usage template in the given theme's colors and re-applies it to the command,
// so quiet and rich (which differ in the "done" color used for command names)
// produce different templates. Color is forced on because lipgloss strips it in
// a non-TTY test process.
func TestRethemeUsage_TemplatesFollowTheme(t *testing.T) {
	origProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(origProfile) })

	origRoot, origSimple := cmd.RootCmdUsageTemplate, cmd.SimpleCmdUsageTemplate
	t.Cleanup(func() {
		cmd.RootCmdUsageTemplate, cmd.SimpleCmdUsageTemplate = origRoot, origSimple
	})

	c := &cobra.Command{Use: "demo"}
	c.SetUsageTemplate(cmd.SimpleCmdUsageTemplate)

	cmd.RethemeUsage(c, theme.New("quiet"))
	quietTpl := c.UsageTemplate()

	cmd.RethemeUsage(c, theme.New("rich"))
	richTpl := c.UsageTemplate()

	if quietTpl == richTpl {
		t.Error("simple usage template must differ between quiet and rich themes")
	}
	if richTpl != cmd.SimpleCmdUsageTemplate {
		t.Error("RethemeUsage must re-apply the rebuilt simple template to the command")
	}
}

// TestResolveConfiguredTheme_FallsBackToDefault verifies that theme resolution
// never returns nil — an unresolvable command yields the default theme so help
// always renders.
func TestResolveConfiguredTheme_FallsBackToDefault(t *testing.T) {
	c := &cobra.Command{Use: "demo"} // no --profile/--config flags registered
	if got := cmd.ResolveConfiguredTheme(c); got == nil {
		t.Fatal("ResolveConfiguredTheme must never return nil")
	}
}
