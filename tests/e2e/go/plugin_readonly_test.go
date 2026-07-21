// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newReadEnv builds a process environment for a CLI-local plugin/update
// command: a hermetic HOME/XDG config dir holding a minimal config (so
// artifacts.repositories resolve to the defaults) plus FORMAE_PEL_ROOT
// pointed at the given orbital tree. Later env entries win over os.Environ.
func newReadEnv(t *testing.T, treeRoot string) []string {
	t.Helper()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "formae")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfg := filepath.Join(cfgDir, "formae.conf.pkl")
	if err := os.WriteFile(cfg, []byte("amends \"formae:/Config.pkl\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"FORMAE_PEL_ROOT="+treeRoot,
	)
}

// runFormae runs the formae binary with the given environment. No agent is
// involved — plugin list/search/info and update list are CLI-local orbital
// operations.
func runFormae(t *testing.T, env []string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(FormaeBinary(t), args...)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// cloneWarmTree finds an already-initialized/warm orbital tree, copies it
// into t.TempDir() so the copy can be mutated (chmod/chown), and returns the
// copy path. Skips when no usable warm tree exists.
func cloneWarmTree(t *testing.T) string {
	t.Helper()
	src := resolveWarmTreeSource(t)
	if src == "" {
		t.Skip("no usable warm orbital tree (set FORMAE_PEL_ROOT to a warm tree or install formae)")
	}
	dst := filepath.Join(t.TempDir(), "tree")
	if out, err := exec.Command("cp", "-a", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("clone warm tree %s -> %s: %v\n%s", src, dst, err, out)
	}
	return dst
}

// resolveWarmTreeSource returns the first candidate tree that `plugin list`
// succeeds against, or "" if none. Order: FORMAE_PEL_ROOT, the binary-derived
// root (dist/e2e under make test-e2e), then /opt/pel.
func resolveWarmTreeSource(t *testing.T) string {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range []string{
		os.Getenv("FORMAE_PEL_ROOT"),
		filepath.Dir(filepath.Dir(FormaeBinary(t))),
		"/opt/pel",
	} {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		env := newReadEnv(t, c)
		if _, err := runFormae(t, env, "plugin", "list", "--output-consumer", "machine", "--output-schema", "json"); err == nil {
			return c
		}
	}
	return ""
}

// firstInstalledPlugin returns the name of the first installed plugin, or ""
// if none / unparseable.
func firstInstalledPlugin(t *testing.T, env []string) string {
	t.Helper()
	out, err := runFormae(t, env, "plugin", "list", "--output-consumer", "machine", "--output-schema", "json")
	if err != nil {
		return ""
	}
	var resp struct {
		Plugins []struct {
			Name string `json:"Name"`
		} `json:"Plugins"`
	}
	if json.Unmarshal(out, &resp) != nil || len(resp.Plugins) == 0 {
		return ""
	}
	return resp.Plugins[0].Name
}

// readCommandCases returns the sudo-less read commands to assert, including a
// `plugin info` case only when an installed plugin name is available.
func readCommandCases(pkg string) [][]string {
	cases := [][]string{
		{"plugin", "list", "--output-consumer", "machine", "--output-schema", "json"},
		{"plugin", "search", "--output-consumer", "machine", "--output-schema", "json"},
		{"update", "list"},
	}
	if pkg != "" {
		cases = append(cases, []string{"plugin", "info", pkg, "--output-consumer", "machine", "--output-schema", "json"})
	}
	return cases
}

// TestPluginReadCommands_ReadOnlyTree proves the non-destructive plugin/update
// read commands work against a NON-WRITABLE tree — no refresh, no elevation. A
// regression that re-introduced a refresh on a read path fails here.
func TestPluginReadCommands_ReadOnlyTree(t *testing.T) {
	tree := cloneWarmTree(t)
	env := newReadEnv(t, tree)
	pkg := firstInstalledPlugin(t, env)

	if out, err := exec.Command("chmod", "-R", "a-w", tree).CombinedOutput(); err != nil {
		t.Fatalf("chmod read-only: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", tree).Run() })

	for _, args := range readCommandCases(pkg) {
		if out, err := runFormae(t, env, args...); err != nil {
			t.Errorf("`formae %s` failed against a read-only tree (should be sudo-less, no refresh): %v\n%s",
				strings.Join(args, " "), err, out)
		}
	}
}

// TestPluginReadCommands_PrivilegedTree proves the read commands still run
// sudo-less as a non-root user against a ROOT-OWNED tree — the production
// scenario (default install is root-owned /opt/pel). Skipped without
// passwordless sudo so local runs never hang on a prompt.
func TestPluginReadCommands_PrivilegedTree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; the privileged-tree elevation path is not exercised")
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("passwordless sudo unavailable; cannot set up a root-owned tree")
	}
	tree := cloneWarmTree(t)
	env := newReadEnv(t, tree)
	pkg := firstInstalledPlugin(t, env)

	if out, err := exec.Command("sudo", "-n", "chown", "-R", "root:root", tree).CombinedOutput(); err != nil {
		t.Fatalf("chown root: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("sudo", "-n", "chown", "-R", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), tree).Run()
	})

	for _, args := range readCommandCases(pkg) {
		if out, err := runFormae(t, env, args...); err != nil {
			t.Errorf("`formae %s` needed elevation against a root-owned tree (should be sudo-less): %v\n%s",
				strings.Join(args, " "), err, out)
		}
	}
}
