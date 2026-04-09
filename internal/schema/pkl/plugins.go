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

	"github.com/masterminds/semver"
)

// GeneratePluginWrappers scans pluginDir for installed plugins that contain
// schema/pkl/Config.pkl and generates a PascalCase wrapper PKL file in the
// pluginDir root for each one. For example, plugin auth-basic/v0.1.0/schema/pkl/Config.pkl
// produces AuthBasic.pkl containing: extends "./auth-basic/v0.1.0/schema/pkl/Config.pkl"
//
// These wrapper files allow users to write:
//
//	import "plugins:/AuthBasic.pkl" as AuthBasic
//	new AuthBasic.AgentConfig { ... }
func GeneratePluginWrappers(pluginDir string) error {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return fmt.Errorf("reading plugin directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginName := entry.Name()
		pluginPath := filepath.Join(pluginDir, pluginName)

		versionDir := findHighestVersionDir(pluginPath)
		if versionDir == "" {
			continue
		}

		configPath := filepath.Join(pluginPath, versionDir, "schema", "Config.pkl")
		if _, err := os.Stat(configPath); err != nil {
			continue
		}

		wrapperName := toPascalCase(pluginName) + ".pkl"
		wrapperPath := filepath.Join(pluginDir, wrapperName)

		relativePath := fmt.Sprintf("./%s/%s/schema/Config.pkl", pluginName, versionDir)
		content := fmt.Sprintf(
			"/// Auto-generated wrapper for %s plugin.\n"+
				"/// Do not edit — regenerated on each config evaluation.\n"+
				"extends \"%s\"\n",
			pluginName, relativePath,
		)

		if err := os.WriteFile(wrapperPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing wrapper %s: %w", wrapperPath, err)
		}
	}

	return nil
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
		if !entry.IsDir() {
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
