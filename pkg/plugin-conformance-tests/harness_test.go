// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReporter is a local testReporter fake, shaped like collectingReporter,
// except Errorf records the message instead of failing the test — so a test
// can assert that a helper never reported an error.
type fakeReporter struct {
	t      *testing.T
	logs   []string
	errors []string
}

func (r *fakeReporter) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *fakeReporter) Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.logs = append(r.logs, msg)
	r.t.Logf(format, args...)
}

// logContaining reports whether any recorded Logf message contains every one
// of substrs.
func (r *fakeReporter) logContaining(substrs ...string) bool {
	for _, msg := range r.logs {
		matched := true
		for _, s := range substrs {
			if !strings.Contains(msg, s) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// newVictimDir creates a throwaway directory (removed by the test framework
// if the helper under test leaves it behind) and a sibling log path for the
// retention-message assertions.
func newVictimDir(t *testing.T) (dir, logPath string) {
	t.Helper()
	dir = t.TempDir()
	return dir, filepath.Join(dir, "formae-test.log")
}

func dirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestCleanupTempDir(t *testing.T) {
	t.Run("passing removes the directory and logs cleanup", func(t *testing.T) {
		t.Setenv("FORMAE_TEST_KEEP_TEMP", "")
		dir, logPath := newVictimDir(t)
		r := &fakeReporter{t: t}

		cleanupTempDir(r, dir, logPath, false)

		if dirExists(dir) {
			t.Errorf("expected temp directory %s to be removed", dir)
		}
		if len(r.errors) != 0 {
			t.Errorf("expected no reported errors, got %v", r.errors)
		}
		if !r.logContaining("Cleaning up", dir) {
			t.Errorf("expected a cleaning-up message naming %s, got %v", dir, r.logs)
		}
	})

	t.Run("failed retains the directory and reports both paths", func(t *testing.T) {
		t.Setenv("FORMAE_TEST_KEEP_TEMP", "")
		dir, logPath := newVictimDir(t)
		r := &fakeReporter{t: t}

		cleanupTempDir(r, dir, logPath, true)

		if !dirExists(dir) {
			t.Errorf("expected temp directory %s to be retained", dir)
		}
		if !r.logContaining(dir, logPath) {
			t.Errorf("expected a retention message naming both %s and %s, got %v", dir, logPath, r.logs)
		}
		if len(r.errors) != 0 {
			t.Errorf("expected no reported errors, got %v", r.errors)
		}
	})

	t.Run("passing with FORMAE_TEST_KEEP_TEMP=1 retains the directory", func(t *testing.T) {
		t.Setenv("FORMAE_TEST_KEEP_TEMP", "1")
		dir, logPath := newVictimDir(t)
		r := &fakeReporter{t: t}

		cleanupTempDir(r, dir, logPath, false)

		if !dirExists(dir) {
			t.Errorf("expected temp directory %s to be retained", dir)
		}
	})

	t.Run("passing with malformed FORMAE_TEST_KEEP_TEMP removes the directory and reports the ignored value", func(t *testing.T) {
		t.Setenv("FORMAE_TEST_KEEP_TEMP", "yes")
		dir, logPath := newVictimDir(t)
		r := &fakeReporter{t: t}

		cleanupTempDir(r, dir, logPath, false)

		if dirExists(dir) {
			t.Errorf("expected temp directory %s to be removed", dir)
		}
		if !r.logContaining("FORMAE_TEST_KEEP_TEMP", "yes") {
			t.Errorf("expected the ignored-value message to name FORMAE_TEST_KEEP_TEMP and the value, got %v", r.logs)
		}
	})

	t.Run("passing with an unremovable directory reports the failure without failing the test", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		t.Setenv("FORMAE_TEST_KEEP_TEMP", "")

		parent := t.TempDir()
		victim := filepath.Join(parent, "victim")
		if err := os.Mkdir(victim, 0o755); err != nil {
			t.Fatalf("failed to create victim directory: %v", err)
		}
		logPath := filepath.Join(victim, "formae-test.log")

		if err := os.Chmod(parent, 0o500); err != nil {
			t.Fatalf("failed to chmod parent read-only: %v", err)
		}
		// Restore the parent's mode before t.TempDir()'s own teardown runs, so
		// that teardown doesn't report a second, unrelated removal failure.
		t.Cleanup(func() {
			if err := os.Chmod(parent, 0o755); err != nil {
				t.Logf("failed to restore parent permissions: %v", err)
			}
		})

		r := &fakeReporter{t: t}
		cleanupTempDir(r, victim, logPath, false)

		if !dirExists(victim) {
			t.Errorf("expected the unremovable directory %s to remain after a failed removal", victim)
		}
		if !r.logContaining("Failed to remove", victim) {
			t.Errorf("expected a removal-failure message naming %s, got %v", victim, r.logs)
		}
		if len(r.errors) != 0 {
			t.Errorf("a teardown removal failure must be logged, not reported as a test error: %v", r.errors)
		}
	})
}

func TestKeepTempDirRequested(t *testing.T) {
	tests := []struct {
		name           string
		env            string
		want           bool
		wantIgnoredMsg bool
	}{
		{name: "unset", env: "", want: false},
		{name: "1", env: "1", want: true},
		{name: "true", env: "true", want: true},
		{name: "TRUE", env: "TRUE", want: true},
		{name: "padded true", env: " true ", want: true},
		{name: "0", env: "0", want: false},
		{name: "false", env: "false", want: false},
		{name: "malformed", env: "yes", want: false, wantIgnoredMsg: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv cannot unset a variable, but setting the empty string
			// reproduces the unset case: TrimSpace("") == "" is handled before
			// strconv.ParseBool ever sees it. Every case sets the variable
			// explicitly, so an ambient FORMAE_TEST_KEEP_TEMP in the developer's
			// environment cannot leak into a case that didn't request a value.
			t.Setenv("FORMAE_TEST_KEEP_TEMP", tt.env)
			r := &fakeReporter{t: t}

			got := keepTempDirRequested(r)

			if got != tt.want {
				t.Errorf("keepTempDirRequested() = %v, want %v", got, tt.want)
			}
			if tt.wantIgnoredMsg && !r.logContaining("FORMAE_TEST_KEEP_TEMP", tt.env) {
				t.Errorf("expected an ignored-value message naming FORMAE_TEST_KEEP_TEMP and %q, got %v", tt.env, r.logs)
			}
			if !tt.wantIgnoredMsg && len(r.logs) != 0 {
				t.Errorf("expected no log messages for a well-formed value, got %v", r.logs)
			}
		})
	}
}

func TestSanitizeForPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "path separator replaced", input: "a/b", want: "a_b"},
		{name: "double colon collapsed to a single underscore", input: "a::b", want: "a_b"},
		{name: "leading and trailing underscores trimmed", input: "::TestFoo::", want: "TestFoo"},
		{name: "realistic test name", input: "TestCRUD/AWS::s3-bucket", want: "TestCRUD_AWS_s3-bucket"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeForPath(tt.input); got != tt.want {
				t.Errorf("sanitizeForPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	t.Run("caps at 40 runes", func(t *testing.T) {
		input := strings.Repeat("a", 50)
		got := sanitizeForPath(input)
		if len(got) != 40 {
			t.Errorf("sanitizeForPath(50 chars) length = %d, want 40", len(got))
		}
		if got != strings.Repeat("a", 40) {
			t.Errorf("sanitizeForPath(50 chars) = %q, want the first 40 runes", got)
		}
	})

	t.Run("result is a valid os.MkdirTemp pattern component", func(t *testing.T) {
		sanitized := sanitizeForPath("TestCRUD/AWS::s3-bucket")
		dir, err := os.MkdirTemp(t.TempDir(), fmt.Sprintf("formae-test-%s-*", sanitized))
		if err != nil {
			t.Fatalf("os.MkdirTemp with sanitized pattern %q failed: %v", sanitized, err)
		}
		if !dirExists(dir) {
			t.Errorf("expected MkdirTemp to create %s", dir)
		}
	})
}
