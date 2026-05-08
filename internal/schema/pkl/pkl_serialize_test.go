// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

func TestResolveIncludes_PreResolvedDepsTakePrecedence(t *testing.T) {
	forma := &model.Forma{Resources: []model.Resource{{Type: "AWS::S3::Bucket"}}}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: "/this/path/should/not/be/touched",
		Dependencies: []string{
			"pkl.formae@0.85.0",
			"local:aws:/some/path/PklProject",
		},
	}

	got := resolveIncludes(forma, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:aws:/some/path/PklProject",
	}, got)
}

func TestResolveIncludes_RemoteOnlyWhenNoDirAndNoDeps(t *testing.T) {
	forma := &model.Forma{Resources: []model.Resource{{Type: "AWS::S3::Bucket"}}}
	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationRemote,
	}

	got := resolveIncludes(forma, options)

	// formae version is the binary's compile-time version (test build = "0.0.0"),
	// so it's filtered out of the includes by the resolver. We expect only the
	// remote aws entry — and since aws version is "" (no installed version), it's
	// added as a plain namespace.
	assert.Contains(t, got, "aws.aws@")
}

// installVersionedPluginForSerialize mirrors installVersionedPlugin from the
// package_resolver test; duplicated here only because helpers in
// _test.go files don't cross test files in build-tag splits.
func installVersionedPluginForSerialize(t *testing.T, namespace, pkgName string, versionSubdirs []string) string {
	t.Helper()
	tmpDir := t.TempDir()
	versionDir := filepath.Join(tmpDir, pkgName, "v0.1.1")
	pklDir := filepath.Join(versionDir, "schema", "pkl")
	require.NoError(t, os.MkdirAll(pklDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(versionDir, "formae-plugin.pkl"),
		[]byte(fmt.Sprintf("namespace = %q", namespace)), 0644))
	require.NoError(t, os.WriteFile(
		filepath.Join(pklDir, "PklProject"),
		[]byte(fmt.Sprintf("amends \"pkl:Project\"\npackage { name = %q }\n", pkgName)), 0644))
	for _, sub := range versionSubdirs {
		require.NoError(t, os.MkdirAll(filepath.Join(pklDir, sub), 0755))
	}
	return tmpDir
}

func TestResolveSchemaVersions_NilForma(t *testing.T) {
	assert.Nil(t, resolveSchemaVersions(nil, nil))
	assert.Nil(t, resolveSchemaVersions(nil, &schema.SerializeOptions{}))
}

func TestResolveSchemaVersions_OptionsTakePrecedence(t *testing.T) {
	forma := &model.Forma{
		Targets: []model.Target{{Namespace: "K8S", SchemaVersion: "v1.20"}},
	}
	options := &schema.SerializeOptions{
		SchemaVersions: map[string]string{"k8s": "v1.30"},
	}
	got := resolveSchemaVersions(forma, options)
	assert.Equal(t, "v1.30", got["k8s"], "explicit options.SchemaVersions wins over Target stamp")
}

func TestResolveSchemaVersions_TargetStampUsedWhenOptionsEmpty(t *testing.T) {
	forma := &model.Forma{
		Targets: []model.Target{{Namespace: "K8S", SchemaVersion: "v1.27"}},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{})
	assert.Equal(t, "v1.27", got["k8s"])
}

func TestResolveSchemaVersions_ManifestDefaultUsedWhenStampEmpty(t *testing.T) {
	tmpDir := installVersionedPluginForSerialize(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{LocalPluginDir: tmpDir})
	assert.Equal(t, "v1.34", got["k8s"], "filesystem-derived default = lexically-highest v* subdir")
}

func TestResolveSchemaVersions_EnvOverrideWinsOverEverything(t *testing.T) {
	tmpDir := installVersionedPluginForSerialize(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	t.Setenv("FORMAE_SCHEMA_VERSIONS", "k8s=v1.21")
	forma := &model.Forma{
		Targets:   []model.Target{{Namespace: "K8S", SchemaVersion: "v1.30"}},
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	options := &schema.SerializeOptions{
		LocalPluginDir: tmpDir,
		SchemaVersions: map[string]string{"k8s": "v1.27"},
	}
	got := resolveSchemaVersions(forma, options)
	assert.Equal(t, "v1.21", got["k8s"], "env var override is the topmost layer")
}

func TestResolveSchemaVersions_NamespaceWithNoSourceOmitted(t *testing.T) {
	forma := &model.Forma{
		Resources: []model.Resource{{Type: "AWS::S3::Bucket"}},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{})
	assert.Nil(t, got, "no target stamp, no manifest, no env override → nil so ImportsGenerator falls back to unrestricted glob")
}

// keep the JSON shape stable when nothing is set
func TestResolveSchemaVersions_PlumbedViaPackageResolver(t *testing.T) {
	// Confirms the resolver hits the filesystem scan path when only the
	// LocalPluginDir is set — no Target stamp, no env override, no
	// caller-supplied SchemaVersions.
	tmpDir := installVersionedPluginForSerialize(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{Resources: []model.Resource{{Type: "K8S::Core::Pod"}}}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{LocalPluginDir: tmpDir})
	assert.Equal(t, "v1.34", got["k8s"])
}

func TestFormatVersionsForProperty_Empty(t *testing.T) {
	assert.Equal(t, "", formatVersionsForProperty(nil))
	assert.Equal(t, "", formatVersionsForProperty(map[string]string{}))
}

func TestFormatVersionsForProperty_SingleEntry(t *testing.T) {
	assert.Equal(t, "k8s=v1.30", formatVersionsForProperty(map[string]string{"k8s": "v1.30"}))
}

func TestFormatVersionsForProperty_StableOrderAcrossKeys(t *testing.T) {
	// Sorted ascending so the property string is deterministic for caching
	// and reproducible test output.
	assert.Equal(t, "aws=v2024-01-01,k8s=v1.30", formatVersionsForProperty(map[string]string{
		"k8s": "v1.30",
		"aws": "v2024-01-01",
	}))
}

func TestFormatVersionsForProperty_LowercasesNamespace(t *testing.T) {
	assert.Equal(t, "k8s=v1.30", formatVersionsForProperty(map[string]string{"K8S": "v1.30"}),
		"namespace is lowercased so ImportsGenerator's pkg-name comparison hits regardless of casing in the source map")
}

func TestFormatVersionsForProperty_DropsBlankEntries(t *testing.T) {
	assert.Equal(t, "k8s=v1.30", formatVersionsForProperty(map[string]string{
		"k8s": "v1.30",
		"":    "v9",
		"aws": "",
	}))
}
