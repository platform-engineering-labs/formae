// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTheme(t *testing.T) {
	th := New("formae")
	assert.Equal(t, "formae", th.Name)
	assert.NotEmpty(t, th.Styles.Title.Render("test"))
}

func TestNewThemeClassic(t *testing.T) {
	th := New("classic")
	assert.Equal(t, "classic", th.Name)
	assert.NotEmpty(t, th.Styles.Title.Render("test"))
}

func TestNewThemeDefault(t *testing.T) {
	th := New("")
	assert.Equal(t, "formae", th.Name)
}

func TestNewThemeUnknown(t *testing.T) {
	th := New("doesnotexist")
	assert.Equal(t, "formae", th.Name, "unknown theme names should normalize to formae")
}
