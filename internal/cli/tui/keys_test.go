// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultKeyMap(t *testing.T) {
	km := DefaultKeyMap()

	assert.NotEmpty(t, km.Up.Keys())
	assert.NotEmpty(t, km.Down.Keys())
	assert.NotEmpty(t, km.PageUp.Keys())
	assert.NotEmpty(t, km.PageDown.Keys())
	assert.NotEmpty(t, km.Search.Keys())
	assert.NotEmpty(t, km.ToggleDetail.Keys())
	assert.NotEmpty(t, km.Filter.Keys())
	assert.NotEmpty(t, km.AutoFollow.Keys())
	assert.NotEmpty(t, km.Enter.Keys())
	assert.NotEmpty(t, km.Back.Keys())
	assert.NotEmpty(t, km.Quit.Keys())
	assert.NotEmpty(t, km.Help.Keys())
}

func TestDefaultKeyMap_HelpText(t *testing.T) {
	km := DefaultKeyMap()

	// Each binding has a help description
	help := km.Quit.Help()
	assert.Equal(t, "q", help.Key)
	assert.Equal(t, "quit", help.Desc)
}
