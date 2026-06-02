// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pklrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Env keys driving the fake pkl binary (see TestHelperProcess). They are passed
// to the child via the parent's environment (t.Setenv), so no shell is involved
// and the test is OS-independent.
const (
	envHelper  = "PKLRUN_HELPER_PROCESS" // "1" activates the fake-pkl behavior
	envCounter = "PKLRUN_HELPER_COUNTER" // file to append one byte per invocation
	envExit    = "PKLRUN_HELPER_EXIT"    // exit code (default 0)
	envStderr  = "PKLRUN_HELPER_STDERR"  // text to emit on stderr
)

// fakePklCommand returns a WithPklCommand value that re-invokes this test binary
// as a stand-in for the pkl CLI. Behavior is controlled by the env vars above,
// which the child process inherits — no shell script, works on any OS.
func fakePklCommand() []string {
	return []string{os.Args[0], "-test.run=TestHelperProcess", "--"}
}

// useFakePkl configures the fake pkl for one test via the inherited environment.
func useFakePkl(t *testing.T, counterPath string, exitCode int, stderr string) {
	t.Helper()
	t.Setenv(envHelper, "1")
	t.Setenv(envCounter, counterPath)
	t.Setenv(envExit, strconv.Itoa(exitCode))
	t.Setenv(envStderr, stderr)
}

// TestHelperProcess is not a real test: it is the fake `pkl` executable that
// fakePklCommand re-invokes. It is inert during a normal test run and only acts
// when PKLRUN_HELPER_PROCESS=1, then exits the process. On `project resolve
// <dir>` with a zero exit code it creates <dir>/PklProject.deps.json, mirroring
// what real `pkl project resolve` produces.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(envHelper) != "1" {
		return
	}

	if counter := os.Getenv(envCounter); counter != "" {
		if f, err := os.OpenFile(counter, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString("x")
			_ = f.Close()
		}
	}

	code := 0
	if e := os.Getenv(envExit); e != "" {
		code, _ = strconv.Atoi(e)
	}

	// Args after "--": the pkl subcommand, e.g. "project resolve <dir>".
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if code == 0 && len(args) >= 3 && args[0] == "project" && args[1] == "resolve" {
		_ = os.WriteFile(filepath.Join(args[2], depsFile), []byte("{}"), 0o644)
	}

	if s := os.Getenv(envStderr); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	os.Exit(code)
}

func invocations(t *testing.T, counterPath string) int {
	t.Helper()
	data, err := os.ReadFile(counterPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return strings.Count(string(data), "x")
}

func TestEnsureProjectResolved_MissingDeps_Resolves(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, projectFile), []byte("amends \"pkl:Project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "calls")
	useFakePkl(t, counter, 0, "")

	if err := ensureProjectResolved(dir, fakePklCommand()); err != nil {
		t.Fatalf("ensureProjectResolved: %v", err)
	}

	if n := invocations(t, counter); n != 1 {
		t.Fatalf("expected 1 resolve invocation, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, depsFile)); err != nil {
		t.Fatalf("expected %s to be created: %v", depsFile, err)
	}
}

func TestEnsureProjectResolved_PresentDeps_Skips(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, depsFile), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "calls")
	useFakePkl(t, counter, 0, "")

	if err := ensureProjectResolved(dir, fakePklCommand()); err != nil {
		t.Fatalf("ensureProjectResolved: %v", err)
	}

	if n := invocations(t, counter); n != 0 {
		t.Fatalf("expected 0 resolve invocations when %s present, got %d", depsFile, n)
	}
}

func TestEnsureProjectResolved_ResolveFailure_SurfacesOutput(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(t.TempDir(), "calls")
	useFakePkl(t, counter, 1, "boom-malformed-project")

	err := ensureProjectResolved(dir, fakePklCommand())
	if err == nil {
		t.Fatal("expected error on resolve failure, got nil")
	}
	if !strings.Contains(err.Error(), "boom-malformed-project") {
		t.Fatalf("expected combined output in error, got: %v", err)
	}
}

func TestBundledPklCommand(t *testing.T) {
	// No sibling pkl → nil.
	emptyDir := t.TempDir()
	if got := BundledPklCommand(filepath.Join(emptyDir, "formae")); got != nil {
		t.Fatalf("expected nil when no sibling pkl, got %v", got)
	}

	// Sibling pkl present → [<dir>/pkl].
	dir := t.TempDir()
	pklPath := filepath.Join(dir, "pkl")
	if err := os.WriteFile(pklPath, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	got := BundledPklCommand(filepath.Join(dir, "formae"))
	if len(got) != 1 || got[0] != pklPath {
		t.Fatalf("expected [%s], got %v", pklPath, got)
	}

	// Empty exe path → nil.
	if got := BundledPklCommand(""); got != nil {
		t.Fatalf("expected nil for empty exe path, got %v", got)
	}
}
