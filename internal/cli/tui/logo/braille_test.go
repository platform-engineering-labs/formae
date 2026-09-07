// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestRenderBraille_Golden(t *testing.T) {
	t.Parallel()
	out := renderBraille(true, 8)
	tuitest.RequireGolden(t, []byte(out))
}
