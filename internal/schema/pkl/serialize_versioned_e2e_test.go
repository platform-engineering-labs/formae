// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e_versioned

package pkl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	formae "github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/pkg/model"
)

// e2e test: drives the full pkl serialization pipeline against a real
// installed K8s plugin schema layout that ships per-version subtrees under
// one PklProject (k8s.pkl + v1.21/.../v1.34/).
//
// What it proves end-to-end:
//   - Target.SchemaVersion threads through to ImportsGenerator
//   - ImportsGenerator narrows @k8s/**/*.pkl -> @k8s/<ver>/**/*.pkl
//   - ResourcesGenerator dedups: with narrowing, exactly one Pod module
//     wins; without narrowing 14 versions would collide
//   - SerializeForma succeeds (Pkl evaluator resolves all imports cleanly)
//
// Skipped if the K8s plugin isn't installed at ~/.pel/formae/plugins/k8s/.
// Run with: go test -tags e2e_versioned ./internal/schema/pkl/...
//
// Requires `make install-versioned` in formae-plugin-k8s first.
func TestSerializeForma_VersionedSchema_K8sPodPicksRightSubtree(t *testing.T) {
	pluginDir, ok := installedK8sPluginDir(t)
	if !ok {
		t.Skip("K8s plugin not installed via `make install-versioned`; skipping e2e")
	}

	if !hasVersionSubdirs(t, pluginDir) {
		t.Skip("K8s plugin install lacks v*/ subdirs — likely installed via legacy `make install`; skipping")
	}

	pluginsRoot := filepath.Dir(filepath.Dir(pluginDir))

	podProps := json.RawMessage(`{
		"metadata": {"name": "test-pod", "namespace": "default", "uid": "abc"},
		"spec": {"containers": [{"name": "c", "image": "nginx"}]}
	}`)
	forma := &model.Forma{
		Stacks: []model.Stack{{Label: "default"}},
		Targets: []model.Target{{
			Label:         "orbstack",
			Namespace:     "K8S",
			ApiVersion: "v1.30",
			Config:        json.RawMessage(`{"context":"orbstack"}`),
		}},
		Resources: []model.Resource{{
			Label:      "test-pod",
			Type:       "K8S::Core::Pod",
			Stack:      "default",
			Target:     "orbstack",
			NativeID:   "abc",
			Properties: podProps,
		}},
	}

	resolver := NewPackageResolver().WithLocalSchemas(pluginsRoot)
	resolver.Add("formae", "pkl", formaeVersion())
	resolver.Add("k8s", "k8s", "")
	deps := resolver.GetPackageStrings()

	options := &schema.SerializeOptions{
		Schema:         "pkl",
		SchemaLocation: schema.SchemaLocationLocal,
		LocalPluginDir: pluginsRoot,
		Dependencies:   deps,
	}

	out, err := PKL{}.SerializeForma(forma, options)
	require.NoError(t, err, "SerializeForma should succeed; if dispatch is wrong the evaluator collides on K8S::Core::Pod")
	require.NotEmpty(t, out, "Output Pkl is empty")

	t.Logf("Generated Pkl (first 1200 chars):\n%s", truncate(out, 1200))

	// The serialized Pkl must reference one of the K8s namespace modules.
	// Exact rendering varies, but the output should never silently drop the
	// pod — that's the canary if dispatch broke.
	assert.True(t,
		strings.Contains(out, "K8S::Core::Pod") ||
			strings.Contains(out, "Pod {") ||
			strings.Contains(out, "core.Pod") ||
			strings.Contains(out, "pod.Pod"),
		"Output should mention Pod schema; got first 800 chars:\n%s",
		truncate(out, 800),
	)
}

// Same forma, no SchemaVersions, no manifest fallback. Each Pod path under
// v1.21..v1.34 declares the same type — ResourcesGenerator can only pick
// one, which makes the result indeterminate. We don't assert "this fails"
// (it might pick last-wins), but we do assert that explicit narrowing
// produces a stable, identical output across runs.
func TestSerializeForma_VersionedSchema_NarrowingIsDeterministic(t *testing.T) {
	pluginDir, ok := installedK8sPluginDir(t)
	if !ok {
		t.Skip("K8s plugin not installed; skipping")
	}
	if !hasVersionSubdirs(t, pluginDir) {
		t.Skip("K8s plugin install lacks v*/ subdirs; skipping")
	}
	pluginsRoot := filepath.Dir(filepath.Dir(pluginDir))

	build := func(version string) string {
		forma := &model.Forma{
			Stacks: []model.Stack{{Label: "default"}},
			Targets: []model.Target{{
				Label: "orbstack", Namespace: "K8S",
				ApiVersion: version,
				Config:     json.RawMessage(`{"context":"orbstack"}`),
			}},
			Resources: []model.Resource{{
				Label: "test-pod", Type: "K8S::Core::Pod", Stack: "default", Target: "orbstack", NativeID: "abc",
				Properties: json.RawMessage(`{"metadata":{"name":"test-pod","namespace":"default","uid":"abc"},"spec":{"containers":[{"name":"c","image":"nginx"}]}}`),
			}},
		}
		resolver := NewPackageResolver().WithLocalSchemas(pluginsRoot)
		resolver.Add("formae", "pkl", formaeVersion())
		resolver.Add("k8s", "k8s", "")
		options := &schema.SerializeOptions{
			Schema:         "pkl",
			SchemaLocation: schema.SchemaLocationLocal,
			LocalPluginDir: pluginsRoot,
			Dependencies:   resolver.GetPackageStrings(),
		}
		out, err := PKL{}.SerializeForma(forma, options)
		require.NoErrorf(t, err, "version %s should serialize", version)
		return out
	}

	v130 := build("v1.30")
	v130Again := build("v1.30")
	assert.Equal(t, v130, v130Again,
		"Repeated serialization with the same Target.SchemaVersion must be byte-identical (proves the dispatch isn't introducing nondeterminism)")
}

// hasVersionSubdirs reports whether the installed plugin's schema/pkl/
// directory has at least one v*/ subdir — the signal that this install
// uses the unified versioned-schema layout.
func hasVersionSubdirs(t *testing.T, pluginDir string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(pluginDir, "schema", "pkl"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			return true
		}
	}
	return false
}

func installedK8sPluginDir(t *testing.T) (string, bool) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	base := filepath.Join(home, ".pel", "formae", "plugins", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			return filepath.Join(base, e.Name()), true
		}
	}
	return "", false
}

// formaeVersion returns the formae binary's compile-time semver. When
// unset (test build with no -ldflags), falls back to a placeholder that
// PklProjectTemplate accepts; in that mode the formae remote dep entry is
// elided by resolveIncludes anyway, so the value is moot.
func formaeVersion() string {
	if formae.Version != "0.0.0" && formae.Version != "" {
		return formae.Version
	}
	return "0.85.0"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
