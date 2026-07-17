// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/schema"
)

// versionReadExpr is the in-repo version lookup in schema/PklProject. It reads
// ../../../../version.semver relative to the repo layout, which does not exist
// once the schema is materialized outside the tree — so the materializer swaps
// it for the literal version.
const versionReadExpr = `read("../../../../version.semver").text.trim()`

// coreSchemaDir returns the default materialization directory for the given
// version: <base>/schema/<version>, where <base> is the parent of the plugin
// dir (so ~/.pel/formae/schema/<version> by default, and it honors
// FORMAE_PLUGIN_DIR for free).
func coreSchemaDir(version string) string {
	return filepath.Join(filepath.Dir(defaultPluginDir()), "schema", version)
}

// MaterializeCoreSchema writes the embedded formae core schema plus a
// self-contained PklProject (version inlined) to dir, overwriting any existing
// content. When dir is "" it defaults to coreSchemaDir(version). It returns the
// path to the materialized PklProject, suitable for a local: dependency string.
func MaterializeCoreSchema(dir, version string) (string, error) {
	if dir == "" {
		dir = coreSchemaDir(version)
	}

	// Unconditional overwrite keeps dev builds (same version, changed schema)
	// self-healing and avoids stale-content bugs — the whole tree is tiny.
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("clearing core schema dir %q: %w", dir, err)
	}

	err := fs.WalkDir(coreSchema, "schema", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "schema/")
		target := filepath.Join(dir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return fmt.Errorf("creating %q: %w", filepath.Dir(target), mkErr)
		}
		data, readErr := coreSchema.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if rel == ProjectFile {
			data = []byte(strings.Replace(string(data), versionReadExpr, fmt.Sprintf("%q", version), 1))
		}
		if wErr := os.WriteFile(target, data, 0o644); wErr != nil {
			return fmt.Errorf("writing %q: %w", target, wErr)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("materializing core schema to %q: %w", dir, err)
	}

	return filepath.Join(dir, ProjectFile), nil
}

// CoreSchemaDep returns the formae-core dependency string for a generated
// PklProject. For SchemaLocationLocal it materializes the embedded schema and
// returns local:formae:<PklProject path>; for remote it returns
// pkl.formae@<version> (empty for the 0.0.0 dev build, matching prior behavior
// of suppressing the core dep for unstamped binaries).
func CoreSchemaDep(location schema.SchemaLocation, version string) (string, error) {
	if location == schema.SchemaLocationLocal {
		projPath, err := MaterializeCoreSchema("", version)
		if err != nil {
			return "", err
		}
		return "local:formae:" + projPath, nil
	}
	if version != "0.0.0" {
		return "pkl.formae@" + version, nil
	}
	return "", nil
}
