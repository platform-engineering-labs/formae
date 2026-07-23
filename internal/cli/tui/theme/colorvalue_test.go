//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColorValueString(t *testing.T) {
	var doc struct {
		C colorValue `toml:"c"`
	}
	_, err := toml.Decode(`c = "#FF6B00"`, &doc)
	require.NoError(t, err)
	assert.Equal(t, "#FF6B00", doc.C.Light)
	assert.Equal(t, "#FF6B00", doc.C.Dark)
	assert.Equal(t, lipglossAdaptive("#FF6B00", "#FF6B00"), doc.C.adaptive())
}

func TestColorValueTable(t *testing.T) {
	var doc struct {
		C colorValue `toml:"c"`
	}
	_, err := toml.Decode(`c = { light = "#111111", dark = "#EEEEEE" }`, &doc)
	require.NoError(t, err)
	assert.Equal(t, "#111111", doc.C.Light)
	assert.Equal(t, "#EEEEEE", doc.C.Dark)
}

func TestColorValueIsZero(t *testing.T) {
	assert.True(t, colorValue{}.isZero())
	assert.False(t, colorValue{Light: "#000000"}.isZero())
}

func lipglossAdaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}
