// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Pad pads or truncates PLAIN (unstyled) s to exactly w display cells (rune
// count). This function operates on plain text only; never pass ANSI-styled
// strings — ANSI escape codes are counted as runes and will corrupt the output.
func Pad(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		runes := []rune(s)
		return string(runes[:w])
	}
	return s + strings.Repeat(" ", w-n)
}

// Truncate cuts PLAIN (unstyled) s to at most w runes, appending … when the
// string is cut. w must be >= 1. This function operates on plain text only;
// never pass ANSI-styled strings — escape codes are counted as runes and will
// corrupt the output.
func Truncate(s string, w int) string {
	if w < 1 {
		w = 1
	}
	runes := []rune(s)
	if len(runes) > w-1 {
		// need at least w-1 chars + ellipsis to fill w, but only cut if > w runes
		if len(runes) <= w {
			return s
		}
		return string(runes[:w-1]) + "…"
	}
	return s
}

// PadStyled right-pads a possibly-styled string to w cells using
// lipgloss.Width for ANSI-aware measurement. It never truncates — callers
// should truncate plain text before styling. If the styled string is already
// wider than w, it is returned unchanged.
func PadStyled(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-sw)
}
