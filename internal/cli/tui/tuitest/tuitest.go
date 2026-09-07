// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package tuitest provides shared test infrastructure for TUI components:
// deterministic lipgloss rendering, teatest program helpers and golden-file
// assertions, and redaction-assertion helpers. Golden files live in each
// package's testdata/ directory and are regenerated with `go test -update`.
package tuitest

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
)

// PinRendering forces a fixed color profile and background so rendered
// output (and therefore golden files) does not depend on the terminal the
// tests run in. Call it from TestMain before m.Run().
func PinRendering() {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
}

// Run starts a teatest program for the model at a fixed terminal size.
func Run(t *testing.T, m tea.Model, width, height int) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, m, teatest.WithInitialTermSize(width, height))
}

// WaitForContains polls the program output until it contains want, failing
// the test after a short timeout.
func WaitForContains(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte(want))
	}, teatest.WithDuration(3*time.Second))
}

// RequireGolden compares out against testdata/<TestName>.golden.
func RequireGolden(t *testing.T, out []byte) {
	t.Helper()
	teatest.RequireEqualOutput(t, out)
}

// DecodeByteArrays rewrites every "[104 105]"-shaped decimal byte sequence in s
// as the bytes it encodes, leaving everything else untouched.
//
// It exists for redaction assertions. A json.RawMessage rendered by %v, or by a
// text log handler, becomes a decimal byte array rather than the JSON it
// carries, so a plaintext substring assertion over such output passes while the
// value it is looking for is fully present. Decode first, then assert.
func DecodeByteArrays(s string) string {
	var out strings.Builder
	for {
		open := strings.IndexByte(s, '[')
		if open < 0 {
			out.WriteString(s)
			return out.String()
		}
		closing := strings.IndexByte(s[open:], ']')
		if closing < 0 {
			out.WriteString(s)
			return out.String()
		}
		closing += open
		out.WriteString(s[:open])
		if decoded, ok := decodeDecimalBytes(s[open+1 : closing]); ok {
			out.WriteString(decoded)
		} else {
			out.WriteString(s[open : closing+1])
		}
		s = s[closing+1:]
	}
}

// decodeDecimalBytes decodes "104 105" to "hi". ok is false when body is not a
// space-separated list of byte-ranged decimals.
func decodeDecimalBytes(body string) (string, bool) {
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return "", false
	}
	buf := make([]byte, 0, len(fields))
	for _, f := range fields {
		n := 0
		for _, c := range f {
			if c < '0' || c > '9' {
				return "", false
			}
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return "", false
		}
		buf = append(buf, byte(n))
	}
	return string(buf), true
}
