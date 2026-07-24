// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// omarchyColors is the flat colors.toml an Omarchy theme publishes at
// ~/.config/omarchy/current/theme/colors.toml. Absent keys stay empty; the
// mapper (mapOmarchyPalette) applies per-key fallbacks.
type omarchyColors struct {
	Background          string `toml:"background"`
	Foreground          string `toml:"foreground"`
	Accent              string `toml:"accent"`
	Cursor              string `toml:"cursor"`
	SelectionForeground string `toml:"selection_foreground"`
	SelectionBackground string `toml:"selection_background"`
	Color0              string `toml:"color0"`
	Color1              string `toml:"color1"`
	Color2              string `toml:"color2"`
	Color3              string `toml:"color3"`
	Color4              string `toml:"color4"`
	Color5              string `toml:"color5"`
	Color6              string `toml:"color6"`
	Color7              string `toml:"color7"`
	Color8              string `toml:"color8"`
	Color9              string `toml:"color9"`
	Color10             string `toml:"color10"`
	Color11             string `toml:"color11"`
	Color12             string `toml:"color12"`
	Color13             string `toml:"color13"`
	Color14             string `toml:"color14"`
	Color15             string `toml:"color15"`
}

// parseOmarchyColors decodes an Omarchy colors.toml document.
func parseOmarchyColors(data []byte) (omarchyColors, error) {
	var oc omarchyColors
	if err := toml.Unmarshal(data, &oc); err != nil {
		return omarchyColors{}, fmt.Errorf("parse omarchy colors: %w", err)
	}
	return oc, nil
}
