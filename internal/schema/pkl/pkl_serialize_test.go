// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"encoding/json"
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

func TestResolveSchemaVersions_TargetStampWinsOverFilesystemDefault(t *testing.T) {
	tmpDir := installVersionedPluginForSerialize(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Targets:   []model.Target{{Namespace: "K8S", Config: json.RawMessage(`{"ApiVersion":"v1.27"}`)}},
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{LocalPluginDir: tmpDir})
	assert.Equal(t, "v1.27", got["k8s"],
		"per-target stamp pins the version; filesystem default is the fallback")
}

func TestResolveSchemaVersions_FilesystemDefaultUsedWhenNoStamp(t *testing.T) {
	tmpDir := installVersionedPluginForSerialize(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Resources: []model.Resource{{Type: "K8S::Core::Pod"}},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{LocalPluginDir: tmpDir})
	assert.Equal(t, "v1.34", got["k8s"],
		"no target stamp → lexically-highest v*/ subdir wins")
}

func TestResolveSchemaVersions_NamespaceWithNoSourceOmitted(t *testing.T) {
	forma := &model.Forma{
		Resources: []model.Resource{{Type: "AWS::S3::Bucket"}},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{})
	assert.Nil(t, got, "no target stamp, no installed plugin → nil so ImportsGenerator falls back to unrestricted glob")
}

func TestResolveSchemaVersions_TargetStampOnlyForMatchingNamespace(t *testing.T) {
	// Stamp lives on the K8S target; AWS resources in the same Forma
	// must not pick it up.
	tmpDir := installVersionedPluginForSerialize(t, "K8S", "k8s",
		[]string{"v1.21", "v1.30", "v1.34"})
	forma := &model.Forma{
		Targets: []model.Target{{Namespace: "K8S", Config: json.RawMessage(`{"ApiVersion":"v1.27"}`)}},
		Resources: []model.Resource{
			{Type: "K8S::Core::Pod"},
			{Type: "AWS::S3::Bucket"},
		},
	}
	got := resolveSchemaVersions(forma, &schema.SerializeOptions{LocalPluginDir: tmpDir})
	assert.Equal(t, "v1.27", got["k8s"])
	_, awsHas := got["aws"]
	assert.False(t, awsHas, "no AWS plugin install + no AWS target stamp → no entry")
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

// Regression: when caller pre-resolves an include as `local:k8s:<path>` (e.g.
// resolveIncludes via resolver.WithLocalSchemas), the previous swap pass
// failed to recognize it and appended a duplicate `local:k8s:<path>` entry,
// producing `Duplicate definition of member "k8s"` from `pkl project resolve`.
func TestSwapVersionedDepsToLocal_DoesNotDuplicateExistingLocalEntry(t *testing.T) {
	pluginDir := installVersionedPluginForSerialize(t, "K8S", "k8s", []string{"v1.34"})
	localPath := filepath.Join(pluginDir, "k8s", "v0.1.1", "schema", "pkl", "PklProject")

	includes := []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + localPath,
	}
	versions := map[string]string{"k8s": "v1.34"}
	options := &schema.SerializeOptions{LocalPluginDir: pluginDir}

	got := swapVersionedDepsToLocal(includes, versions, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + localPath,
	}, got, "existing local: entry must pass through without a duplicate appended")
}

// When the include list has a remote `<ns>.<name>@<ver>` entry, the swap pass
// should rewrite it in place to `local:<name>:<path>` (not append).
func TestSwapVersionedDepsToLocal_RewritesRemoteToLocal(t *testing.T) {
	pluginDir := installVersionedPluginForSerialize(t, "K8S", "k8s", []string{"v1.34"})
	expectedPath := filepath.Join(pluginDir, "k8s", "v0.1.1", "schema", "pkl", "PklProject")

	includes := []string{
		"pkl.formae@0.85.0",
		"k8s.k8s@0.1.1",
	}
	versions := map[string]string{"k8s": "v1.34"}
	options := &schema.SerializeOptions{LocalPluginDir: pluginDir}

	got := swapVersionedDepsToLocal(includes, versions, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + expectedPath,
	}, got)
}

// When the namespace is missing from includes entirely, the swap pass must
// append a fresh `local:<name>:<path>` so the temp PklProject can resolve it.
func TestSwapVersionedDepsToLocal_AppendsWhenNamespaceMissing(t *testing.T) {
	pluginDir := installVersionedPluginForSerialize(t, "K8S", "k8s", []string{"v1.34"})
	expectedPath := filepath.Join(pluginDir, "k8s", "v0.1.1", "schema", "pkl", "PklProject")

	includes := []string{"pkl.formae@0.85.0"}
	versions := map[string]string{"k8s": "v1.34"}
	options := &schema.SerializeOptions{LocalPluginDir: pluginDir}

	got := swapVersionedDepsToLocal(includes, versions, options)

	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:k8s:" + expectedPath,
	}, got)
}
