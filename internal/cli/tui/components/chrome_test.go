// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00"},
		{42 * time.Second, "00:42"},
		{5*time.Minute + 22*time.Second, "05:22"},
		{90 * time.Minute, "90:00"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, FormatDuration(tt.d))
	}
}

func TestPadBetween(t *testing.T) {
	t.Run("pads to total width", func(t *testing.T) {
		gap := PadBetween(20, "left", "right")
		assert.Equal(t, 20-4-5, len(gap))
	})
	t.Run("minimum one space when overflowing", func(t *testing.T) {
		assert.Equal(t, " ", PadBetween(5, "left", "right"))
	})
	t.Run("ANSI-aware", func(t *testing.T) {
		styled := lipgloss.NewStyle().Bold(true).Render("left")
		gap := PadBetween(20, styled, "right")
		assert.Equal(t, 20-4-5, len(gap))
	})
}

func TestHeaderBar_Layout(t *testing.T) {
	th := theme.New("formae")
	out := HeaderBar(th, "formae status command", "↻ live", 80)
	plain := ansi.Strip(out)
	lines := strings.Split(plain, "\n")
	assert.Len(t, lines, 2) // content + bottom border
	assert.Contains(t, lines[0], "formae status command")
	assert.Contains(t, lines[0], "↻ live")
	assert.Equal(t, 80, lipgloss.Width(out))
}

func TestFooterBar_Layout(t *testing.T) {
	th := theme.New("formae")
	out := FooterBar(th, 80, []KeyHint{{"enter", "drill in"}, {"q", "quit"}}, "42%")
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "enter: drill in")
	assert.Contains(t, plain, "q: quit")
	assert.Contains(t, plain, "42%")
	assert.Contains(t, plain, "?: help")
	assert.Equal(t, 80, lipgloss.Width(out))
}

func TestChrome_Golden(t *testing.T) {
	th := theme.New("formae")
	out := HeaderBar(th, "← esc", "↻ live", 80) + "\n" +
		FooterBar(th, 80, []KeyHint{{"d", "detail"}, {"/", "search"}}, "")
	tuitest.RequireGolden(t, []byte(out))
}
