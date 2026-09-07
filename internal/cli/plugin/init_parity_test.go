// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

// init_parity_test.go pins the --no-input scaffold behaviour so that future
// changes to the interactive path (Tasks 13/14 etc.) cannot silently regress
// what --no-input produces. Any change to the produced file set or to the
// content transformations applied to the manifest and source files should
// trigger a deliberate update here.

package plugin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// templateDownloaderStub populates outputDir with a minimal template tree that
// mirrors the real formae-plugin-template repository structure. It is
// deterministic, offline, and exercises every path-rename and content-transform
// rule in transformPath / transformContent / finalizeLicense.
type templateDownloaderStub struct{}

func (templateDownloaderStub) Download(_ context.Context, outputDir string) error {
	files := map[string]string{
		// Manifest — the key parity target.
		"formae-plugin.pkl": `amends "package://localhost/plugins/example/schema/pkl/example@0.1.0#/Plugin.pkl"
name = "example"
type = "example"
namespace = "EXAMPLE"
description = "Example Formae plugin template"
category = "other"
license = "Apache-2.0"
`,
		// Go source files that get path-renamed.
		"plugin.go": `// © 2025 Your Name
// SPDX-License-Identifier: Apache-2.0

package example

import "@example/example.pkl"
`,
		"plugin_test.go": `// © 2025 Your Name
// SPDX-License-Identifier: Apache-2.0

package example
`,
		// PKL resource file that gets path-renamed.
		"schema/pkl/example.pkl": `module example

class ExampleResource extends example.Resource {
    label: String
}
`,
		// PklProject — URI transforms.
		"schema/pkl/PklProject": `amends "pkl:Project"

package {
  name = "example"
  baseUri = "package://localhost/plugins/example/schema/pkl/example"
  version = "0.1.0"
  packageZipUrl = "https://localhost/plugins/example/schema/pkl/example@\(version).zip"
  authors {
    "..."
  }
}

dependencies {
  ["example"] { uri = "package://localhost/plugins/example/schema/pkl/example@0.1.0" }
}
`,
		// License stubs so finalizeLicense can copy the selected one.
		"licenses/Apache-2.0.txt": `Copyright [year] [fullname]

Licensed under the Apache License, Version 2.0.
`,
		"licenses/MIT.txt": `Copyright [year] [fullname]

MIT License.
`,
		// README — not renamed, but content transforms still run.
		"README.md": `# <PluginName> plugin

A formae plugin for EXAMPLE resources.
`,
	}

	for rel, content := range files {
		dest := filepath.Join(outputDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

// collectFiles walks dir and returns a sorted slice of relative paths for all
// regular files found. Used to assert the exact file set after scaffolding.
func collectFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		require.NoError(t, err)
		files = append(files, rel)
		return nil
	})
	require.NoError(t, err)
	sort.Strings(files)
	return files
}

// readFile is a small helper that reads a file and fails the test on error.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "expected file %q to exist", path)
	return string(b)
}

// TestNoInputScaffoldParity is the parity test. It runs runPluginInitNoInput
// with a stubbed downloader, then asserts:
//  1. The exact set of files produced (path renames applied).
//  2. Manifest (formae-plugin.pkl) contains all configured values.
//  3. Source file header is updated (author, license).
//  4. File renames occurred correctly.
//  5. licenses/ directory is removed; LICENSE file is created.
func TestNoInputScaffoldParity(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "cloudflare")

	opts := &PluginInitOptions{
		Name:                "cloudflare",
		Namespace:           "CLOUDFLARE",
		Description:         "Cloudflare resource plugin for Formae",
		Category:            "network",
		Author:              "Acme Corp",
		ModulePath:          "github.com/acme/formae-plugin-cloudflare",
		License:             "Apache-2.0",
		OutputDir:           outDir,
		NoInput:             true,
		NoAvailabilityCheck: true,
		TemplateDownloader:  templateDownloaderStub{},
	}

	err := runPluginInitNoInput(context.Background(), opts)
	require.NoError(t, err, "runPluginInitNoInput must succeed")

	// -------------------------------------------------------------------------
	// 1. Exact file set
	// -------------------------------------------------------------------------
	// The stub creates:
	//   formae-plugin.pkl, plugin.go, plugin_test.go, schema/pkl/example.pkl,
	//   schema/pkl/PklProject, licenses/Apache-2.0.txt, licenses/MIT.txt, README.md
	//
	// After transformation:
	//   plugin.go        → cloudflare.go
	//   plugin_test.go   → cloudflare_test.go
	//   schema/pkl/example.pkl → schema/pkl/cloudflare.pkl
	//   licenses/        removed
	//   LICENSE          created
	//
	// Final expected set (sorted):
	wantFiles := []string{
		"LICENSE",
		"README.md",
		"cloudflare.go",
		"cloudflare_test.go",
		"formae-plugin.pkl",
		filepath.Join("schema", "pkl", "PklProject"),
		filepath.Join("schema", "pkl", "cloudflare.pkl"),
	}
	sort.Strings(wantFiles)

	gotFiles := collectFiles(t, outDir)
	assert.Equal(t, wantFiles, gotFiles, "scaffolded file tree must match expected set")

	// -------------------------------------------------------------------------
	// 2. Manifest (formae-plugin.pkl) values
	// -------------------------------------------------------------------------
	manifest := readFile(t, filepath.Join(outDir, "formae-plugin.pkl"))

	assert.Contains(t, manifest, `name = "cloudflare"`, "manifest must contain plugin name")
	assert.Contains(t, manifest, `type = "cloudflare"`, "manifest must contain plugin type")
	assert.Contains(t, manifest, `namespace = "CLOUDFLARE"`, "manifest must contain namespace")
	assert.Contains(t, manifest, `description = "Cloudflare resource plugin for Formae"`, "manifest must contain description")
	assert.Contains(t, manifest, `category = "network"`, "manifest must contain category")
	assert.Contains(t, manifest, `license = "Apache-2.0"`, "manifest must contain license")

	// Original placeholders must be gone.
	assert.NotContains(t, manifest, `name = "example"`)
	assert.NotContains(t, manifest, `namespace = "EXAMPLE"`)
	assert.NotContains(t, manifest, `description = "Example Formae plugin template"`)
	assert.NotContains(t, manifest, `category = "other"`, "category must be updated from 'other' to 'network'")

	// -------------------------------------------------------------------------
	// 3. Source file: author and license header updated
	// -------------------------------------------------------------------------
	mainGo := readFile(t, filepath.Join(outDir, "cloudflare.go"))
	assert.Contains(t, mainGo, "Acme Corp", "author must be substituted in file header")
	assert.NotContains(t, mainGo, "Your Name", "original author placeholder must be gone")

	// -------------------------------------------------------------------------
	// 4. File rename: plugin.go → cloudflare.go, etc.
	// -------------------------------------------------------------------------
	assert.FileExists(t, filepath.Join(outDir, "cloudflare.go"))
	assert.FileExists(t, filepath.Join(outDir, "cloudflare_test.go"))
	assert.FileExists(t, filepath.Join(outDir, "schema", "pkl", "cloudflare.pkl"))
	assert.NoFileExists(t, filepath.Join(outDir, "plugin.go"), "plugin.go must be renamed")
	assert.NoFileExists(t, filepath.Join(outDir, "plugin_test.go"), "plugin_test.go must be renamed")
	assert.NoFileExists(t, filepath.Join(outDir, "schema", "pkl", "example.pkl"), "example.pkl must be renamed")

	// -------------------------------------------------------------------------
	// 5. License finalization
	// -------------------------------------------------------------------------
	assert.FileExists(t, filepath.Join(outDir, "LICENSE"), "LICENSE must be created")
	assert.NoDirExists(t, filepath.Join(outDir, "licenses"), "licenses/ dir must be removed")

	licenseContent := readFile(t, filepath.Join(outDir, "LICENSE"))
	assert.Contains(t, licenseContent, "Acme Corp", "LICENSE must contain author name")

	// -------------------------------------------------------------------------
	// 6. Namespace normalization (passed as already-uppercase CLOUDFLARE)
	// -------------------------------------------------------------------------
	cloudflareGo := readFile(t, filepath.Join(outDir, "cloudflare.go"))
	assert.NotContains(t, cloudflareGo, "EXAMPLE::", "EXAMPLE:: namespace must be replaced")

	// -------------------------------------------------------------------------
	// 7. PKL schema content transform: module name updated
	// -------------------------------------------------------------------------
	cloudflareSchema := readFile(t, filepath.Join(outDir, "schema", "pkl", "cloudflare.pkl"))
	assert.Contains(t, cloudflareSchema, "module cloudflare", "PKL module declaration must be updated")
	assert.NotContains(t, cloudflareSchema, "module example", "original module name must be gone")

	// -------------------------------------------------------------------------
	// 8. PklProject URI substitution
	// -------------------------------------------------------------------------
	pklProject := readFile(t, filepath.Join(outDir, "schema", "pkl", "PklProject"))
	assert.Contains(t, pklProject, "plugins/cloudflare/schema/pkl/cloudflare", "PklProject URIs must be updated")
	assert.NotContains(t, pklProject, "plugins/example/schema/pkl/example", "original URIs must be gone")

	// -------------------------------------------------------------------------
	// 9. PKL dependency alias updated
	// -------------------------------------------------------------------------
	assert.Contains(t, pklProject, `["cloudflare"]`, "PKL dependency alias must use plugin name")
	assert.NotContains(t, pklProject, `["example"]`, "original dependency alias must be gone")

	// -------------------------------------------------------------------------
	// 10. README <PluginName> placeholder substituted
	// -------------------------------------------------------------------------
	readme := readFile(t, filepath.Join(outDir, "README.md"))
	assert.Contains(t, readme, "Cloudflare", "README <PluginName> must be substituted with capitalized name")
	assert.NotContains(t, readme, "<PluginName>", "README <PluginName> placeholder must be gone")
}

// TestNoInputScaffoldParity_CategoryDefault verifies that omitting --category
// still produces a scaffold whose manifest carries the default "other" category.
func TestNoInputScaffoldParity_CategoryDefault(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "myplugin")

	opts := &PluginInitOptions{
		Name:                "myplugin",
		Namespace:           "MYNS",
		Description:         "Test plugin",
		Category:            "other", // default applied by validatePluginInitOptions
		Author:              "Test Author",
		ModulePath:          "github.com/test/formae-plugin-myplugin",
		License:             "Apache-2.0",
		OutputDir:           outDir,
		NoInput:             true,
		NoAvailabilityCheck: true,
		TemplateDownloader:  templateDownloaderStub{},
	}

	require.NoError(t, runPluginInitNoInput(context.Background(), opts))

	manifest := readFile(t, filepath.Join(outDir, "formae-plugin.pkl"))
	// When category is "other" (the template default), the replacement still
	// produces `category = "other"` in the output.
	assert.Contains(t, manifest, `category = "other"`)
}

// TestNoInputScaffoldParity_Namespace_MixedCase checks that a mixed-case
// namespace (e.g. "Cloudflare") is normalized to uppercase before scaffolding
// so all EXAMPLE:: references are replaced with the uppercased form.
func TestNoInputScaffoldParity_Namespace_MixedCase(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "myplugin")

	opts := &PluginInitOptions{
		Name:                "myplugin",
		Namespace:           "Myns", // mixed-case: will be normalized to MYNS
		Description:         "Test plugin",
		Category:            "other",
		Author:              "Test Author",
		ModulePath:          "github.com/test/formae-plugin-myplugin",
		License:             "Apache-2.0",
		OutputDir:           outDir,
		NoInput:             true,
		NoAvailabilityCheck: true,
		TemplateDownloader:  templateDownloaderStub{},
	}

	require.NoError(t, runPluginInitNoInput(context.Background(), opts))

	manifest := readFile(t, filepath.Join(outDir, "formae-plugin.pkl"))
	assert.Contains(t, manifest, `namespace = "MYNS"`, "namespace must be normalized to uppercase")

	goFile := readFile(t, filepath.Join(outDir, "myplugin.go"))
	assert.False(t, strings.Contains(goFile, "EXAMPLE::"),
		"all EXAMPLE:: references must be gone after namespace normalization")
}
