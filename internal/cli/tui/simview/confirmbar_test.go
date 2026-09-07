// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package simview

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
)

// groupWith builds a minimal simGroup containing a single resource row with
// the given opKind, sufficient for opCounts to tally it.
func groupWith(op opKind) simGroup {
	return simGroup{
		kind: kindResource,
		rows: []simRow{
			{op: op},
		},
	}
}

func TestConfirmBarBgSeverity(t *testing.T) {
	th := theme.New("rich") // ConfirmationBar.Color == "severity"
	// a group containing a delete -> Error
	del := []simGroup{groupWith(opDelete)}
	assert.Equal(t, th.Palette.Error, confirmBarBg(th, del))
	// only updates -> OpUpdate
	upd := []simGroup{groupWith(opUpdate)}
	assert.Equal(t, th.Palette.OpUpdate, confirmBarBg(th, upd))
	// only creates -> Done
	crt := []simGroup{groupWith(opCreate)}
	assert.Equal(t, th.Palette.Done, confirmBarBg(th, crt))
}

func TestConfirmBarBgBrand(t *testing.T) {
	th := theme.New("quiet") // ConfirmationBar.Color == "brand"
	del := []simGroup{groupWith(opDelete)}
	assert.Equal(t, th.Palette.SecondaryAccent, confirmBarBg(th, del))
}
