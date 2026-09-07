// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestErrorPanel_Structure(t *testing.T) {
	th := theme.New("formae")
	panel := ErrorPanel{
		Title:   "Conflicting Commands",
		Message: "Cannot submit command: conflicts with in-progress commands.",
		Details: []string{"cmd-abc123  apply reconcile  (started 2m ago)"},
		Suggestions: []string{
			"Wait for the conflicting command to finish, or cancel it with:",
			"  formae cancel --query='id:cmd-abc123'",
		},
	}
	out := panel.Render(th, 78)
	plain := ansi.Strip(out)
	lines := strings.Split(plain, "\n")

	assert.True(t, strings.HasPrefix(lines[0], "╭─ Conflicting Commands "), lines[0])
	assert.True(t, strings.HasSuffix(lines[0], "╮"))
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], "╰"))
	assert.Contains(t, plain, "Cannot submit command")
	assert.Contains(t, plain, "cmd-abc123")
	assert.Contains(t, plain, "formae cancel")
	for i, line := range lines {
		assert.Equal(t, 78, lipgloss.Width(line), "line %d must span the panel width", i)
	}
}

func TestErrorPanel_WrapsLongMessage(t *testing.T) {
	th := theme.New("formae")
	panel := ErrorPanel{
		Title:   "Error",
		Message: strings.Repeat("connection refused when reaching the formae agent ", 4),
	}
	out := ansi.Strip(panel.Render(th, 60))
	for i, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 60, "line %d overflows", i)
	}
}

func TestErrorPanel_MinimalFields(t *testing.T) {
	th := theme.New("formae")
	out := ansi.Strip(ErrorPanel{Title: "Target Already Exists",
		Message: "The target 'aws-us-east-1' already exists."}.Render(th, 78))
	assert.Contains(t, out, "already exists")
	assert.NotContains(t, out, "\n\n\n") // no stacked blank lines from empty sections
}

func TestErrorPanel_Golden(t *testing.T) {
	th := theme.New("formae")
	panel := ErrorPanel{
		Title:   "Resources Not Found",
		Message: "The forma references resources that do not exist:",
		Details: []string{
			th.Styles.Accent.Render("formae://my-vpc.SubnetId"),
			"  Referenced by: web-1 (AWS::EC2::Instance)",
		},
		Suggestions: []string{"Ensure the referenced resources exist or are defined in the forma."},
	}
	tuitest.RequireGolden(t, []byte(panel.Render(th, 78)))
}
