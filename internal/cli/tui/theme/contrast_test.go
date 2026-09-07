// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuiltinSelectionBandIsReadable guards every built-in theme's cursor-row
// selection band against the WCAG contrast floor its own foreground text
// must clear (the enforced invariant), driven off builtinNames() so a newly
// added built-in is covered automatically.
//
// Only the two same-side pairs are checked (Light band vs Light text, Dark
// band vs Dark text): no call site ever pairs a .Light band with a .Dark
// foreground. Three views pin both the band and the cursor-row foreground to
// .Dark, and every other call site resolves both sides adaptively together,
// so a cross-side pairing never actually occurs.
func TestBuiltinSelectionBandIsReadable(t *testing.T) {
	for _, name := range builtinNames() {
		t.Run(name, func(t *testing.T) {
			th, ok := loadBuiltin(name)
			require.True(t, ok)
			p := th.Palette

			assert.GreaterOrEqual(t, contrast(p.Selection.Light, p.TextPrimary.Light), minBandText,
				"light: contrast(selection, text_primary)")
			assert.GreaterOrEqual(t, contrast(p.Selection.Dark, p.TextPrimary.Dark), minBandText,
				"dark: contrast(selection, text_primary)")
		})
	}
}

// TestBuiltinSelectionBandIsVisibleAgainstBase separately checks the weaker,
// conceded preference: the selection band should also be visible against the
// panel background, not just readable against the text on top of it.
func TestBuiltinSelectionBandIsVisibleAgainstBase(t *testing.T) {
	for _, name := range builtinNames() {
		t.Run(name, func(t *testing.T) {
			th, ok := loadBuiltin(name)
			require.True(t, ok)
			p := th.Palette

			assert.GreaterOrEqual(t, contrast(p.Selection.Light, p.Base.Light), minBandVisible,
				"light: contrast(selection, base)")
			assert.GreaterOrEqual(t, contrast(p.Selection.Dark, p.Base.Dark), minBandVisible,
				"dark: contrast(selection, base)")
		})
	}
}
