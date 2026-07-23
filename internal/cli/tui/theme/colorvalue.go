// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// colorValue is a theme palette color parsed from TOML. It accepts either a
// bare hex string (same value for light and dark backgrounds) or an inline
// table { light = "#..", dark = "#.." }.
type colorValue struct {
	Light string
	Dark  string
}

// UnmarshalTOML accepts a string or a { light, dark } table.
func (c *colorValue) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		c.Light, c.Dark = t, t
		return nil
	case map[string]any:
		light, lok := t["light"].(string)
		dark, dok := t["dark"].(string)
		if !lok || !dok {
			return fmt.Errorf("color table must have string light and dark keys, got %v", t)
		}
		c.Light, c.Dark = light, dark
		return nil
	default:
		return fmt.Errorf("color must be a hex string or a { light, dark } table, got %T", v)
	}
}

// adaptive converts to the lipgloss form used throughout the theme.
func (c colorValue) adaptive() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: c.Light, Dark: c.Dark}
}

// isZero reports whether the value is unset (used by extends merge).
func (c colorValue) isZero() bool {
	return c.Light == "" && c.Dark == ""
}
