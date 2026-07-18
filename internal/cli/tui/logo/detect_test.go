// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"sync"
	"testing"
)

// TestDecide covers the full fixture matrix for decide().
// Each row maps an (envInfo, probeResult) pair to the expected Capability.
func TestDecide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		env   envInfo
		probe probeResult
		want  Capability
	}{
		// ── Hard disqualifiers → CapText ─────────────────────────────────────
		{
			name:  "non-TTY → CapText",
			env:   envInfo{IsTTY: false, Unicode: true},
			probe: probeResult{},
			want:  CapText,
		},
		{
			name:  "TERM=dumb → CapText",
			env:   envInfo{IsTTY: true, Dumb: true, Unicode: true},
			probe: probeResult{},
			want:  CapText,
		},
		{
			name:  "CI env → CapText",
			env:   envInfo{IsTTY: true, CI: true, Unicode: true},
			probe: probeResult{},
			want:  CapText,
		},
		{
			name:  "no Unicode support → CapText",
			env:   envInfo{IsTTY: true, Unicode: false},
			probe: probeResult{},
			want:  CapText,
		},
		// ── Probe results (trusted, no tmux/ssh) ─────────────────────────────
		{
			name:  "probe.Kitty → CapKitty",
			env:   envInfo{IsTTY: true, Unicode: true},
			probe: probeResult{Kitty: true},
			want:  CapKitty,
		},
		{
			name:  "probe.ITerm2 (no kitty) → CapITerm2",
			env:   envInfo{IsTTY: true, Unicode: true},
			probe: probeResult{ITerm2: true},
			want:  CapITerm2,
		},
		// ── Probes UNTRUSTED under tmux ───────────────────────────────────────
		{
			name:  "probe.Kitty but Tmux → CapBraille (probe ignored)",
			env:   envInfo{IsTTY: true, Tmux: true, Unicode: true},
			probe: probeResult{Kitty: true},
			want:  CapBraille,
		},
		{
			name:  "probe.ITerm2 but Tmux → CapBraille (probe ignored)",
			env:   envInfo{IsTTY: true, Tmux: true, Unicode: true},
			probe: probeResult{ITerm2: true},
			want:  CapBraille,
		},
		// ── Probes UNTRUSTED under SSH ────────────────────────────────────────
		{
			name:  "probe.Kitty but SSH → CapBraille (probe ignored)",
			env:   envInfo{IsTTY: true, SSH: true, Unicode: true},
			probe: probeResult{Kitty: true},
			want:  CapBraille,
		},
		{
			name:  "probe.ITerm2 but SSH → CapBraille (probe ignored)",
			env:   envInfo{IsTTY: true, SSH: true, Unicode: true},
			probe: probeResult{ITerm2: true},
			want:  CapBraille,
		},
		// ── Env hints insufficient for graphics (no probe) ────────────────────
		{
			name:  "KITTY_WINDOW_ID set but no probe → CapBraille",
			env:   envInfo{IsTTY: true, Unicode: true, KittyWindowID: true},
			probe: probeResult{},
			want:  CapBraille,
		},
		{
			name:  "TERM_PROGRAM=iTerm.app but no probe → CapBraille",
			env:   envInfo{IsTTY: true, Unicode: true, TermProgram: "iTerm.app"},
			probe: probeResult{},
			want:  CapBraille,
		},
		{
			name:  "WEZTERM env but no probe → CapBraille",
			env:   envInfo{IsTTY: true, Unicode: true, WezTerm: true},
			probe: probeResult{},
			want:  CapBraille,
		},
		// ── Fallthrough: TTY + Unicode, no probe → CapBraille ────────────────
		{
			name:  "TTY + Unicode, no probe → CapBraille",
			env:   envInfo{IsTTY: true, Unicode: true},
			probe: probeResult{},
			want:  CapBraille,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := decide(tt.env, tt.probe)
			if got != tt.want {
				t.Errorf("decide(%+v, %+v) = %v; want %v", tt.env, tt.probe, got, tt.want)
			}
		})
	}
}

// TestDetect_Stable checks that Detect() is idempotent (sync.Once caching).
// We swap the probe seam to avoid real terminal I/O.
func TestDetect_Stable(t *testing.T) {
	// Reset the global Once + result so this test is self-contained.
	detectOnce = sync.Once{}
	detectCached = CapText // will be overwritten on first call

	// Stub the probe seam to return a fixed value.
	origProbe := probe
	probe = func(_ envInfo) probeResult { return probeResult{} }
	t.Cleanup(func() {
		probe = origProbe
		detectOnce = sync.Once{}
	})

	first := Detect()
	second := Detect()

	if first != second {
		t.Errorf("Detect() not stable: first=%v second=%v", first, second)
	}
}
