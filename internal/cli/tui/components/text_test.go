// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"regexp"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

// ansiRe strips well-formed ANSI CSI sequences (colors, bold, etc.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestPad(t *testing.T) {
	t.Run("pads_short_string", func(t *testing.T) {
		assert.Equal(t, "ab  ", Pad("ab", 4))
	})
	t.Run("truncates_long_string", func(t *testing.T) {
		assert.Equal(t, "abcd", Pad("abcdef", 4))
	})
	t.Run("exact_width_unchanged", func(t *testing.T) {
		assert.Equal(t, "abcd", Pad("abcd", 4))
	})
	t.Run("empty_string", func(t *testing.T) {
		assert.Equal(t, "    ", Pad("", 4))
	})
	t.Run("unicode_rune_count", func(t *testing.T) {
		// "über" is 4 runes; pad to 6 → 2 spaces
		assert.Equal(t, "über  ", Pad("über", 6))
	})
}

func TestTruncate(t *testing.T) {
	t.Run("truncates_long_string_with_ellipsis", func(t *testing.T) {
		assert.Equal(t, "abc…", Truncate("abcdef", 4))
	})
	t.Run("short_string_unchanged", func(t *testing.T) {
		assert.Equal(t, "ab", Truncate("ab", 4))
	})
	t.Run("exact_width_unchanged", func(t *testing.T) {
		assert.Equal(t, "abcd", Truncate("abcd", 4))
	})
	t.Run("unicode", func(t *testing.T) {
		// 6 runes, truncate to 4: keep 3 runes + ellipsis
		assert.Equal(t, "übe…", Truncate("überlang", 4))
	})
}

func TestPadStyled(t *testing.T) {
	th := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true)
	styled := th.Render("hi")

	t.Run("lipgloss_width_equals_w", func(t *testing.T) {
		result := PadStyled(styled, 10)
		assert.Equal(t, 10, lipgloss.Width(result))
	})

	t.Run("no_ansi_fragment_garbage", func(t *testing.T) {
		// After stripping well-formed ANSI sequences there must be NO leftover
		// bracket-digit sequences like "[1;38..." that indicate sliced escape codes.
		result := PadStyled(styled, 10)
		plain := stripANSI(result)
		assert.NotRegexp(t, `\[[0-9;]+[A-Za-z]`, plain,
			"PadStyled result contains ANSI fragment garbage after stripping: %q", plain)
	})

	t.Run("does_not_truncate", func(t *testing.T) {
		// PadStyled never truncates — if already wider than w, width stays >= w
		wide := th.Render("hello world this is very long text")
		result := PadStyled(wide, 5)
		assert.GreaterOrEqual(t, lipgloss.Width(result), lipgloss.Width(wide),
			"PadStyled must not truncate content")
	})
}
