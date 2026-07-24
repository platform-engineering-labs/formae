//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import "testing"

func TestParseOmarchyColors(t *testing.T) {
	data := []byte(`
background = "#1a1b26"
foreground = "#c0caf5"
accent = "#7aa2f7"
cursor = "#c0caf5"
selection_foreground = "#1a1b26"
selection_background = "#33467c"
color0 = "#15161e"
color1 = "#f7768e"
color2 = "#9ece6a"
color3 = "#e0af68"
color4 = "#7aa2f7"
color5 = "#bb9af7"
color7 = "#a9b1d6"
color8 = "#414868"
`)
	oc, err := parseOmarchyColors(data)
	if err != nil {
		t.Fatalf("parseOmarchyColors: %v", err)
	}
	if oc.Background != "#1a1b26" {
		t.Errorf("Background = %q, want #1a1b26", oc.Background)
	}
	if oc.Accent != "#7aa2f7" {
		t.Errorf("Accent = %q, want #7aa2f7", oc.Accent)
	}
	if oc.Color1 != "#f7768e" {
		t.Errorf("Color1 = %q, want #f7768e", oc.Color1)
	}
	if oc.SelectionBackground != "#33467c" {
		t.Errorf("SelectionBackground = %q, want #33467c", oc.SelectionBackground)
	}
	// color6 absent → empty (mapper handles fallback)
	if oc.Color6 != "" {
		t.Errorf("Color6 = %q, want empty", oc.Color6)
	}
}

func TestParseOmarchyColors_Malformed(t *testing.T) {
	if _, err := parseOmarchyColors([]byte("this is = = not toml")); err == nil {
		t.Error("expected a parse error for malformed TOML")
	}
}
