// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// FieldList renders aligned "Label:   value" pairs. Labels get a trailing
// colon and are padded (plain text, then styled — never pad styled strings)
// so all values start at the same column.
func FieldList(th *theme.Theme, pairs [][2]string) string {
	maxLabel := 0
	for _, p := range pairs {
		if utf8.RuneCountInString(p[0]) > maxLabel {
			maxLabel = utf8.RuneCountInString(p[0])
		}
	}
	labelStyle := lipgloss.NewStyle().Foreground(th.Palette.TextSecondary)
	valueStyle := lipgloss.NewStyle().Foreground(th.Palette.TextPrimary)
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		label := Pad(p[0]+":", maxLabel+1)
		lines = append(lines, labelStyle.Render(label)+"  "+valueStyle.Render(p[1]))
	}
	return strings.Join(lines, "\n")
}
