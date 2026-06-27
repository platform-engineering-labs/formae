// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runProfile executes the formae binary with FORMAE_CONFIG_DIR set to cfgDir
// and captures stdout, stderr, and exit code. Unlike FormaeCLI.run it never
// fails the test on a non-zero exit so callers can assert on failure cases.
func runProfile(t *testing.T, bin, cfgDir string, args ...string) (string, string, int) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), "FORMAE_CONFIG_DIR="+cfgDir)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec: %v", err)
	}
	return stdout.String(), stderr.String(), code
}

// closedPortE2E allocates a free TCP port, immediately closes the listener,
// and returns a host string ("http://127.0.0.1") and port. Nothing will be
// listening there by the time the test uses the values.
func closedPortE2E(t *testing.T) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("closedPortE2E: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	return "http://" + addr.IP.String(), addr.Port
}

// writeProfilePKL writes a minimal formae PKL profile that points the CLI at
// the given url and port. No agent block or plugins — just the cli.api section.
func writeProfilePKL(t *testing.T, path, url string, port int) {
	t.Helper()
	content := fmt.Sprintf(`amends "formae:/Config.pkl"

cli {
    api {
        url  = %q
        port = %d
    }
}
`, url, port)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeProfilePKL %s: %v", path, err)
	}
}

// setActiveProfile writes the active-profile pointer file.
func setActiveProfile(t *testing.T, cfgDir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfgDir, "active"), []byte(name), 0o644); err != nil {
		t.Fatalf("setActiveProfile: %v", err)
	}
}

// TestProfileRouting_E2E proves that --profile and the active pointer route the
// CLI to the correct agent endpoint, and that --profile is a one-shot override
// that does not mutate the stored active pointer.
//
// CMD: "status agent" — issues GET /api/v1/stats, the lightest agent-connecting
// command. On a good endpoint it exits 0 with real agent output. On a bad
// endpoint the CLI exits non-zero with "agent is not running" in stderr.
func TestProfileRouting_E2E(t *testing.T) {
	bin := FormaeBinary(t)
	agent := StartAgent(t, bin)
	agentPort := agent.Port()

	cfgDir := t.TempDir()
	profDir := filepath.Join(cfgDir, "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatalf("mkdir profiles: %v", err)
	}

	// live.pkl → real running agent
	writeProfilePKL(t, filepath.Join(profDir, "live.pkl"), "http://localhost", agentPort)

	// dead.pkl → a closed port (nothing listening)
	deadHost, deadPort := closedPortE2E(t)
	writeProfilePKL(t, filepath.Join(profDir, "dead.pkl"), deadHost, deadPort)

	setActiveProfile(t, cfgDir, "live")

	t.Run("--profile live connects to the real agent", func(t *testing.T) {
		stdout, stderr, code := runProfile(t, bin, cfgDir, "status", "agent", "--profile", "live")
		if code != 0 {
			t.Fatalf("expected exit 0 for live profile, got %d\nstderr: %s\nstdout: %s", code, stderr, stdout)
		}
		if strings.TrimSpace(stdout) == "" {
			t.Errorf("expected non-empty stdout from live agent, got empty")
		}
	})

	t.Run("--profile dead fails with connection error", func(t *testing.T) {
		_, stderr, code := runProfile(t, bin, cfgDir, "status", "agent", "--profile", "dead")
		if code == 0 {
			t.Errorf("expected non-zero exit for dead profile, got 0")
		}
		if !strings.Contains(stderr, "agent is not running") {
			t.Errorf("expected 'agent is not running' in stderr, got: %q", stderr)
		}
	})

	t.Run("one-shot override does not mutate active pointer", func(t *testing.T) {
		// active=live; override once to dead — must fail.
		setActiveProfile(t, cfgDir, "live")
		_, _, code := runProfile(t, bin, cfgDir, "status", "agent", "--profile", "dead")
		if code == 0 {
			t.Errorf("expected non-zero exit when overriding to dead, got 0")
		}

		// Active pointer must still be "live".
		out, _, code := runProfile(t, bin, cfgDir, "profile", "current")
		if code != 0 {
			t.Fatalf("profile current: code=%d", code)
		}
		if got := strings.TrimSpace(out); got != "live" {
			t.Errorf("active after one-shot override = %q, want %q", got, "live")
		}

		// Subsequent no-flag call must route to live → real agent → exit 0.
		stdout, stderr, code := runProfile(t, bin, cfgDir, "status", "agent")
		if code != 0 {
			t.Fatalf("expected exit 0 after one-shot override restores live, got %d\nstderr: %s\nstdout: %s", code, stderr, stdout)
		}
	})
}
