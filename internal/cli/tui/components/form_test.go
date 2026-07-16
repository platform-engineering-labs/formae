// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestHuhTheme_MapsPalette(t *testing.T) {
	th := theme.New("formae")
	ht := HuhTheme(th)
	require.NotNil(t, ht)
	assert.Equal(t, th.Palette.Error, ht.Focused.ErrorMessage.GetForeground())
	assert.Equal(t, th.Palette.SecondaryAccent, ht.Focused.FocusedButton.GetBackground())
}

func TestNewConfirm_AffirmativeFlow(t *testing.T) {
	th := theme.New("formae")
	var ok bool
	form := NewConfirm(th, "Apply changes?", "3 resources will be created.", &ok)
	// form.Run() wires these; when embedding the form as a tea.Model the
	// caller owns them.
	form.SubmitCmd = tea.Quit

	tm := tuitest.Run(t, form, 80, 24)
	tuitest.WaitForContains(t, tm, "Apply changes?")
	tm.Type("y") // Accept binding answers and submits
	tm.WaitFinished(t)

	assert.True(t, ok)
}

func TestNewConfirm_NegativeFlow(t *testing.T) {
	th := theme.New("formae")
	ok := true
	form := NewConfirm(th, "Apply changes?", "", &ok)
	form.SubmitCmd = tea.Quit

	tm := tuitest.Run(t, form, 80, 24)
	tuitest.WaitForContains(t, tm, "Apply changes?")
	tm.Type("n") // Reject binding
	tm.WaitFinished(t)

	assert.False(t, ok)
}

func TestNewThemedForm_ReturnsForm(t *testing.T) {
	th := theme.New("formae")
	var name string
	f := NewThemedForm(th, huh.NewGroup(huh.NewInput().Title("Name").Value(&name)))
	require.NotNil(t, f)
}
