// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package cli_test

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

// buildBinary compiles fcfg into a temp dir and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fcfg")
	// The repo root is four levels up from this test file
	// (cmd/formae-config/internal/cli -> repo root).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Join(wd, "..", "..", "..", "..")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/formae-config")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// run executes fcfg with the given args under cfgDir and returns stdout, stderr, exit code.
func run(t *testing.T, bin, cfgDir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "FORMAE_CONFIG_DIR="+cfgDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("unexpected exec error: %v", err)
	}
	return stdout.String(), stderr.String(), code
}

func TestFullLifecycle(t *testing.T) {
	bin := buildBinary(t)
	cfgDir := t.TempDir()

	// Pre-seed a regular config file.
	if err := os.WriteFile(filepath.Join(cfgDir, "formae.conf.pkl"), []byte("default-content\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// `current` before init -> exit 3.
	_, _, code := run(t, bin, cfgDir, "current")
	if code != 3 {
		t.Errorf("current pre-init: code=%d, want 3", code)
	}

	// init --yes -> exit 0.
	_, _, code = run(t, bin, cfgDir, "init", "--yes")
	if code != 0 {
		t.Errorf("init: code=%d", code)
	}

	// init again -> "already initialized", exit 0.
	out, _, code := run(t, bin, cfgDir, "init", "--yes")
	if code != 0 || !strings.Contains(out, "already initialized") {
		t.Errorf("init idempotent: code=%d, out=%q", code, out)
	}

	// current -> "default".
	out, _, code = run(t, bin, cfgDir, "current")
	if code != 0 || strings.TrimSpace(out) != "default" {
		t.Errorf("current: code=%d, out=%q", code, out)
	}

	// Plant a second profile by hand and `use` it.
	if err := os.WriteFile(filepath.Join(cfgDir, "profiles", "prod.pkl"), []byte("prod-content\n"), 0o644); err != nil {
		t.Fatalf("plant prod: %v", err)
	}
	_, _, code = run(t, bin, cfgDir, "use", "prod")
	if code != 0 {
		t.Errorf("use prod: code=%d", code)
	}
	out, _, _ = run(t, bin, cfgDir, "current")
	if strings.TrimSpace(out) != "prod" {
		t.Errorf("after use prod, current=%q", out)
	}

	// `use` of unknown profile -> exit 1.
	_, _, code = run(t, bin, cfgDir, "use", "missing")
	if code != 1 {
		t.Errorf("use missing: code=%d, want 1", code)
	}

	// `save` to a new name (default content), then `list --json`.
	_, _, code = run(t, bin, cfgDir, "save", "snapshot")
	if code != 0 {
		t.Errorf("save: code=%d", code)
	}
	jsonOut, _, code := run(t, bin, cfgDir, "list", "--json")
	if code != 0 {
		t.Errorf("list --json: code=%d", code)
	}
	var listed struct {
		Active   *string  `json:"active"`
		Profiles []string `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &listed); err != nil {
		t.Fatalf("decode list json: %v\n%s", err, jsonOut)
	}
	if listed.Active == nil || *listed.Active != "prod" {
		t.Errorf("list active = %v, want prod", listed.Active)
	}
	want := []string{"default", "prod", "snapshot"}
	if strings.Join(listed.Profiles, ",") != strings.Join(want, ",") {
		t.Errorf("list profiles = %v, want %v", listed.Profiles, want)
	}

	// `save` over an existing name without --force -> exit 1.
	_, _, code = run(t, bin, cfgDir, "save", "snapshot")
	if code != 1 {
		t.Errorf("save existing: code=%d, want 1", code)
	}

	// `delete` active -> exit 1.
	_, _, code = run(t, bin, cfgDir, "delete", "prod")
	if code != 1 {
		t.Errorf("delete active: code=%d, want 1", code)
	}

	// Switch off prod, then delete it.
	_, _, _ = run(t, bin, cfgDir, "use", "default")
	_, _, code = run(t, bin, cfgDir, "delete", "prod")
	if code != 0 {
		t.Errorf("delete non-active: code=%d", code)
	}
}
