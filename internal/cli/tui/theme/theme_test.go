// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTheme(t *testing.T) {
	th := New("quiet")
	assert.Equal(t, "quiet", th.Name)
	assert.NotEmpty(t, th.Styles.Title.Render("test"))
}

func TestNewThemeFormaeAlias(t *testing.T) {
	th := New("formae")
	assert.Equal(t, "quiet", th.Name, "formae is an alias for quiet")
	assert.NotEmpty(t, th.Styles.Title.Render("test"))
}

// TestNewThemeClassic documents that classic, once a built-in, is now just
// an unknown theme name and falls back to quiet.
func TestNewThemeClassic(t *testing.T) {
	th := New("classic")
	assert.Equal(t, "quiet", th.Name, "classic was removed as a built-in; it falls back to quiet")
	assert.NotEmpty(t, th.Styles.Title.Render("test"))
}

func TestNewThemeDefault(t *testing.T) {
	th := New("")
	assert.Equal(t, "quiet", th.Name)
}

func TestNewThemeUnknown(t *testing.T) {
	th := New("doesnotexist")
	assert.Equal(t, "quiet", th.Name, "unknown theme names should fall back to quiet")
}

func TestNewThemeHeaderHighlight(t *testing.T) {
	assert.Equal(t, "background", New("rich").Header.Highlight)
	assert.Equal(t, "brighten", New("quiet").Header.Highlight)
	assert.Equal(t, "brighten", New("colorblind").Header.Highlight)
}
