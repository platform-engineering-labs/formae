// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package ppm

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/masterminds/semver"
)

func TestInstall_ResolvesLatest(t *testing.T) {
	oa := CurrentOsArch()
	repo := repoWithEntries(t,
		entry("formae", "1.0.0", oa),
		entry("formae", "2.0.0", oa),
		entry("formae", "3.0.0-rc1", oa),
		entry("formae", "1.5.0", &OSArch{Linux, X8664}),
	)

	pi := &mockPluginInstaller{}
	prefix := t.TempDir()

	installer, err := NewInstaller(
		&RepoConfig{Uri: mustParseURL(t, "https://example.com")},
		InstallConfig{Prefix: prefix, SkipPrompt: true},
		WithPluginInstaller(pi),
		WithInstallerLogger(noopLogger),
		WithExtractor(noopExtractor),
		WithPrompter(alwaysYes),
	)
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	installer.repo = repo

	if err := installer.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if repo.fetchedPackage == nil {
		t.Fatal("expected a package to be fetched")
	}
	if repo.fetchedPackage.Version.String() != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %s", repo.fetchedPackage.Version)
	}

	if len(pi.execCalls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(pi.execCalls))
	}
	if pi.execCalls[0].version != "2.0.0" {
		t.Errorf("exec call version: got %s, want 2.0.0", pi.execCalls[0].version)
	}
	if len(pi.resourceCalls) != 1 {
		t.Fatalf("expected 1 resource call, got %d", len(pi.resourceCalls))
	}
}

func TestInstall_SpecificVersion(t *testing.T) {
	oa := CurrentOsArch()
	repo := repoWithEntries(t,
		entry("formae", "1.0.0", oa),
		entry("formae", "2.0.0", oa),
	)

	pi := &mockPluginInstaller{}
	prefix := t.TempDir()

	installer, err := NewInstaller(
		&RepoConfig{Uri: mustParseURL(t, "https://example.com")},
		InstallConfig{Version: "1.0.0", Prefix: prefix, SkipPrompt: true},
		WithPluginInstaller(pi),
		WithInstallerLogger(noopLogger),
		WithExtractor(noopExtractor),
		WithPrompter(alwaysYes),
	)
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}
	installer.repo = repo

	if err := installer.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if repo.fetchedPackage == nil {
		t.Fatal("expected a package to be fetched")
	}
	if repo.fetchedPackage.Version.String() != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", repo.fetchedPackage.Version)
	}
}

func TestInstall_LocalFile(t *testing.T) {
	pi := &mockPluginInstaller{}
	prefix := t.TempDir()

	oa := CurrentOsArch()
	fileName := Package.FileName("formae", semver.MustParse("4.5.6"), oa)
	localPath := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(localPath, []byte("fake-tgz"), 0644); err != nil {
		t.Fatal(err)
	}

	var extractedTo string
	installer, err := NewInstaller(
		&RepoConfig{Uri: mustParseURL(t, "https://example.com")},
		InstallConfig{LocalFile: localPath, Prefix: prefix, SkipPrompt: true},
		WithPluginInstaller(pi),
		WithInstallerLogger(noopLogger),
		WithExtractor(func(tgzPath, destDir string) error {
			extractedTo = destDir
			return nil
		}),
		WithPrompter(alwaysYes),
	)
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}

	if err := installer.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if extractedTo != prefix {
		t.Errorf("extracted to %q, want %q", extractedTo, prefix)
	}

	if len(pi.execCalls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(pi.execCalls))
	}
	if pi.execCalls[0].version != "4.5.6" {
		t.Errorf("exec call version: got %s, want 4.5.6", pi.execCalls[0].version)
	}
}

func TestInstall_LocalFile_BadName(t *testing.T) {
	pi := &mockPluginInstaller{}
	prefix := t.TempDir()

	localPath := filepath.Join(t.TempDir(), "my-custom-build.tgz")
	if err := os.WriteFile(localPath, []byte("fake-tgz"), 0644); err != nil {
		t.Fatal(err)
	}

	installer, err := NewInstaller(
		&RepoConfig{Uri: mustParseURL(t, "https://example.com")},
		InstallConfig{LocalFile: localPath, Prefix: prefix, SkipPrompt: true},
		WithPluginInstaller(pi),
		WithInstallerLogger(noopLogger),
		WithExtractor(noopExtractor),
		WithPrompter(alwaysYes),
	)
	if err != nil {
		t.Fatalf("NewInstaller: %v", err)
	}

	if err := installer.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if len(pi.execCalls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(pi.execCalls))
	}
	if pi.execCalls[0].version != "local" {
		t.Errorf("exec call version: got %q, want %q", pi.execCalls[0].version, "local")
	}
}

type mockRepoReader struct {
	data           *RepoData
	fetchErr       error
	fetchedPackage *PkgEntry
	fetchPkgErr    error
	fetchPkgFn     func(entry *PkgEntry, path string) error
}

func (m *mockRepoReader) Data() *RepoData { return m.data }
func (m *mockRepoReader) Fetch() error    { return m.fetchErr }
func (m *mockRepoReader) FetchPackage(entry *PkgEntry, path string) error {
	m.fetchedPackage = entry
	if m.fetchPkgFn != nil {
		return m.fetchPkgFn(entry, path)
	}
	return m.fetchPkgErr
}

type mockPluginInstaller struct {
	execCalls     []execCall
	resourceCalls []string
	execErr       error
	resourceErr   error
}

type execCall struct {
	prefix  string
	version string
}

func (m *mockPluginInstaller) InstallExecutables(prefix, version string) error {
	m.execCalls = append(m.execCalls, execCall{prefix, version})
	return m.execErr
}

func (m *mockPluginInstaller) InstallResourcePlugins(prefix string) error {
	m.resourceCalls = append(m.resourceCalls, prefix)
	return m.resourceErr
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return u
}

func noopLogger(string, ...any) {}

func noopExtractor(_, _ string) error { return nil }

func alwaysYes(_ string) (bool, error) { return true, nil }

func repoWithEntries(t *testing.T, entries ...*PkgEntry) *mockRepoReader {
	t.Helper()
	return &mockRepoReader{
		data: &RepoData{Packages: entries},
		fetchPkgFn: func(entry *PkgEntry, path string) error {
			fileName := Package.FileName(entry.Name, entry.Version, entry.OsArch)
			return os.WriteFile(filepath.Join(path, fileName), []byte("fake-tgz"), 0644)
		},
	}
}

func entry(name, ver string, oa *OSArch) *PkgEntry {
	content := []byte("fake-tgz")
	h := sha256.Sum256(content)
	return &PkgEntry{
		Name:    name,
		Version: semver.MustParse(ver),
		OsArch:  oa,
		Sha256:  hex.EncodeToString(h[:]),
	}
}
