// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// The lock is best effort: it makes concurrent initialization orderly, and must
// never become a new way to fail. A peer that outlives the wait — or a
// filesystem that cannot lock at all — leaves initialization exactly as it was
// before the lock existed, so such a machine is never worse off.
func TestInitializationProceedsWhenTheLockCannotBeTaken(t *testing.T) {
	prev := initLockWait
	initLockWait = 20 * time.Millisecond
	t.Cleanup(func() { initLockWait = prev })

	root := t.TempDir()

	// Hold the lock for longer than initialization is willing to wait.
	held := flock.New(filepath.Join(root, initLockFileName))
	locked, err := held.TryLock()
	if err != nil || !locked {
		t.Fatalf("could not hold the lock for the test: locked=%v err=%v", locked, err)
	}
	t.Cleanup(func() { _ = held.Unlock() })

	if _, err := New(root).Resolve(); err != nil {
		t.Fatalf("Resolve while the lock is held: %v", err)
	}

	active, err := New(root).Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != "default" {
		t.Fatalf("active = %q, want default", active)
	}
	if _, err := os.Stat(New(root).ProfilePath("default")); err != nil {
		t.Fatalf("default profile was not written: %v", err)
	}
}
