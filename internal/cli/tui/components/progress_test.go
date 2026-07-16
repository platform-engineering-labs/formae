// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestProgressBar_Content(t *testing.T) {
	th := theme.New("formae")
	tests := []struct {
		name                    string
		completed, total, width int
		wantFilled, wantWidth   int
	}{
		{"half", 5, 10, 10, 5, 10},
		{"empty", 0, 10, 10, 0, 10},
		{"full", 10, 10, 10, 10, 10},
		{"zero total renders empty track", 0, 0, 10, 0, 10},
		{"overshoot clamps to width", 15, 10, 10, 10, 10},
		{"rounds down", 12, 15, 14, 11, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(ProgressBar(th, tt.completed, tt.total, tt.width))
			assert.Equal(t, tt.wantWidth, len([]rune(got)))
			assert.Equal(t, tt.wantFilled, strings.Count(got, "━"))
			assert.Equal(t, tt.wantWidth-tt.wantFilled, strings.Count(got, "─"))
		})
	}
}

func TestProgressBar_NonPositiveWidth(t *testing.T) {
	th := theme.New("formae")
	assert.Equal(t, "", ProgressBar(th, 1, 2, 0))
	assert.Equal(t, "", ProgressBar(th, 1, 2, -3))
}

func TestProgressBar_Golden(t *testing.T) {
	th := theme.New("formae")
	out := ProgressBar(th, 12, 15, 40)
	tuitest.RequireGolden(t, []byte(out))
}
