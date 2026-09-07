//go:build unit

// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestAppThemeFromConfig(t *testing.T) {
	a := &App{Config: &pkgmodel.Config{Cli: pkgmodel.CliConfig{Theme: "rich"}}}
	assert.Equal(t, "rich", a.Theme().Name)
}

func TestAppThemeNilConfigDefaultsQuiet(t *testing.T) {
	var a *App
	assert.Equal(t, "quiet", a.Theme().Name)
}

func TestAppThemeFormaeAlias(t *testing.T) {
	a := &App{Config: &pkgmodel.Config{Cli: pkgmodel.CliConfig{Theme: "formae"}}}
	assert.Equal(t, "quiet", a.Theme().Name)
}

func TestAppThemeOmarchyFallsBackWhenAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := &App{Config: &pkgmodel.Config{Cli: pkgmodel.CliConfig{Theme: "omarchy"}}}
	// No Omarchy install → quiet fallback, no panic/error.
	if got := a.Theme().Name; got != "quiet" {
		t.Errorf("omarchy theme with no install = %q, want quiet fallback", got)
	}
}
