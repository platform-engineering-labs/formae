// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/orbital/mgr"
	"github.com/platform-engineering-labs/orbital/opm/records"
	"github.com/platform-engineering-labs/orbital/opm/security"
	"github.com/platform-engineering-labs/orbital/opm/tree"
	"github.com/platform-engineering-labs/orbital/ops"
	"github.com/platform-engineering-labs/orbital/platform"
)

const hubURL = "https://hub.platform.engineering/binaries/repo.json"

// EnsureFormaeBinary returns the path to a formae binary, downloading it via
// orbital if the FORMAE_BINARY environment variable is not already set. The
// returned cleanup function removes any temporary directory that was created.
func EnsureFormaeBinary(t *testing.T) (binaryPath string, cleanup func()) {
	t.Helper()

	if p := os.Getenv("FORMAE_BINARY"); p != "" {
		if _, err := os.Stat(p); err == nil {
			t.Logf("using existing formae binary: %s", p)
			return p, func() {}
		}
		t.Logf("FORMAE_BINARY set to %s but file does not exist, downloading instead", p)
	}

	tmpDir, err := os.MkdirTemp("", "formae-conformance-*")
	if err != nil {
		t.Fatalf("failed to create temp dir for formae binary: %v", err)
	}

	cleanup = func() { os.RemoveAll(tmpDir) }

	orb := newOrbitalManager(t, tmpDir)

	if !orb.Ready() {
		if _, err := orb.Initialize(); err != nil {
			cleanup()
			t.Fatalf("failed to initialize orbital: %v", err)
		}
	}

	if err := orb.Refresh(); err != nil {
		cleanup()
		t.Fatalf("failed to refresh orbital repository: %v", err)
	}

	available, err := orb.AvailableFor("formae")
	if err != nil {
		cleanup()
		t.Fatalf("failed to query available formae versions: %v", err)
	}

	candidate := resolveCandidate(t, available, cleanup)

	t.Logf("installing formae %s", candidate.Version.Short())
	if err := orb.Install(candidate.Id().String()); err != nil {
		cleanup()
		t.Fatalf("failed to install formae: %v", err)
	}

	binPath := findBinary(t, tmpDir, cleanup)

	version := extractVersion(t, binPath)
	t.Logf("formae binary version: %s", version)

	os.Setenv("FORMAE_VERSION", version)
	os.Setenv("FORMAE_BINARY", binPath)

	return binPath, cleanup
}

// ResolvePKLDependencies updates PklProject files in the given directories to
// reference the specified formae version, removes cached dependency resolutions,
// and runs `pkl project resolve`. The returned function restores all modified
// PklProject files to their original content.
func ResolvePKLDependencies(t *testing.T, version string, projectDirs ...string) (restore func()) {
	t.Helper()

	if version == "" {
		version = os.Getenv("FORMAE_VERSION")
	}

	if version == "" {
		t.Log("no formae version specified and FORMAE_VERSION not set, skipping PKL dependency resolution")
		return func() {}
	}

	type backup struct {
		path    string
		content []byte
	}
	var backups []backup

	for _, dir := range projectDirs {
		pklPath := filepath.Join(dir, "PklProject")

		original, err := os.ReadFile(pklPath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", pklPath, err)
		}

		backups = append(backups, backup{path: pklPath, content: original})

		// Replace formae/formae@X.Y.Z with the correct version
		re := regexp.MustCompile(`formae/formae@[\d]+\.[\d]+\.[\d]+[^"]*"`)
		updated := re.ReplaceAllString(string(original), fmt.Sprintf(`formae/formae@%s"`, version))

		if err := os.WriteFile(pklPath, []byte(updated), 0644); err != nil {
			t.Fatalf("failed to write updated %s: %v", pklPath, err)
		}

		t.Logf("updated %s to formae version %s", pklPath, version)

		// Remove cached deps to force re-resolution
		depsPath := filepath.Join(dir, "PklProject.deps.json")
		if _, err := os.Stat(depsPath); err == nil {
			if err := os.Remove(depsPath); err != nil {
				t.Fatalf("failed to remove %s: %v", depsPath, err)
			}
			t.Logf("removed %s", depsPath)
		}

		// Run pkl project resolve
		cmd := exec.Command("pkl", "project", "resolve", dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("pkl project resolve failed for %s: %v", dir, err)
		}

		t.Logf("resolved PKL dependencies for %s", dir)
	}

	return func() {
		for _, b := range backups {
			if err := os.WriteFile(b.path, b.content, 0644); err != nil {
				t.Logf("warning: failed to restore %s: %v", b.path, err)
			}
		}
	}
}

// newOrbitalManager creates an orbital Manager pointed at the hub repository.
func newOrbitalManager(t *testing.T, rootPath string) *mgr.Manager {
	t.Helper()

	u, err := url.Parse(hubURL)
	if err != nil {
		t.Fatalf("failed to parse hub URL: %v", err)
	}

	orb, err := mgr.New(slog.Default(), rootPath, &tree.Config{
		OS:       platform.Current().OS,
		Arch:     platform.Current().Arch,
		Security: security.Default,
		Repositories: []ops.Repository{
			{
				Uri:      *u,
				Priority: 0,
				Enabled:  true,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create orbital manager: %v", err)
	}

	return orb
}

// resolveCandidate picks the formae package to install: a specific version if
// FORMAE_VERSION is set, or the latest available version otherwise.
func resolveCandidate(t *testing.T, available *records.Status, cleanup func()) *records.Package {
	t.Helper()

	if version := os.Getenv("FORMAE_VERSION"); version != "" {
		v := &ops.Version{}
		if err := v.Parse(version); err != nil {
			cleanup()
			t.Fatalf("failed to parse FORMAE_VERSION %q: %v", version, err)
		}

		found, candidate := available.HasVersion(v)
		if !found {
			cleanup()
			t.Fatalf("formae version %s not found in repository", version)
		}

		return candidate
	}

	// For a fresh install HasUpdate may return false because nothing is
	// installed yet. Fall back to picking the first entry from the sorted
	// Available list (highest version).
	if hasUpdate, candidate := available.HasUpdate(); hasUpdate {
		return candidate
	}

	available.Sort()
	if len(available.Available) > 0 {
		return available.Available[0]
	}

	cleanup()
	t.Fatalf("no formae versions available in repository")
	return nil // unreachable
}

// findBinary locates the formae binary inside the orbital installation tree.
func findBinary(t *testing.T, tmpDir string, cleanup func()) string {
	t.Helper()

	candidates := []string{
		filepath.Join(tmpDir, "bin", "formae"),
		filepath.Join(tmpDir, "formae", "bin", "formae"),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	cleanup()
	t.Fatalf("formae binary not found in temp dir %s (tried %s)", tmpDir, strings.Join(candidates, ", "))
	return "" // unreachable
}

// extractVersion runs `formae --version` and returns the version string.
func extractVersion(t *testing.T, binPath string) string {
	t.Helper()

	out, err := exec.Command(binPath, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to get formae version: %v\noutput: %s", err, string(out))
	}

	// Output is typically "formae version X.Y.Z" or just "X.Y.Z"
	output := strings.TrimSpace(string(out))
	parts := strings.Fields(output)
	if len(parts) == 0 {
		t.Fatalf("empty version output from formae binary")
	}

	// Return the last field which should be the version number
	return parts[len(parts)-1]
}
