// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package update

import (
	"os"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
)

func TestMain(m *testing.M) {
	tuitest.PinRendering()
	os.Exit(m.Run())
}

func TestRenderVersionList_Golden(t *testing.T) {
	th := theme.New("formae")
	installed := "0.82.2"
	installedDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	available := []string{"0.83.0", "0.82.2", "0.82.1", "0.82.0"}

	out := renderVersionList(th, installed, installedDate, available)
	tuitest.RequireGolden(t, []byte(out))
}

func TestRenderVersionList_NoNewer(t *testing.T) {
	th := theme.New("formae")
	installed := "0.83.0"
	installedDate := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	available := []string{"0.83.0", "0.82.2", "0.82.1"}

	out := renderVersionList(th, installed, installedDate, available)
	tuitest.RequireGolden(t, []byte(out))
}
