// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	// Find project root (walk up to go.mod)
	projectRoot := findProjectRoot()

	// Build formae binary once for all blackbox tests. The binary lives at
	// <tmpDir>/bin/formae so orbital's tree-root derivation
	// (filepath.Dir(filepath.Dir(binPath))) resolves to <tmpDir>, where we
	// then create an empty .ops/ directory. That satisfies orbital's
	// Ready() check (it only tests for the directory's existence) without
	// scaffolding a real signed orbital tree, so the agent's plugin manager
	// initializes cleanly at startup. Production builds installed via
	// setup.sh have a fully populated orbital tree at /opt/pel and follow
	// the same path naturally.
	tmpDir, err := os.MkdirTemp("", "formae-blackbox-*")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}

	formaeBinary = filepath.Join(tmpDir, "bin", "formae")
	if err := os.MkdirAll(filepath.Dir(formaeBinary), 0755); err != nil {
		os.RemoveAll(tmpDir)
		log.Fatalf("Failed to create binary dir: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", formaeBinary, "./cmd/formae")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		log.Fatalf("Failed to build formae binary: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, ".ops"), 0755); err != nil {
		os.RemoveAll(tmpDir)
		log.Fatalf("Failed to create orbital tree marker: %v", err)
	}

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("could not find project root (no go.mod found)")
		}
		dir = parent
	}
}
