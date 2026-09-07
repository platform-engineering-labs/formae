// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

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

// Drives the whole serialize pipeline over a plugin laid out the way a
// versioned plugin actually ships: per-version subtrees (v1.0/, v1.1/) next to
// a deliberately version-independent resource subtree (helm/). Pinning a
// version narrows the import glob, and a root-level subdirectory used to fall
// outside every glob — extract died on `Cannot find key`.
//
// Self-contained: the fixture schema lives in testdata/versionedschema/ and is
// staged into a plugin install layout under t.TempDir(), so no installed
// plugin, no cluster, no network. Deps are all `local:` so nothing resolves
// against the hub.

// installFakeVerPlugin stages testdata/versionedschema/ as an installed plugin
// and returns (plugin root to scan, the staged PklProject path).
func installFakeVerPlugin(t *testing.T) (string, string) {
	t.Helper()

	formaeProject, err := filepath.Abs(filepath.Join("schema", "PklProject"))
	require.NoError(t, err)
	require.FileExists(t, formaeProject)

	root := t.TempDir()
	pklDir := filepath.Join(root, "fakever", "v0.1.1", "schema", "pkl")
	require.NoError(t, os.MkdirAll(pklDir, 0755))

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "fakever", "v0.1.1", "formae-plugin.pkl"),
		[]byte("namespace = \"FakeVer\"\n"), 0644))

	// Written here rather than checked in: the formae dep has to be an absolute
	// path, since the fixture is copied out of the source tree.
	require.NoError(t, os.WriteFile(
		filepath.Join(pklDir, "PklProject"),
		[]byte(fmt.Sprintf(`amends "pkl:Project"

package {
  name = "fakever"
  baseUri = "package://fake.local/fakever"
  version = "1.0.0"
  packageZipUrl = "https://fake.local/fakever@1.0.0.zip"
}

dependencies {
    ["formae"] = import(%q)
}
`, formaeProject)), 0644))

	require.NoError(t, os.CopyFS(pklDir, os.DirFS(filepath.Join("testdata", "versionedschema"))))

	return root, filepath.Join(pklDir, "PklProject")
}

func fakeVerForma() *model.Forma {
	return &model.Forma{
		Stacks: []model.Stack{{Label: "default"}},
		Targets: []model.Target{{
			Label:     "fv",
			Namespace: "FakeVer",
			Config:    json.RawMessage(`{"ApiVersion":"v1.1"}`),
		}},
		// One resource per glob shape the narrowed import has to cover:
		//   widget     v1.1/widget.pkl        version dir, flat
		//   gadget     v1.1/core/gadget.pkl   version dir, nested
		//   release    helm/release.pkl       non-version dir, flat
		//   repository helm/charts/*.pkl      non-version dir, nested
		Resources: []model.Resource{
			{
				Label: "my-widget", Type: "FakeVer::Core::Widget",
				Stack: "default", Target: "fv", NativeID: "w-1",
				Properties: json.RawMessage(`{"Name":"my-widget","Replicas":2}`),
			},
			{
				Label: "my-gadget", Type: "FakeVer::Core::Gadget",
				Stack: "default", Target: "fv", NativeID: "g-1",
				Properties: json.RawMessage(`{"Name":"my-gadget"}`),
			},
			{
				Label: "my-release", Type: "FakeVer::Helm::Release",
				Stack: "default", Target: "fv", NativeID: "r-1",
				Properties: json.RawMessage(`{"Name":"my-release","Chart":"nginx"}`),
			},
			{
				Label: "my-repo", Type: "FakeVer::Helm::Repository",
				Stack: "default", Target: "fv", NativeID: "repo-1",
				Properties: json.RawMessage(`{"Name":"my-repo","Url":"https://charts.example.com"}`),
			},
		},
	}
}

// The regression this whole change exists for: a resource whose module lives in
// a root-level subdirectory of a version-pinned plugin must still extract.
func TestSerializeForma_PinnedVersion_ResolvesVersionIndependentSubtree(t *testing.T) {
	pluginRoot, pklProject := installFakeVerPlugin(t)

	formaeProject, err := filepath.Abs(filepath.Join("schema", "PklProject"))
	require.NoError(t, err)

	out, err := PKL{}.SerializeForma(fakeVerForma(), &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginRoot,
		Dependencies: []string{
			"local:formae:" + formaeProject,
			"local:fakever:" + pklProject,
		},
	})
	require.NoError(t, err,
		"a resource in a root-level subdir of a version-pinned plugin must resolve; "+
			"before the fix this failed with `Cannot find key \"FakeVer::Helm::Release\"`")

	t.Logf("generated forma:\n%s", out)

	// Each label proves one glob shape actually resolved a schema module: a
	// missing shape doesn't degrade quietly, it fails the whole evaluation with
	// `Cannot find key`, so reaching these assertions is already most of the
	// proof. They pin which resource covers which shape.
	for _, label := range []string{"my-widget", "my-gadget", "my-release", "my-repo"} {
		assert.Contains(t, out, label)
	}
	assert.Contains(t, out, "nginx",
		"properties render too, so helm/release.pkl was the module used — not a stub")
	assert.Contains(t, out, "charts.example.com",
		"and likewise for the nested helm/charts/repository.pkl")
}

// The pinned subtree still wins: `Replicas` exists only on v1.1's Widget, so
// rendering it proves the v1.0 copy did not shadow it.
func TestSerializeForma_PinnedVersion_KeepsNarrowingAcrossVersionCollision(t *testing.T) {
	pluginRoot, pklProject := installFakeVerPlugin(t)

	formaeProject, err := filepath.Abs(filepath.Join("schema", "PklProject"))
	require.NoError(t, err)

	out, err := PKL{}.SerializeForma(fakeVerForma(), &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginRoot,
		Dependencies: []string{
			"local:formae:" + formaeProject,
			"local:fakever:" + pklProject,
		},
	})
	require.NoError(t, err)

	assert.Contains(t, out, `import "@fakever/v1.1/widget.pkl"`,
		"the pinned subtree supplies Widget; v1.0/widget.pkl declares the same type "+
			"and must not be imported")
	assert.NotContains(t, out, "@fakever/v1.0/",
		"nothing from the unpinned version may be imported")
	assert.Contains(t, out, "replicas = 2",
		"replicas exists only on v1.1's Widget; had the v1.0 copy won "+
			"ResourcesGenerator's last-writer-wins fold, the field would be dropped")
}
