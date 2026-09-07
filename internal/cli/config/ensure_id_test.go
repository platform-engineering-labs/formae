// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh machine has no ~/.pel/formae yet; ensuring an id must create the
// directory rather than fail on the missing parent.
func TestEnsureId_CreatesTheDataDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Config.EnsureClientID(); err != nil {
		t.Fatalf("EnsureClientID on a fresh home: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".pel/formae", "cli_client_id"))
	if err != nil {
		t.Fatalf("id file not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("id file is empty")
	}
}
