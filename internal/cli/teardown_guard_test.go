// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyPackages are the deleted rendering packages that must never be re-imported.
var legacyPackages = []string{
	"internal/cli/renderer",
	"internal/cli/display",
	"internal/cli/prompter",
}

// isLegacyImport reports whether a raw import path (as it appears in source,
// including surrounding quotes) contains any of the legacy package substrings.
func isLegacyImport(importPath string) bool {
	clean := strings.Trim(importPath, `"`)
	for _, pkg := range legacyPackages {
		if strings.Contains(clean, pkg) {
			return true
		}
	}
	return false
}

// findLegacyImports walks root and returns a map of file -> []offending-import-path
// for every .go file that imports a legacy package.
func findLegacyImports(root string) (map[string][]string, error) {
	findings := map[string][]string{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			// Skip unparseable files (generated, bad syntax) — don't fail the walk.
			return nil
		}
		for _, imp := range f.Imports {
			if isLegacyImport(imp.Path.Value) {
				findings[path] = append(findings[path], imp.Path.Value)
			}
		}
		return nil
	})
	return findings, err
}

// repoRoot walks up from dir until it finds a go.mod file.
func repoRoot(dir string) (string, error) {
	cur := dir
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", os.ErrNotExist
		}
		cur = parent
	}
}

// TestDetectionHelperFlags proves the isLegacyImport helper has teeth by
// feeding it known-bad synthetic import paths and asserting they are flagged.
func TestDetectionHelperFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		imp     string
		wantHit bool
	}{
		{`"github.com/platform-engineering-labs/formae/internal/cli/renderer"`, true},
		{`"github.com/platform-engineering-labs/formae/internal/cli/display"`, true},
		{`"github.com/platform-engineering-labs/formae/internal/cli/prompter"`, true},
		{`"github.com/platform-engineering-labs/formae/internal/cli/printer"`, false},
		{`"github.com/platform-engineering-labs/formae/internal/cli/tui"`, false},
		{`"fmt"`, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.imp, func(t *testing.T) {
			t.Parallel()
			got := isLegacyImport(tc.imp)
			assert.Equal(t, tc.wantHit, got, "isLegacyImport(%q)", tc.imp)
		})
	}
}

// TestNoLegacyRenderingImports walks the entire internal/ tree and fails if
// any .go file imports the deleted renderer, display, or prompter packages.
func TestNoLegacyRenderingImports(t *testing.T) {
	t.Parallel()

	// CWD when `go test` runs a package is the package directory itself.
	// internal/cli/ is two levels below the repo root (repo/internal/cli).
	cwd, err := os.Getwd()
	require.NoError(t, err, "could not determine working directory")

	root, err := repoRoot(cwd)
	require.NoError(t, err, "could not locate repo root (go.mod not found)")

	internalDir := filepath.Join(root, "internal")
	_, statErr := os.Stat(internalDir)
	require.NoError(t, statErr, "internal/ dir not found under repo root %s", root)

	findings, err := findLegacyImports(internalDir)
	require.NoError(t, err, "error walking internal/ tree")

	if len(findings) == 0 {
		return // all clear
	}

	t.Errorf("found %d file(s) importing deleted legacy rendering packages — these packages were removed in the cli-visual-upgrade teardown and must not be re-imported:", len(findings))
	for file, imports := range findings {
		for _, imp := range imports {
			t.Errorf("  %s imports %s", file, imp)
		}
	}
}
