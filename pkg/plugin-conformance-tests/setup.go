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

// hubRepoBase is the orbital repository; the channel is appended as a URL
// fragment (e.g. "#stable", "#dev") to select stable vs dev releases.
const hubRepoBase = "https://hub.platform.engineering/repos/platform.engineering/pel"

// EnsureFormaeBinary returns the path to a formae binary, downloading it via
// orbital if the FORMAE_BINARY environment variable is not already set. The
// returned cleanup function removes any temporary directory that was created.
func EnsureFormaeBinary(t *testing.T) (binaryPath string, cleanup func()) {
	t.Helper()

	if p := os.Getenv("FORMAE_BINARY"); p != "" {
		if _, err := os.Stat(p); err == nil {
			t.Logf("using existing formae binary: %s", p)
			version := extractVersion(t, p)
			t.Logf("formae binary version: %s", version)
			t.Setenv("FORMAE_VERSION", version)
			return p, func() {}
		}
		t.Logf("FORMAE_BINARY set to %s but file does not exist, downloading instead", p)
	}

	tmpDir, err := os.MkdirTemp("", "formae-conformance-*")
	if err != nil {
		t.Fatalf("failed to create temp dir for formae binary: %v", err)
	}

	cleanup = func() { os.RemoveAll(tmpDir) }

	// Each channel gets its own orbital tree so the stable and dev
	// repositories don't clobber each other. Managers are built lazily so the
	// dev channel is only refreshed when the stable channel can't satisfy the
	// minimum.
	managers := map[string]*mgr.Manager{}
	roots := map[string]string{}
	queryChannel := func(channel string) (*records.Status, error) {
		orb, ok := managers[channel]
		if !ok {
			root := filepath.Join(tmpDir, channel)
			if err := os.MkdirAll(root, 0o755); err != nil {
				cleanup()
				t.Fatalf("failed to create orbital root for %s channel: %v", channel, err)
			}
			orb = newOrbitalManager(t, root, channel)
			if !orb.Ready() {
				if _, err := orb.Initialize(); err != nil {
					cleanup()
					t.Fatalf("failed to initialize orbital (%s channel): %v", channel, err)
				}
			}
			if err := orb.Refresh(); err != nil {
				cleanup()
				t.Fatalf("failed to refresh orbital repository (%s channel): %v", channel, err)
			}
			managers[channel] = orb
			roots[channel] = root
		}
		return orb.AvailableFor("formae")
	}

	candidate, channel := selectFormaeCandidate(t, queryChannel, cleanup)

	t.Logf("installing formae %s from the %s channel", candidate.Version.Short(), channel)
	if err := managers[channel].Install(candidate.Id().String()); err != nil {
		cleanup()
		t.Fatalf("failed to install formae: %v", err)
	}

	binPath := findBinary(t, roots[channel], cleanup)

	version := extractVersion(t, binPath)
	t.Logf("formae binary version: %s", version)

	t.Setenv("FORMAE_VERSION", version)
	t.Setenv("FORMAE_BINARY", binPath)

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

		if _, err := os.Stat(pklPath); os.IsNotExist(err) {
			t.Logf("No PklProject found in %s, skipping", dir)
			continue
		}

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

// newOrbitalManager creates an orbital Manager pointed at the hub repository
// for the given channel ("stable" or "dev").
func newOrbitalManager(t *testing.T, rootPath, channel string) *mgr.Manager {
	t.Helper()

	u, err := url.Parse(hubRepoBase + "#" + channel)
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

// selectFormaeCandidate decides which formae package to install and from which
// channel. An explicit FORMAE_VERSION is honored exactly, searched across the
// stable then dev channels. Otherwise the plugin's minFormaeVersion (from
// formae-plugin.pkl) drives a stable-preferred, dev-fallback resolution.
func selectFormaeCandidate(t *testing.T, queryChannel func(channel string) (*records.Status, error), cleanup func()) (*records.Package, string) {
	t.Helper()

	if version := os.Getenv("FORMAE_VERSION"); version != "" {
		want := &ops.Version{}
		if err := want.Parse(version); err != nil {
			cleanup()
			t.Fatalf("failed to parse FORMAE_VERSION %q: %v", version, err)
		}
		for _, channel := range []string{"stable", "dev"} {
			available, err := queryChannel(channel)
			if err != nil {
				cleanup()
				t.Fatalf("failed to query available formae versions (%s channel): %v", channel, err)
			}
			if found, candidate := available.HasVersion(want); found {
				return candidate, channel
			}
		}
		cleanup()
		t.Fatalf("formae version %s not found in the stable or dev channel", version)
	}

	minVersion := readMinFormaeVersion(t, cleanup)
	candidate, channel, err := resolveFormaeCandidate(queryChannel, minVersion)
	if err != nil {
		cleanup()
		t.Fatalf("%v", err)
	}
	return candidate, channel
}

// readMinFormaeVersion reads the minFormaeVersion field from the plugin's
// formae-plugin.pkl manifest in the current working directory. This is the
// compatibility floor the conformance run must meet.
func readMinFormaeVersion(t *testing.T, cleanup func()) *ops.Version {
	t.Helper()

	out, err := exec.Command("pkl", "eval", "-x", "minFormaeVersion", "formae-plugin.pkl").CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("failed to read minFormaeVersion from formae-plugin.pkl: %v\noutput: %s", err, string(out))
	}

	raw := strings.TrimSpace(string(out))
	v := &ops.Version{}
	if err := v.Parse(raw); err != nil {
		cleanup()
		t.Fatalf("failed to parse minFormaeVersion %q from formae-plugin.pkl: %v", raw, err)
	}
	return v
}

// resolveFormaeCandidate picks the formae package to install by preferring the
// highest stable release that meets minVersion, falling back to the highest
// qualifying dev release, and erroring if neither channel has one. queryChannel
// returns the available packages for a given channel ("stable" or "dev").
func resolveFormaeCandidate(queryChannel func(channel string) (*records.Status, error), minVersion *ops.Version) (*records.Package, string, error) {
	for _, channel := range []string{"stable", "dev"} {
		available, err := queryChannel(channel)
		if err != nil {
			return nil, "", fmt.Errorf("querying %s channel for formae versions: %w", channel, err)
		}
		if candidate := pickHighestAtLeast(available, minVersion); candidate != nil {
			return candidate, channel, nil
		}
	}
	return nil, "", fmt.Errorf("no formae release >= %s available in the stable or dev channel", minVersion.Short())
}

// pickHighestAtLeast returns the highest-version package whose core
// (major.minor.patch) version is >= minVersion, or nil if none qualify. The
// pre-release component is ignored on purpose: a dev build of the target
// version (e.g. 0.86.0-dev.4) is treated as meeting a 0.86.0 floor, which is
// what makes the dev-channel fallback useful.
func pickHighestAtLeast(available *records.Status, minVersion *ops.Version) *records.Package {
	if available == nil {
		return nil
	}
	available.Sort() // highest version first
	for _, pkg := range available.Available {
		if versionAtLeast(pkg.Version, minVersion) {
			return pkg
		}
	}
	return nil
}

// versionAtLeast reports whether v's core version is >= min's core version,
// ignoring pre-release and build metadata.
func versionAtLeast(v, min *ops.Version) bool {
	vCore := &ops.Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
	minCore := &ops.Version{Major: min.Major, Minor: min.Minor, Patch: min.Patch}
	return vCore.Compare(minCore) != -1
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

	// Output is "formae version: X.Y.Z\ngo version: go1.X.Y" — parse first line only
	firstLine := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) == 0 {
		t.Fatalf("empty version output from formae binary")
	}

	// Return the last field of the first line (the formae version)
	return parts[len(parts)-1]
}
