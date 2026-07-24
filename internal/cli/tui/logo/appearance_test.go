// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import "testing"

// The appearance precedence is: FORMAE_APPEARANCE env > cli.appearance config >
// auto-detect (COLORFGBG, then OSC-11). These tests pin each layer of that chain
// through the pure ResolveDarkBackground entry point. COLORFGBG stands in for the
// auto-detect layer: a trailing field of 7 or 15 means a light background, so
// "15;0" auto-detects dark and "15;7" auto-detects light.

func TestResolveDarkBackground_EnvOverridesConfig(t *testing.T) {
	// A light env override must win even when config says dark, and vice versa.
	t.Setenv("COLORFGBG", "") // keep auto-detect out of the way

	t.Setenv("FORMAE_APPEARANCE", "light")
	if ResolveDarkBackground("dark") {
		t.Error("FORMAE_APPEARANCE=light must override cli.appearance=dark")
	}

	t.Setenv("FORMAE_APPEARANCE", "dark")
	if !ResolveDarkBackground("light") {
		t.Error("FORMAE_APPEARANCE=dark must override cli.appearance=light")
	}
}

func TestResolveDarkBackground_ConfigOverridesAutoDetect(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "") // no env override → config layer decides

	// Auto-detect would report LIGHT (bg field 7); config=dark must win.
	t.Setenv("COLORFGBG", "15;7")
	if !ResolveDarkBackground("dark") {
		t.Error("cli.appearance=dark must override auto-detected light background")
	}

	// Auto-detect would report DARK (bg field 0); config=light must win.
	t.Setenv("COLORFGBG", "15;0")
	if ResolveDarkBackground("light") {
		t.Error("cli.appearance=light must override auto-detected dark background")
	}
}

func TestResolveDarkBackground_AutoDefersToDetection(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")

	t.Setenv("COLORFGBG", "0;15") // light background
	if ResolveDarkBackground("auto") {
		t.Error("cli.appearance=auto must defer to auto-detect (light here)")
	}

	t.Setenv("COLORFGBG", "15;0") // dark background
	if !ResolveDarkBackground("auto") {
		t.Error("cli.appearance=auto must defer to auto-detect (dark here)")
	}
}

func TestResolveDarkBackground_EmptyAndUnknownDeferToAuto(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "0;15") // light background

	for _, appearance := range []string{"", "auto", "banana"} {
		if ResolveDarkBackground(appearance) {
			t.Errorf("appearance=%q should defer to auto-detect (light here)", appearance)
		}
	}
}

func TestResolveDarkBackground_CaseAndWhitespaceInsensitive(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "15;7") // auto-detect would say light

	if !ResolveDarkBackground("  DARK  ") {
		t.Error("appearance value should be trimmed and case-folded before matching")
	}
}

// HasDarkBackground is the config-less shorthand (env + auto-detect only); adding
// the config layer must not change its behavior for the many per-frame callers.
func TestHasDarkBackground_IgnoresConfigLayer(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "0;15") // light background
	if HasDarkBackground() {
		t.Error("HasDarkBackground() must equal the env+auto result (light here)")
	}
}
