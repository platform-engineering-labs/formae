// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package logo

import (
	"sync"
	"testing"
)

// The appearance precedence is: FORMAE_APPEARANCE env > cli.appearance config >
// auto-detect (a live OSC-11 query in a direct terminal; COLORFGBG elsewhere).
// These tests pin each layer of that chain through the pure ResolveDarkBackground
// entry point. Tests run without a TTY, so COLORFGBG stands in for the
// auto-detect layer: a trailing field of 7 or 15 means a light background, so
// "15;0" auto-detects dark and "15;7" auto-detects light.

func TestResolveDarkBackground_EnvOverridesConfig(t *testing.T) {
	// A light env override must win even when config says dark, and vice versa.
	t.Setenv("COLORFGBG", "") // keep auto-detect out of the way

	t.Setenv("FORMAE_APPEARANCE", "light")
	if ResolveDarkBackground("dark") {
		t.Error("FORMAE_APPEARANCE=light must override cli.appearance=dark")
	}

	t.Setenv("FORMAE_APPEARANCE", "dark")
	if !ResolveDarkBackground("light") {
		t.Error("FORMAE_APPEARANCE=dark must override cli.appearance=light")
	}
}

func TestResolveDarkBackground_ConfigOverridesAutoDetect(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "") // no env override → config layer decides

	// Auto-detect would report LIGHT (bg field 7); config=dark must win.
	t.Setenv("COLORFGBG", "15;7")
	if !ResolveDarkBackground("dark") {
		t.Error("cli.appearance=dark must override auto-detected light background")
	}

	// Auto-detect would report DARK (bg field 0); config=light must win.
	t.Setenv("COLORFGBG", "15;0")
	if ResolveDarkBackground("light") {
		t.Error("cli.appearance=light must override auto-detected dark background")
	}
}

func TestResolveDarkBackground_AutoDefersToDetection(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")

	t.Setenv("COLORFGBG", "0;15") // light background
	if ResolveDarkBackground("auto") {
		t.Error("cli.appearance=auto must defer to auto-detect (light here)")
	}

	t.Setenv("COLORFGBG", "15;0") // dark background
	if !ResolveDarkBackground("auto") {
		t.Error("cli.appearance=auto must defer to auto-detect (dark here)")
	}
}

func TestResolveDarkBackground_EmptyAndUnknownDeferToAuto(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "0;15") // light background

	for _, appearance := range []string{"", "auto", "banana"} {
		if ResolveDarkBackground(appearance) {
			t.Errorf("appearance=%q should defer to auto-detect (light here)", appearance)
		}
	}
}

func TestResolveDarkBackground_CaseAndWhitespaceInsensitive(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "15;7") // auto-detect would say light

	if !ResolveDarkBackground("  DARK  ") {
		t.Error("appearance value should be trimmed and case-folded before matching")
	}
}

// HasDarkBackground is the config-less shorthand (env + auto-detect only); adding
// the config layer must not change its behavior for the many per-frame callers.
func TestHasDarkBackground_IgnoresConfigLayer(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "0;15") // light background
	if HasDarkBackground() {
		t.Error("HasDarkBackground() must equal the env+auto result (light here)")
	}
}

// Tests must not depend on whether `go test` happens to run with stdout on a
// terminal (local-directory mode streams the test binary straight to it). Pin
// the terminal env to "no TTY" so Detect stays disqualified and the appearance
// auto-detect layer is COLORFGBG-driven; tests that need a TTY opt in via
// stubAppearance or by calling decide/probe with an explicit envInfo.
func init() {
	terminalEnv = func() envInfo {
		e := gatherEnv()
		e.IsTTY = false
		return e
	}
}

// stubAppearance replaces the auto-detect seams for one test and resets the
// memoized query so each test sees a fresh decision.
func stubAppearance(t *testing.T, env envInfo, live bool) {
	t.Helper()
	origEnv, origQuery := terminalEnv, queryDarkBackground
	t.Cleanup(func() {
		terminalEnv, queryDarkBackground = origEnv, origQuery
		darkBgOnce = sync.Once{}
	})
	darkBgOnce = sync.Once{}
	terminalEnv = func() envInfo { return env }
	queryDarkBackground = func() bool { return live }
}

// A direct terminal must use the live background, not a COLORFGBG that the
// terminal computed when the session started (iTerm2 keeps it after a macOS
// light/dark switch).
func TestAutoDetect_DirectTTYPrefersLiveQueryOverStaleCOLORFGBG(t *testing.T) {
	t.Setenv("FORMAE_APPEARANCE", "")
	t.Setenv("COLORFGBG", "15;0") // stale: says dark
	stubAppearance(t, envInfo{IsTTY: true, Unicode: true}, false /* live: light */)
	if ResolveDarkBackground("") {
		t.Error("direct TTY: live OSC-11 answer (light) must win over stale COLORFGBG (dark)")
	}
}

// Under tmux, or where there is nothing to query (no TTY, CI, dumb), COLORFGBG
// is used and the terminal is never queried.
func TestAutoDetect_UnsafeContextsUseCOLORFGBGWithoutQuery(t *testing.T) {
	for name, env := range map[string]envInfo{
		"no tty":               {IsTTY: false},
		"tmux":                 {IsTTY: true, Tmux: true},
		"tmux in a herdr pane": {IsTTY: true, Tmux: true, Herdr: true},
		"ci":                   {IsTTY: true, CI: true},
		"dumb":                 {IsTTY: true, Dumb: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("FORMAE_APPEARANCE", "")
			t.Setenv("COLORFGBG", "0;15") // light
			stubAppearance(t, env, true)
			queryDarkBackground = func() bool {
				t.Error("must not query the terminal here")
				return true
			}
			if ResolveDarkBackground("") {
				t.Error("COLORFGBG=0;15 must yield light")
			}
		})
	}
}

// Over ssh (including a herdr pane that inherited SSH_* variables) the live
// answer, which Bubble Tea's init already obtained over the session, wins over
// COLORFGBG, which sshd rarely forwards and which may be stale.
func TestAutoDetect_SSHUsesLiveAnswer(t *testing.T) {
	for name, env := range map[string]envInfo{
		"ssh":                     {IsTTY: true, Unicode: true, SSH: true},
		"herdr pane with ssh env": {IsTTY: true, Unicode: true, SSH: true, Herdr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("FORMAE_APPEARANCE", "")
			t.Setenv("COLORFGBG", "15;0") // says dark
			stubAppearance(t, env, false /* live: light */)
			if ResolveDarkBackground("") {
				t.Error("live answer (light) must win over COLORFGBG (dark)")
			}
		})
	}
}
