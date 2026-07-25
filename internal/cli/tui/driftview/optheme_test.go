// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package driftview

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

func TestDriftOpColorFromTheme(t *testing.T) {
	th := theme.New("rich")
	assert.Equal(t, th.Palette.OpDelete, driftOpColor(th.Palette, rowClassDelete))
	assert.Equal(t, th.Palette.OpCreate, driftOpColor(th.Palette, rowClassCreate))
	assert.Equal(t, th.Palette.OpUpdate, driftOpColor(th.Palette, rowClassUpdate))
}
