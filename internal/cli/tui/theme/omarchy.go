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

// mapOmarchyPalette translates an Omarchy colors.toml into formae's semantic
// palette (design §6.1). Each color is mirrored onto both adaptive sides: the
// Omarchy palette is the terminal's actual colors, so it renders faithfully
// regardless of background detection. Per-key fallbacks keep every semantic
// slot populated even from a sparse colors.toml.
func mapOmarchyPalette(oc omarchyColors) paletteFile {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	mirror := func(hex string) *colorValue {
		if hex == "" {
			return nil
		}
		return &colorValue{Light: hex, Dark: hex}
	}

	textSecondary := pick(oc.Color7, oc.Foreground)
	textSubtle := pick(oc.Color8, oc.Color7, oc.Foreground)
	border := pick(oc.Color8, oc.Color7)
	primary := pick(oc.Accent, oc.Color4)
	secondary := pick(oc.Color5, oc.Accent)

	return paletteFile{
		Base:            mirror(oc.Background),
		Surface:         mirror(oc.Background),
		TextPrimary:     mirror(oc.Foreground),
		TextSecondary:   mirror(textSecondary),
		TextSubtle:      mirror(textSubtle),
		Border:          mirror(border),
		Selection:       mirror(pick(oc.SelectionBackground, oc.Color8)),
		PrimaryAccent:   mirror(primary),
		SecondaryAccent: mirror(secondary),
		Error:           mirror(oc.Color1),
		ErrorSubtle:     mirror(oc.Color1),
		ErrorBright:     mirror(oc.Color1),
		Warning:         mirror(oc.Color3),
		Done:            mirror(oc.Color2),
		InProgress:      mirror(pick(oc.Color8, oc.Color7)),
		Pending:         mirror(oc.Color8),
		OpCreate:        mirror(oc.Color2),
		OpUpdate:        mirror(oc.Color3),
		OpDelete:        mirror(oc.Color1),
		OpReplace:       mirror(secondary),
		OpDetach:        mirror(oc.Color8),
		OpKeep:          mirror(oc.Color8),
	}
}
