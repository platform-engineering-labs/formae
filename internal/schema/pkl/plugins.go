// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pklgo "github.com/apple/pkl-go/pkl"
	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/plugin/discovery"
)

// pluginsSchemeOption wires the plugins:/ FS scheme so configs can write
// `import "plugins:/Aws.pkl"` regardless of whether the AWS plugin lives in
// the user's writable plugin dir or under SystemPluginDir(exePath).
//
// PKL refuses cross-scheme imports — a `plugins:/` module can't `extends` a
// `file://` URI without explicit trust configuration — so wrappers must use
// relative paths inside the plugins:/ mount. To make system-installed
// plugins reachable, we mirror them into wrapperDir as symlinks. Names
// already present in wrapperDir (real dev plugins or hand-managed symlinks)
// are left alone, mirroring the dev-wins semantics of
// discovery.DiscoverPluginsMulti.
//
// Returns nil if wrapperDir is empty (no usable home).
func pluginsSchemeOption(wrapperDir, exePath string) (func(*pklgo.EvaluatorOptions), error) {
	if wrapperDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(wrapperDir, 0755); err != nil {
		return nil, fmt.Errorf("creating plugin wrapper dir %s: %w", wrapperDir, err)
	}

	var systemDirs []string
	if exePath != "" {
		if sys := discovery.SystemPluginDir(exePath); sys != "" && sys != wrapperDir {
			systemDirs = append(systemDirs, sys)
		}
	}
	if err := mirrorSystemPlugins(wrapperDir, systemDirs); err != nil {
		return nil, err
	}
	if err := GeneratePluginWrappers(wrapperDir); err != nil {
		return nil, err
	}
	return pklgo.WithFs(os.DirFS(wrapperDir), "plugins"), nil
}

// executablePath returns the running executable's path with symlinks resolved,
// or "" if it can't be determined. Used to derive the system plugin dir.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

// mirrorManifestFile is the sidecar that records every symlink
// mirrorSystemPlugins has placed under wrapperDir. Format: one
// `<name>\t<absolute target>` line per managed symlink. The manifest is the
// sole source of ownership — any wrapperDir entry not listed here is
// considered user-managed and is never touched.
const mirrorManifestFile = ".system-mirrors"

// mirrorSystemPlugins ensures every plugin under any systemDir is reachable
// from wrapperDir as a symlink, so configs can `import "plugins:/<X>.pkl"`
// without falling foul of PKL's cross-scheme trust rules.
//
// Symlinks recorded in the manifest are reclaimed when their recorded target
// no longer matches the current symlink target, the target is no longer a
// directory, or the target sits outside the current systemDirs (e.g. the
// formae binary moved to a new install root across an upgrade). Symlinks
// that aren't in the manifest — including dev plugins and user-managed
// links — are left alone.
func mirrorSystemPlugins(wrapperDir string, systemDirs []string) error {
	managed, err := readMirrorManifest(wrapperDir)
	if err != nil {
		return err
	}
	if err := pruneSystemSymlinks(wrapperDir, systemDirs, managed); err != nil {
		return err
	}
	for _, sys := range systemDirs {
		entries, err := os.ReadDir(sys)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			target := filepath.Join(sys, name)
			if !isDirFollowingSymlinks(target) {
				continue
			}
			link := filepath.Join(wrapperDir, name)
			if _, err := os.Lstat(link); err == nil {
				continue
			}
			if err := os.Symlink(target, link); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
			}
			managed[name] = target
		}
	}
	return writeMirrorManifest(wrapperDir, managed)
}

// pruneSystemSymlinks removes managed symlinks whose recorded target either
// no longer exists, no longer matches the on-disk symlink target (the user
// took over the entry), or sits outside the current systemDirs (the install
// root changed). Entries owned by us but reclaimed for any of those reasons
// are dropped from the managed map so the manifest reflects the true state.
func pruneSystemSymlinks(wrapperDir string, systemDirs []string, managed map[string]string) error {
	for name, recordedTarget := range managed {
		path := filepath.Join(wrapperDir, name)
		linkTarget, err := os.Readlink(path)
		if err != nil {
			delete(managed, name)
			continue
		}
		absTarget := linkTarget
		if !filepath.IsAbs(linkTarget) {
			absTarget = filepath.Join(wrapperDir, linkTarget)
		}
		// User replaced our link with their own — drop from manifest, leave alone.
		if absTarget != recordedTarget {
			delete(managed, name)
			continue
		}
		// Recorded link still in place. Keep it only if it points into the
		// current system tree AND the target plugin still exists.
		if pathInsideAny(absTarget, systemDirs) {
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing stale plugin symlink %s: %w", path, err)
		}
		delete(managed, name)
	}
	return nil
}

// readMirrorManifest loads the symlinks-we-own ledger from wrapperDir. A
// missing file is treated as an empty ledger so first runs work without
// special-casing.
func readMirrorManifest(wrapperDir string) (map[string]string, error) {
	managed := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(wrapperDir, mirrorManifestFile))
	if err != nil {
		if os.IsNotExist(err) {
			return managed, nil
		}
		return nil, fmt.Errorf("reading mirror manifest: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		managed[parts[0]] = parts[1]
	}
	return managed, nil
}

// writeMirrorManifest persists the symlinks-we-own ledger atomically. Empty
// ledgers are kept on disk (rather than removed) so future runs can tell the
// difference between "we own nothing" and "first run".
func writeMirrorManifest(wrapperDir string, managed map[string]string) error {
	names := make([]string, 0, len(managed))
	for name := range managed {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Auto-generated. Tracks symlinks created by formae's plugin mirror.\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s\t%s\n", name, managed[name])
	}
	manifestPath := filepath.Join(wrapperDir, mirrorManifestFile)
	tmp := manifestPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("writing mirror manifest: %w", err)
	}
	if err := os.Rename(tmp, manifestPath); err != nil {
		return fmt.Errorf("rotating mirror manifest: %w", err)
	}
	return nil
}

func pathInsideAny(path string, roots []string) bool {
	for _, root := range roots {
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

const autoGeneratedHeader = "/// Auto-generated wrapper for "

// GeneratePluginWrappers scans wrapperDir for installed plugins that contain
// schema/Config.pkl and writes a PascalCase wrapper PKL file in wrapperDir
// for each one. Symlinks under wrapperDir are followed, so plugins mirrored
// from a system dir are picked up the same way as plugins installed
// directly in wrapperDir.
//
// After writing, any wrapper carrying the auto-generated header whose plugin
// is no longer present is removed. Hand-written .pkl files are left
// untouched.
//
// Resulting wrappers allow users to write:
//
//	import "plugins:/AuthBasic.pkl" as AuthBasic
//	new AuthBasic.AgentConfig { ... }
func GeneratePluginWrappers(wrapperDir string) error {
	if wrapperDir == "" {
		return nil
	}

	entries, err := os.ReadDir(wrapperDir)
	if err != nil {
		return fmt.Errorf("reading plugin directory: %w", err)
	}

	expected := make(map[string]bool)
	for _, entry := range entries {
		pluginName := entry.Name()
		pluginPath := filepath.Join(wrapperDir, pluginName)
		if !isDirFollowingSymlinks(pluginPath) {
			continue
		}

		versionDir := findHighestVersionDir(pluginPath)
		if versionDir == "" {
			continue
		}
		configPath := filepath.Join(pluginPath, versionDir, "schema", "Config.pkl")
		if _, err := os.Stat(configPath); err != nil {
			continue
		}

		wrapperName := toPascalCase(pluginName) + ".pkl"
		wrapperPath := filepath.Join(wrapperDir, wrapperName)

		content := fmt.Sprintf(
			"%s%s plugin.\n"+
				"/// Do not edit; regenerated on each config evaluation.\n"+
				"extends \"./%s/%s/schema/Config.pkl\"\n",
			autoGeneratedHeader, pluginName, pluginName, versionDir,
		)
		if err := os.WriteFile(wrapperPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing wrapper %s: %w", wrapperPath, err)
		}
		expected[wrapperName] = true
	}

	return sweepStaleWrappers(wrapperDir, expected)
}

// sweepStaleWrappers removes auto-generated wrappers in wrapperDir whose
// filename is not in `expected`. Files without the auto-generated header
// (i.e. hand-written) are never touched.
func sweepStaleWrappers(wrapperDir string, expected map[string]bool) error {
	entries, err := os.ReadDir(wrapperDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".pkl") {
			continue
		}
		if expected[name] {
			continue
		}
		path := filepath.Join(wrapperDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(string(data), autoGeneratedHeader) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing stale wrapper %s: %w", path, err)
		}
	}
	return nil
}

// isDirFollowingSymlinks reports whether path is a directory, following
// symlinks. os.DirEntry.IsDir() returns false for a symlink even when the
// target is a directory, which would skip plugins installed via symlink
// (common in dev setups).
func isDirFollowingSymlinks(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// findHighestVersionDir returns the name of the highest semver directory inside
// dir (e.g. "v0.2.0"). Returns "" if no version directories are found.
func findHighestVersionDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var versions []string
	for _, entry := range entries {
		if !isDirFollowingSymlinks(filepath.Join(dir, entry.Name())) {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "v") {
			versions = append(versions, name)
		}
	}

	if len(versions) == 0 {
		return ""
	}

	sort.Slice(versions, func(i, j int) bool {
		vi, errI := semver.NewVersion(versions[i])
		vj, errJ := semver.NewVersion(versions[j])
		if errI != nil || errJ != nil {
			return versions[i] > versions[j]
		}
		return vi.GreaterThan(vj)
	})

	return versions[0]
}

// toPascalCase converts a hyphenated plugin name to PascalCase.
// For example: "auth-basic" -> "AuthBasic", "gcp" -> "Gcp".
func toPascalCase(s string) string {
	parts := strings.Split(s, "-")
	var b strings.Builder
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			b.WriteString(part[1:])
		}
	}
	return b.String()
}

// defaultPluginDir returns the plugin directory path. It checks the
// FORMAE_PLUGIN_DIR environment variable first, falling back to
// ~/.pel/formae/plugins. This allows custom plugin directories to be
// resolved at config-evaluation time (before the config's own pluginDir
// field is available).
func defaultPluginDir() string {
	if dir := os.Getenv("FORMAE_PLUGIN_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pel", "formae", "plugins")
}
