// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package logo

import (
	"sync"
	"testing"
)

// TestIsHardDisqualified is a table-driven unit test for the pure
// isHardDisqualified function.  It also documents the invariant relied on by
// Detect(): when isHardDisqualified returns true the probe seam is NEVER
// called, so no escape-sequence traffic is sent to disqualified terminals.
func TestIsHardDisqualified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  envInfo
		want bool
	}{
		{
			name: "non-TTY → disqualified",
			env:  envInfo{IsTTY: false, Unicode: true},
			want: true,
		},
		{
			name: "TERM=dumb → disqualified",
			env:  envInfo{IsTTY: true, Dumb: true, Unicode: true},
			want: true,
		},
		{
			name: "CI env → disqualified",
			env:  envInfo{IsTTY: true, CI: true, Unicode: true},
			want: true,
		},
		{
			name: "no Unicode support → disqualified",
			env:  envInfo{IsTTY: true, Dumb: false, CI: false, Unicode: false},
			want: true,
		},
		{
			name: "TTY + Unicode, no dumb/ci → NOT disqualified",
			env:  envInfo{IsTTY: true, Dumb: false, CI: false, Unicode: true},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isHardDisqualified(tt.env)
			if got != tt.want {
				t.Errorf("isHardDisqualified(%+v) = %v; want %v", tt.env, got, tt.want)
			}
		})
	}
}

// TestDetect_DisqualifiedSkipsProbe verifies that Detect() does NOT invoke the
// probe seam when the environment is hard-disqualified.  In the test process
// stdout is not a real TTY, so gatherEnv() always produces a disqualified env
// (IsTTY=false).  We stub the probe seam to panic if called; a successful run
// proves probe was skipped.
func TestDetect_DisqualifiedSkipsProbe(t *testing.T) {
	// Reset the global Once so this test gets a fresh run.
	detectOnce = sync.Once{}
	detectCached = CapKitty // sentinel; will be overwritten
	t.Cleanup(func() {
		detectOnce = sync.Once{}
	})

	origProbe := probe
	probeCalled := false
	probe = func(_ envInfo) probeResult {
		probeCalled = true
		return probeResult{}
	}
	t.Cleanup(func() { probe = origProbe })

	got := Detect()

	// In a headless test environment stdout is not a TTY, so gatherEnv()
	// returns IsTTY=false → isHardDisqualified=true → CapText, no probe.
	if got != CapText {
		t.Errorf("Detect() = %v; want CapText (disqualified env)", got)
	}
	if probeCalled {
		t.Error("probe seam was called even though the environment is hard-disqualified")
	}
}
