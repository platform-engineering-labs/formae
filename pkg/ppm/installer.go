// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/masterminds/semver"
)

// Installer orchestrates a full formae installation.
type Installer struct {
	repo    RepoReader
	copier  PluginInstaller
	osArch  *OSArch
	cfg     InstallConfig
	prompt  func(msg string) (bool, error)
	log     func(string, ...any)
	extract func(tgzPath, destDir string) error
}

// NewInstaller creates an Installer backed by the given repo configuration.
func NewInstaller(repoCfg *RepoConfig, cfg InstallConfig, opts ...InstallerOption) (*Installer, error) {
	repo, err := Repo.GetReader(repoCfg)
	if err != nil {
		return nil, err
	}

	inst := &Installer{
		repo:    repo,
		osArch:  CurrentOsArch(),
		cfg:     cfg,
		log:     func(format string, args ...any) { fmt.Printf(format, args...) },
		prompt:  stdinPrompt,
		extract: extractArchiveFromFile,
	}

	for _, opt := range opts {
		opt(inst)
	}

	if inst.copier == nil {
		copier, err := NewPluginCopier(
			WithLogger(inst.log),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create plugin copier: %w", err)
		}
		inst.copier = copier
	}

	return inst, nil
}

type InstallConfig struct {
	Version    string // empty or "latest" = resolve latest
	Prefix     string // install prefix, default "/opt/pel"
	LocalFile  string // path to local .tgz, bypasses download
	SkipPrompt bool
}

type InstallerOption func(*Installer)

func WithPluginInstaller(pi PluginInstaller) InstallerOption {
	return func(i *Installer) {
		i.copier = pi
	}
}

func WithPrompter(fn func(msg string) (bool, error)) InstallerOption {
	return func(i *Installer) {
		i.prompt = fn
	}
}

func WithInstallerLogger(fn func(string, ...any)) InstallerOption {
	return func(i *Installer) {
		i.log = fn
	}
}

func WithExtractor(fn func(tgzPath, destDir string) error) InstallerOption {
	return func(i *Installer) {
		i.extract = fn
	}
}

// Install runs the full installation workflow.
func (i *Installer) Install() error {
	version, tgzPath, err := i.resolveSource()
	if err != nil {
		return err
	}

	if !Sys.IsPrivilegedUser() && needsSudo(i.cfg.Prefix) {
		i.log("this command requires a privileged user: authentication may be required\n")
		args := []string{"install"}
		if i.cfg.Version != "" {
			args = append(args, "--version", i.cfg.Version)
		}
		if i.cfg.Prefix != "" {
			args = append(args, "--prefix", i.cfg.Prefix)
		}
		if i.cfg.LocalFile != "" {
			args = append(args, "--file", i.cfg.LocalFile)
		}
		if i.cfg.SkipPrompt {
			args = append(args, "--yes")
		}
		return Sys.InvokeSelfWithSudo(args...)
	}

	if !i.cfg.SkipPrompt {
		ok, err := i.prompt(fmt.Sprintf("This will install formae %s to %s. Continue?", version, i.cfg.Prefix))
		if err != nil {
			return err
		}
		if !ok {
			i.log("Cancelled.\n")
			return nil
		}
	}

	if err := os.MkdirAll(i.cfg.Prefix, 0755); err != nil {
		return fmt.Errorf("failed to create install prefix: %w", err)
	}

	i.log("installing formae %s...\n", version)

	if err := i.extract(tgzPath, i.cfg.Prefix); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	if err := i.copier.InstallExecutables(i.cfg.Prefix, version); err != nil {
		return fmt.Errorf("failed to install plugin executables: %w", err)
	}

	if err := i.copier.InstallResourcePlugins(i.cfg.Prefix); err != nil {
		return fmt.Errorf("failed to install resource plugins: %w", err)
	}

	i.log("done.\n\n")
	i.log("IMPORTANT: ensure you add %s/formae/bin to your PATH, and reload your shell configuration\n", i.cfg.Prefix)
	i.log("For shell completions, run: formae completion --help\n")

	return nil
}

func (i *Installer) resolveSource() (version string, tgzPath string, err error) {
	if i.cfg.LocalFile != "" {
		return i.resolveLocalFile()
	}
	return i.resolveRemote()
}

func (i *Installer) resolveLocalFile() (string, string, error) {
	if _, err := os.Stat(i.cfg.LocalFile); err != nil {
		return "", "", fmt.Errorf("local file not found: %s", i.cfg.LocalFile)
	}

	entry, err := Package.EntryFromFilePath(i.cfg.LocalFile)
	if err != nil {
		return "local", i.cfg.LocalFile, nil
	}

	return entry.Version.String(), i.cfg.LocalFile, nil
}

func (i *Installer) resolveRemote() (string, string, error) {
	if err := i.repo.Fetch(); err != nil {
		return "", "", fmt.Errorf("failed to fetch repository: %w", err)
	}

	var candidate *PkgEntry

	if i.cfg.Version == "" || i.cfg.Version == "latest" {
		candidate = i.latestStable()
		if candidate == nil {
			return "", "", fmt.Errorf("no version found for platform %s", i.osArch)
		}
	} else {
		v, err := semver.NewVersion(i.cfg.Version)
		if err != nil {
			return "", "", err
		}
		candidate = i.repo.Data().GetEntry("formae", v, i.osArch)
		if candidate == nil {
			return "", "", fmt.Errorf("version %s not found for platform %s", i.cfg.Version, i.osArch)
		}
	}

	tmpDir, err := os.MkdirTemp("", "ppm-install-")
	if err != nil {
		return "", "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	i.log("downloading formae %s (this may take a moment)...\n", candidate.Version)

	if err := i.repo.FetchPackage(candidate, tmpDir); err != nil {
		return "", "", fmt.Errorf("failed to download package: %w", err)
	}

	tgzPath := filepath.Join(tmpDir, Package.FileName(candidate.Name, candidate.Version, candidate.OsArch))

	f, err := os.Open(tgzPath)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()

	if err := Package.Verify(f, candidate.Sha256); err != nil {
		return "", "", fmt.Errorf("checksum verification failed: %w", err)
	}

	cleanup = false
	return candidate.Version.String(), tgzPath, nil
}

func (i *Installer) latestStable() *PkgEntry {
	var best *PkgEntry
	for _, pkg := range i.repo.Data().Packages {
		if pkg.Name != "formae" {
			continue
		}
		if pkg.OsArch.String() != i.osArch.String() {
			continue
		}
		if pkg.Version.Prerelease() != "" {
			continue
		}
		if best == nil || pkg.Version.GreaterThan(best.Version) {
			best = pkg
		}
	}
	return best
}

func needsSudo(prefix string) bool {
	_, err := os.Stat(prefix)
	if err == nil {
		tmp, err := os.CreateTemp(prefix, ".ppm-check-*")
		if err != nil {
			return true
		}
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return false
	}

	parent := filepath.Dir(prefix)
	if _, err = os.Stat(parent); err != nil {
		return true
	}
	tmp, err := os.CreateTemp(parent, ".ppm-check-*")
	if err != nil {
		return true
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return false
}

func stdinPrompt(msg string) (bool, error) {
	fmt.Printf("%s [Y/n] ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, scanner.Err()
	}
	input := strings.TrimSpace(scanner.Text())
	return input == "" || strings.EqualFold(input, "y") || strings.EqualFold(input, "yes"), nil
}
