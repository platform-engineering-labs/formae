// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"os"
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
		{
			name:  "both probe.Kitty and probe.ITerm2 → CapKitty (Kitty wins)",
			env:   envInfo{IsTTY: true, Unicode: true},
			probe: probeResult{Kitty: true, ITerm2: true},
			want:  CapKitty,
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
		// ── herdr panes: inherited SSH_* must not suppress the probe ─────────
		{
			name:  "probe.Kitty, SSH inherited inside a herdr pane → CapKitty",
			env:   envInfo{IsTTY: true, SSH: true, Herdr: true, Unicode: true},
			probe: probeResult{Kitty: true},
			want:  CapKitty,
		},
		{
			name:  "probe.ITerm2, SSH inherited inside a herdr pane → CapITerm2",
			env:   envInfo{IsTTY: true, SSH: true, Herdr: true, Unicode: true},
			probe: probeResult{ITerm2: true},
			want:  CapITerm2,
		},
		{
			name:  "probe.Kitty, tmux inside a herdr pane → CapBraille (tmux wins)",
			env:   envInfo{IsTTY: true, Tmux: true, Herdr: true, Unicode: true},
			probe: probeResult{Kitty: true},
			want:  CapBraille,
		},
		{
			name:  "herdr pane with no probe result → CapBraille",
			env:   envInfo{IsTTY: true, Herdr: true, Unicode: true},
			probe: probeResult{},
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

// TestParseKittyProbe covers the pure parser for Kitty APC graphics responses.
// The function returns true only when the buffer contains an APC graphics
// response (\x1b_G...;OK...\x1b\\). DA1-only, error, or empty responses → false.
func TestParseKittyProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp []byte
		want bool
	}{
		{
			name: "real Kitty OK followed by DA1 → true",
			resp: []byte("\x1b_Gi=31;OK\x1b\\\x1b[?62;c"),
			want: true,
		},
		{
			name: "DA1-only response (non-Kitty terminal) → false",
			resp: []byte("\x1b[?62;c"),
			want: false,
		},
		{
			name: "empty buffer → false",
			resp: []byte{},
			want: false,
		},
		{
			name: "Kitty error response → false",
			resp: []byte("\x1b_Gi=31;ENOTSUPPORTED:no graphics\x1b\\\x1b[?62;c"),
			want: false,
		},
		{
			name: "Kitty OK without ST terminator still true → true",
			resp: []byte("\x1b_Gi=31;OK"),
			want: true,
		},
		{
			name: "APC prefix but no OK → false",
			resp: []byte("\x1b_Gi=31;ERR\x1b\\"),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseKittyProbe(tt.resp)
			if got != tt.want {
				t.Errorf("parseKittyProbe(%q) = %v; want %v", tt.resp, got, tt.want)
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

// TestProbeAllowed covers the gate shared by probe() and decide().
//
// tmux always owns the escape stream, so it always suppresses the probe. SSH
// suppresses it too, except inside a herdr pane: herdr's server outlives the
// client that started it, so SSH_* there describes the server's launch rather
// than the pane's terminal, and herdr answers the query itself.
func TestProbeAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  envInfo
		want bool
	}{
		{name: "plain terminal", env: envInfo{}, want: true},
		{name: "tmux", env: envInfo{Tmux: true}, want: false},
		{name: "ssh", env: envInfo{SSH: true}, want: false},
		{name: "herdr alone", env: envInfo{Herdr: true}, want: true},
		{name: "herdr with inherited ssh", env: envInfo{SSH: true, Herdr: true}, want: true},
		{name: "tmux inside herdr", env: envInfo{Tmux: true, Herdr: true}, want: false},
		{name: "tmux and ssh inside herdr", env: envInfo{Tmux: true, SSH: true, Herdr: true}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := probeAllowed(tt.env); got != tt.want {
				t.Errorf("probeAllowed(%+v) = %v; want %v", tt.env, got, tt.want)
			}
		})
	}
}

// TestProbeSeam_SkippedWhenNotAllowed asserts the real probe seam returns an
// empty result without touching the terminal whenever probeAllowed is false.
func TestProbeSeam_SkippedWhenNotAllowed(t *testing.T) {
	t.Parallel()

	for _, env := range []envInfo{
		{Tmux: true},
		{SSH: true},
		{Tmux: true, Herdr: true},
	} {
		if got := probe(env); got != (probeResult{}) {
			t.Errorf("probe(%+v) = %+v; want empty probeResult", env, got)
		}
	}
}

// TestGatherEnv_Herdr checks that HERDR_ENV, the marker herdr documents for
// detecting one of its panes, is the signal gatherEnv reads.
func TestGatherEnv_Herdr(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{name: "unset", set: false, want: false},
		{name: "empty", value: "", set: true, want: false},
		{name: "one", value: "1", set: true, want: true},
		{name: "zero", value: "0", set: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("HERDR_ENV", tt.value)
			} else {
				t.Setenv("HERDR_ENV", "1")
				os.Unsetenv("HERDR_ENV")
			}
			if got := gatherEnv().Herdr; got != tt.want {
				t.Errorf("gatherEnv().Herdr = %v; want %v", got, tt.want)
			}
		})
	}
}
