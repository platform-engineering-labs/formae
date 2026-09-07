// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package update

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/orbital/opm/records"

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

// TestRenderUpdateList_PipedIsPlain verifies the piped (non-TTY) branch of
// `update list` produces the plain, ANSI-free listing (formatAvailableVersions),
// not the themed list.
func TestRenderUpdateList_PipedIsPlain(t *testing.T) {
	th := theme.New("formae")
	available := []*records.Package{pkg(0, 88, 0, true), pkg(0, 87, 1, false)}

	out := renderUpdateList(th, false, available)

	if strings.Contains(out, "\x1b[") {
		t.Errorf("piped update list must be ANSI-free, got:\n%q", out)
	}
	// Plain renderer format: lowercase "installed:" / "available versions:".
	if !strings.Contains(out, "installed: 0.88.0") {
		t.Errorf("expected plain installed line, got:\n%s", out)
	}
	if !strings.Contains(out, "available versions:") {
		t.Errorf("expected plain section label, got:\n%s", out)
	}
	if out != formatAvailableVersions(available) {
		t.Errorf("piped branch must equal formatAvailableVersions output")
	}
}

// TestRenderUpdateList_TTYIsThemed verifies the TTY branch produces the themed
// list (renderVersionList): styled output with the title-cased section label.
func TestRenderUpdateList_TTYIsThemed(t *testing.T) {
	th := theme.New("formae")
	available := []*records.Package{pkg(0, 88, 0, true), pkg(0, 87, 1, false)}

	out := renderUpdateList(th, true, available)

	// PinRendering forces a color profile, so the themed path emits ANSI.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("themed update list must carry ANSI styling, got:\n%q", out)
	}
	// Themed renderer uses a SectionHeader ("▌ Available versions").
	if !strings.Contains(out, "Available versions") {
		t.Errorf("expected themed section header, got:\n%s", out)
	}
	if !strings.Contains(out, "0.88.0") || !strings.Contains(out, "0.87.1") {
		t.Errorf("expected both versions listed, got:\n%s", out)
	}
}
