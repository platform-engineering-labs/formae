// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package ppm

import (
	"debug/elf"
	"debug/macho"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// PluginInstaller handles post-extraction plugin relocation from the
// system install prefix to the user's plugin directory.
type PluginInstaller interface {
	InstallExecutables(installPrefix, version string) error
	InstallResourcePlugins(installPrefix string) error
}

// PluginCopier copies plugin artifacts from the system install directory
// to the user's plugin directory.
type PluginCopier struct {
	homeDir string
	log     func(string, ...any)
}

// NewPluginCopier creates a PluginCopier. By default it resolves the
// home directory from the current user (or SUDO_USER if running under sudo).
func NewPluginCopier(opts ...PluginCopierOption) (*PluginCopier, error) {
	c := &PluginCopier{
		log: func(format string, args ...any) { fmt.Printf(format, args...) },
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.homeDir == "" {
		home, err := resolveHomeDir()
		if err != nil {
			return nil, err
		}
		c.homeDir = home
	}
	return c, nil
}

type PluginCopierOption func(*PluginCopier)

func WithHomeDir(dir string) PluginCopierOption {
	return func(c *PluginCopier) {
		c.homeDir = dir
	}
}

func WithLogger(fn func(string, ...any)) PluginCopierOption {
	return func(c *PluginCopier) {
		c.log = fn
	}
}

// InstallExecutables copies plugin executables from the system install
// directory to the user's plugin directory, then removes them from source.
func (c *PluginCopier) InstallExecutables(installPrefix, version string) error {
	pluginsDir := filepath.Join(installPrefix, "formae", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		srcPath := filepath.Join(pluginsDir, name)

		if strings.HasSuffix(name, ".so") {
			continue
		}

		if !isExecutable(srcPath) {
			continue
		}

		destDir := filepath.Join(c.homeDir, ".pel", "formae", "plugins", name, "v"+version)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to create plugin directory %s: %w", destDir, err)
		}

		destPath := filepath.Join(destDir, name)
		if err := copyFile(srcPath, destPath); err != nil {
			return fmt.Errorf("failed to copy plugin %s: %w", name, err)
		}

		chownToSudoUser(destPath, c.homeDir)

		if err := os.Remove(srcPath); err != nil {
			return fmt.Errorf("failed to remove plugin from system directory: %w", err)
		}

		c.log("installed plugin: %s\n", name)
	}

	return nil
}

// InstallResourcePlugins copies resource plugin directories from the system
// install directory to the user's plugin directory, then removes the source.
func (c *PluginCopier) InstallResourcePlugins(installPrefix string) error {
	rpDir := filepath.Join(installPrefix, "formae", "resource-plugins")
	namespaces, err := os.ReadDir(rpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read resource-plugins directory: %w", err)
	}

	for _, nsEntry := range namespaces {
		if !nsEntry.IsDir() {
			continue
		}
		namespace := strings.ToLower(nsEntry.Name())
		nsPath := filepath.Join(rpDir, nsEntry.Name())

		existingDir := filepath.Join(c.homeDir, ".pel", "formae", "plugins", namespace)
		if err := os.RemoveAll(existingDir); err != nil {
			return fmt.Errorf("failed to remove existing plugin directory %s: %w", existingDir, err)
		}

		versions, err := os.ReadDir(nsPath)
		if err != nil {
			continue
		}

		for _, vEntry := range versions {
			if !vEntry.IsDir() {
				continue
			}
			ver := vEntry.Name()
			srcDir := filepath.Join(nsPath, ver)
			destDir := filepath.Join(c.homeDir, ".pel", "formae", "plugins", namespace, ver)

			if err := copyDir(srcDir, destDir); err != nil {
				return fmt.Errorf("failed to copy plugin %s/%s: %w", namespace, ver, err)
			}

			chownToSudoUser(destDir, c.homeDir)

			c.log("installed resource plugin: %s %s\n", namespace, ver)
		}
	}

	if err := os.RemoveAll(rpDir); err != nil {
		return fmt.Errorf("failed to remove resource-plugins from system directory: %w", err)
	}

	return nil
}

func isExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	switch runtime.GOOS {
	case "darwin":
		if _, err = macho.NewFile(f); err == nil {
			return true
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return false
		}
		if _, err = macho.NewFatFile(f); err == nil {
			return true
		}
		return false
	default:
		_, err = elf.NewFile(f)
		return err == nil
	}
}

func copyFile(src, dst string) (err error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func resolveHomeDir() (string, error) {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		u, err := user.Lookup(sudoUser)
		if err != nil {
			return "", fmt.Errorf("failed to lookup sudo user %s: %w", sudoUser, err)
		}
		return u.HomeDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return home, nil
}

func chownToSudoUser(path, homeDir string) {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		return
	}

	u, err := user.Lookup(sudoUser)
	if err != nil {
		return
	}

	var uid, gid int
	_, _ = fmt.Sscanf(u.Uid, "%d", &uid)
	_, _ = fmt.Sscanf(u.Gid, "%d", &gid)

	_ = os.Chown(filepath.Join(homeDir, ".pel"), uid, gid)
	_ = os.Chown(filepath.Join(homeDir, ".pel", "formae"), uid, gid)
	_ = os.Chown(filepath.Join(homeDir, ".pel", "formae", "plugins"), uid, gid)

	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Chown(p, uid, gid)
		return nil
	})
}
