// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestSectionHeader(t *testing.T) {
	th := theme.New("formae")

	t.Run("contains_bar_and_title", func(t *testing.T) {
		result := SectionHeader(th, "Resources")
		assert.Contains(t, result, "▌ Resources")
	})

	t.Run("plain_text_contains_title", func(t *testing.T) {
		result := SectionHeader(th, "Resources")
		plain := stripANSI(result)
		assert.Contains(t, plain, "▌ Resources")
	})

	t.Run("different_title", func(t *testing.T) {
		result := SectionHeader(th, "Targets")
		assert.True(t, strings.Contains(result, "▌ Targets") || strings.Contains(stripANSI(result), "▌ Targets"))
	})
}
