// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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
		if len(r.errors) != 0 {
			t.Errorf("expected no reported errors, got %v", r.errors)
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
		if len(r.errors) != 0 {
			t.Errorf("expected no reported errors, got %v", r.errors)
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

	t.Run("caps at 40 runes for multi-byte input", func(t *testing.T) {
		// 60 sanitized runes spread over 90 input bytes: the cap counts runes,
		// and every multi-byte rune is replaced before the cap applies.
		input := strings.Repeat("aé", 30)
		got := sanitizeForPath(input)
		want := strings.Repeat("a_", 20)
		if got != want {
			t.Errorf("sanitizeForPath(%q) = %q, want %q", input, got, want)
		}
		if n := utf8.RuneCountInString(got); n != 40 {
			t.Errorf("sanitizeForPath(%q) length = %d runes, want 40", input, n)
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

// Env keys driving the retention child process (see TestRetentionChild). They
// are passed to the re-invoked test binary through its environment, so no shell
// is involved and the test is OS-independent.
const (
	envRetentionMode = "FORMAE_TEST_RETENTION_MODE"    // how the child ends: pass, error or fatal
	envRetentionDir  = "FORMAE_TEST_RETENTION_TEMPDIR" // directory handed to the harness as its temp dir
)

// Fragments of the messages cleanupTempDir emits, asserted against the child
// output.
const (
	cleanupLogFragment   = "Cleaning up temp directory"
	retentionLogFragment = "Retaining test temp directory"
)

// TestRetentionChild is not a real test: it is the child process re-invoked by
// TestTempDirRetention. It is inert during a normal test run and only acts when
// FORMAE_TEST_RETENTION_MODE is set, registering the harness diagnostics
// cleanup over the directory named by FORMAE_TEST_RETENTION_TEMPDIR and then
// ending the way the mode asks for.
func TestRetentionChild(t *testing.T) {
	mode := os.Getenv(envRetentionMode)
	if mode == "" {
		return
	}

	dir := os.Getenv(envRetentionDir)
	h := &TestHarness{t: t, tempDir: dir, logFile: filepath.Join(dir, "formae-test.log")}
	h.registerDiagnosticsCleanup()

	switch mode {
	case "pass":
	case "error":
		t.Errorf("deliberate failure")
	case "fatal":
		t.Fatalf("deliberate fatal failure")
	default:
		t.Fatalf("unknown retention mode %q", mode)
	}
}

// newRetentionVictimDir creates a directory holding an agent log file, standing
// in for the harness temp directory the child process keeps or removes. It
// lives inside t.TempDir(), so a retained directory is still cleaned up.
func newRetentionVictimDir(t *testing.T) (dir, logPath string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "formae-test-victim")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("failed to create victim directory: %v", err)
	}
	logPath = filepath.Join(dir, "formae-test.log")
	if err := os.WriteFile(logPath, []byte("agent log\n"), 0o644); err != nil {
		t.Fatalf("failed to write victim agent log: %v", err)
	}
	return dir, logPath
}

// runRetentionChild re-invokes this test binary for TestRetentionChild only,
// returning its combined output and exit error. keepTemp is always set
// explicitly so an ambient FORMAE_TEST_KEEP_TEMP cannot leak into the child.
func runRetentionChild(t *testing.T, mode, dir, keepTemp string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRetentionChild$", "-test.v")
	cmd.Env = append(os.Environ(),
		envRetentionMode+"="+mode,
		envRetentionDir+"="+dir,
		"FORMAE_TEST_KEEP_TEMP="+keepTemp,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTempDirRetention(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		keepTemp     string
		wantRetained bool
		wantSuccess  bool
		wantOutput   string // proof the child ran this mode rather than skipping or matching no test
	}{
		{
			name:        "passing test removes the temp directory",
			mode:        "pass",
			keepTemp:    "false",
			wantSuccess: true,
			wantOutput:  "--- PASS: TestRetentionChild",
		},
		{
			name:         "failing test retains the temp directory",
			mode:         "error",
			keepTemp:     "false",
			wantRetained: true,
			wantOutput:   "deliberate failure",
		},
		{
			name:         "fatal failure retains the temp directory",
			mode:         "fatal",
			keepTemp:     "false",
			wantRetained: true,
			wantOutput:   "deliberate fatal failure",
		},
		{
			name:         "passing test with FORMAE_TEST_KEEP_TEMP=1 retains the temp directory",
			mode:         "pass",
			keepTemp:     "1",
			wantRetained: true,
			wantSuccess:  true,
			wantOutput:   "--- PASS: TestRetentionChild",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, logPath := newRetentionVictimDir(t)

			out, err := runRetentionChild(t, tt.mode, dir, tt.keepTemp)

			if tt.wantSuccess && err != nil {
				t.Errorf("child exited with %v, want success\n%s", err, out)
			}
			if !tt.wantSuccess && err == nil {
				t.Errorf("child exited successfully, want a non-zero exit status\n%s", out)
			}
			if got := dirExists(dir); got != tt.wantRetained {
				t.Errorf("directory %s present = %v, want %v\n%s", dir, got, tt.wantRetained, out)
			}

			want := []string{cleanupLogFragment, dir, tt.wantOutput}
			if tt.wantRetained {
				want = []string{retentionLogFragment, dir, logPath, tt.wantOutput}
			}
			for _, s := range want {
				if !strings.Contains(out, s) {
					t.Errorf("child output does not contain %q\n%s", s, out)
				}
			}
		})
	}
}
