// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectInit(t *testing.T) {
	bin := FormaeBinary(t)

	// No agent needed — this is a CLI-only test.
	cli := NewFormaeCLI(bin, "", 0)

	// Step 1: Create a temp directory for the new project.
	dir := t.TempDir()

	// Step 2: Run project init with PKL schema and aws + azure plugins.
	cli.ProjectInit(t, dir, "aws", "azure")

	// Step 3: Verify PklProject file exists.
	pklProjectPath := filepath.Join(dir, "PklProject")
	data, err := os.ReadFile(pklProjectPath)
	if err != nil {
		t.Fatalf("PklProject not found at %s: %v", pklProjectPath, err)
	}
	content := string(data)
	t.Logf("PklProject content:\n%s", content)

	// Step 4: Assert PklProject contents.
	if !strings.Contains(content, `amends "pkl:Project"`) {
		t.Error("PklProject does not contain 'amends \"pkl:Project\"'")
	}
	if !strings.Contains(content, "dependencies") {
		t.Error("PklProject does not contain dependencies block")
	}
	// The formae core dependency may appear as ["formae"] or with a
	// different key depending on plugin resolution. Check for both the key
	// name and the hub URI pattern.
	if !strings.Contains(content, "formae") {
		t.Error("PklProject does not reference formae at all")
	}
	if !strings.Contains(content, "aws") {
		t.Error("PklProject does not reference aws")
	}
	if !strings.Contains(content, "azure") {
		t.Error("PklProject does not reference azure")
	}

	// Step 5: Verify main.pkl exists.
	mainPklPath := filepath.Join(dir, "main.pkl")
	if _, err := os.Stat(mainPklPath); err != nil {
		t.Errorf("main.pkl not found at %s: %v", mainPklPath, err)
	}
}
