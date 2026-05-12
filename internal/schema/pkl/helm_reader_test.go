// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	pklgo "github.com/apple/pkl-go/pkl"
)

func TestHelmReaderOption_RegistersWhenOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on Windows")
	}

	tmp := t.TempDir()
	exe := filepath.Join(tmp, helmReaderBinaryName)
	mustExecutable(t, exe)

	resetHelmReaderCache(t)
	t.Setenv("PATH", tmp)

	opt := helmReaderOption()
	if opt == nil {
		t.Fatal("expected non-nil option when reader is on PATH")
	}

	// Apply the option to a fresh EvaluatorOptions and inspect the
	// registration. Confirms both that the reader scheme was wired up
	// and that the executable path matches the PATH hit.
	opts := &pklgo.EvaluatorOptions{}
	opt(opts)
	got, ok := opts.ExternalResourceReaders[helmReaderScheme]
	if !ok {
		t.Fatalf("expected reader registered under scheme %q, have %v",
			helmReaderScheme, opts.ExternalResourceReaders)
	}
	if got.Executable != exe {
		t.Fatalf("expected executable %q, got %q", exe, got.Executable)
	}
}

func TestHelmReaderOption_NilWhenNotOnPath(t *testing.T) {
	resetHelmReaderCache(t)
	t.Setenv("PATH", t.TempDir()) // empty dir — no binary

	if opt := helmReaderOption(); opt != nil {
		t.Fatal("expected nil option when reader is not reachable")
	}
}

// resetHelmReaderCache clears the process-wide sync.Once so each test
// can re-discover the reader against its own PATH. Restores the
// pre-test state on cleanup so other tests in the package aren't
// affected. Swaps the *sync.Once by pointer to avoid the copylocks vet
// check (sync.Once embeds a noCopy sentinel).
func resetHelmReaderCache(t *testing.T) {
	t.Helper()
	prevOnce := helmReaderOnce
	prevOpt := helmReaderOpt
	helmReaderOnce = &sync.Once{}
	helmReaderOpt = nil
	t.Cleanup(func() {
		helmReaderOnce = prevOnce
		helmReaderOpt = prevOpt
	})
}

// mustExecutable creates a 0755 file at path (with parents).
func mustExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
