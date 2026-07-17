// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaterializeCoreSchema_WritesFilesAndInlinesVersion(t *testing.T) {
	dir := t.TempDir()
	projPath, err := MaterializeCoreSchema(dir, "1.2.3")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "PklProject"), projPath)

	// Core schema files land at the package root so @formae/forma.pkl resolves.
	for _, f := range []string{"forma.pkl", "formae.pkl", "PklProject", "ext/URI.pkl"} {
		_, statErr := os.Stat(filepath.Join(dir, f))
		assert.NoError(t, statErr, "expected %s to be materialized", f)
	}

	// PklProject must carry the literal version — the in-repo read() breaks
	// outside the repo tree.
	proj, err := os.ReadFile(projPath)
	require.NoError(t, err)
	assert.Contains(t, string(proj), `version = "1.2.3"`)
	assert.NotContains(t, string(proj), "read(")

	// tests/ and debug/ are dev-only; they must not be embedded.
	_, statErr := os.Stat(filepath.Join(dir, "tests"))
	assert.True(t, os.IsNotExist(statErr), "tests/ must not be materialized")
}

func TestMaterializeCoreSchema_IdempotentOverwrite(t *testing.T) {
	dir := t.TempDir()
	_, err := MaterializeCoreSchema(dir, "1.2.3")
	require.NoError(t, err)

	// Corrupt a materialized file, re-materialize, confirm it is restored.
	formaPath := filepath.Join(dir, "forma.pkl")
	require.NoError(t, os.WriteFile(formaPath, []byte("// clobbered\n"), 0644))

	_, err = MaterializeCoreSchema(dir, "1.2.3")
	require.NoError(t, err)

	restored, err := os.ReadFile(formaPath)
	require.NoError(t, err)
	assert.NotEqual(t, "// clobbered\n", string(restored))
	assert.Contains(t, string(restored), "formae.pkl")
}

func TestMaterializeCoreSchema_UnwritableDirErrorsWithPath(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	parent := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0644))
	target := filepath.Join(parent, "schema", "1.0.0")

	_, err := MaterializeCoreSchema(target, "1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), target)
}

func TestCoreSchemaDep_Remote(t *testing.T) {
	dep, err := CoreSchemaDep(schema.SchemaLocationRemote, "0.87.0")
	require.NoError(t, err)
	assert.Equal(t, "pkl.formae@0.87.0", dep)
}

func TestCoreSchemaDep_RemoteDevBuildSkips(t *testing.T) {
	dep, err := CoreSchemaDep(schema.SchemaLocationRemote, "0.0.0")
	require.NoError(t, err)
	assert.Equal(t, "", dep)
}

func TestCoreSchemaDep_Local(t *testing.T) {
	// Redirect the default schema dir into a temp tree via FORMAE_PLUGIN_DIR.
	base := t.TempDir()
	t.Setenv("FORMAE_PLUGIN_DIR", filepath.Join(base, "plugins"))

	dep, err := CoreSchemaDep(schema.SchemaLocationLocal, "0.87.0")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(dep, "local:formae:"), "got %q", dep)

	path := strings.TrimPrefix(dep, "local:formae:")
	assert.Equal(t, filepath.Join(base, "schema", "0.87.0", "PklProject"), path)
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr)
}

func TestCoreSchemaDep_LocalDevBuildMaterializes(t *testing.T) {
	base := t.TempDir()
	t.Setenv("FORMAE_PLUGIN_DIR", filepath.Join(base, "plugins"))

	dep, err := CoreSchemaDep(schema.SchemaLocationLocal, "0.0.0")
	require.NoError(t, err)
	assert.Equal(t, "local:formae:"+filepath.Join(base, "schema", "0.0.0", "PklProject"), dep)
}
