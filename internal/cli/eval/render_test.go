// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package eval

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestRenderEvalHeader_Golden(t *testing.T) {
	th := theme.New("formae")
	out := renderEvalHeader(th, "main.pkl", "reconcile")
	tuitest.RequireGolden(t, []byte(out))
}
