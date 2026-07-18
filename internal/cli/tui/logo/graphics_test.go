// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestEncodeKitty_Golden(t *testing.T) {
	t.Parallel()
	out := encodeKitty(true, graphicsCols, graphicsRows)
	tuitest.RequireGolden(t, []byte(out))
}

func TestEncodeITerm2_Golden(t *testing.T) {
	t.Parallel()
	out := encodeITerm2(true, graphicsCols, graphicsRows)
	tuitest.RequireGolden(t, []byte(out))
}
