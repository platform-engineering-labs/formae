// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package profile_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildFormae(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "formae")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Join(wd, "..", "..", "..") // internal/cli/profile -> root
	c := exec.Command("go", "build", "-o", bin, "./cmd/formae")
	c.Dir = repoRoot
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin, cfgDir string, args ...string) (string, string, int) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), "FORMAE_CONFIG_DIR="+cfgDir, "FORMAE_PLUGIN_DIR="+t.TempDir())
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
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

func TestProfileLifecycle(t *testing.T) {
	bin := buildFormae(t)
	cfgDir := t.TempDir()

	// `current` on a truly clean dir: pure read, no bootstrap, reports no active profile.
	out, _, code := run(t, bin, cfgDir, "profile", "current")
	if code != 0 || strings.TrimSpace(out) != "no active profile yet" {
		t.Fatalf("current on clean dir: code=%d out=%q", code, out)
	}

	// `list` on a truly clean dir: pure read, no bootstrap, prints nothing.
	out, _, code = run(t, bin, cfgDir, "profile", "list")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("list on clean dir: code=%d out=%q", code, out)
	}

	// `create staging` bootstraps a default profile (ensureInitialized runs on
	// create), then creates profiles/staging.pkl. After this, active=default.
	if _, _, code = run(t, bin, cfgDir, "profile", "create", "staging"); code != 0 {
		t.Fatalf("create staging: code=%d", code)
	}
	jsonOut, _, code := run(t, bin, cfgDir, "profile", "list", "--json")
	if code != 0 {
		t.Fatalf("list --json: code=%d", code)
	}
	var listed struct {
		Active   *string  `json:"active"`
		Profiles []string `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &listed); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut)
	}
	if listed.Active == nil || *listed.Active != "default" {
		t.Errorf("active after create = %v, want default", listed.Active)
	}
	if strings.Join(listed.Profiles, ",") != "default,staging" {
		t.Errorf("profiles after create = %v", listed.Profiles)
	}

	// `use staging`, confirm `current`.
	if _, _, code = run(t, bin, cfgDir, "profile", "use", "staging"); code != 0 {
		t.Fatalf("use staging: code=%d", code)
	}
	out, _, _ = run(t, bin, cfgDir, "profile", "current")
	if strings.TrimSpace(out) != "staging" {
		t.Errorf("current after use = %q", out)
	}

	// `use` unknown -> nonzero.
	if _, _, code = run(t, bin, cfgDir, "profile", "use", "ghost"); code == 0 {
		t.Errorf("use ghost: expected nonzero exit")
	}

	// `delete` active -> nonzero; switch away then delete.
	if _, _, code = run(t, bin, cfgDir, "profile", "delete", "staging"); code == 0 {
		t.Errorf("delete active: expected nonzero exit")
	}
	if _, _, code = run(t, bin, cfgDir, "profile", "use", "default"); code != 0 {
		t.Fatalf("use default: code=%d", code)
	}
	if _, _, code = run(t, bin, cfgDir, "profile", "delete", "staging"); code != 0 {
		t.Errorf("delete non-active: code=%d", code)
	}
}

func TestCancelAcceptsProfileFlag(t *testing.T) {
	bin := buildFormae(t)
	// --help must list --profile on cancel (no agent needed).
	c := exec.Command(bin, "cancel", "--help")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("cancel --help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--profile") {
		t.Errorf("cancel --help missing --profile flag:\n%s", out)
	}
}
