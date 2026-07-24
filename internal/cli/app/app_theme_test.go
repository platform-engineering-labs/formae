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
