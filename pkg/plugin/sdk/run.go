// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package sdk provides the entry point for external plugins.
// Plugin developers should use RunWithManifest from this package to start their plugins.
package sdk

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/descriptors"
)

const (
	// FormaeSchemaPackageURL is the base URL for the formae schema package.
	// The version will be appended at runtime.
	FormaeSchemaPackageURL = "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@"

	// FormaeVersionEnvVar is the environment variable set by the agent when spawning plugins.
	// It contains the formae version to use for schema resolution.
	FormaeVersionEnvVar = "FORMAE_VERSION"
)

// getFormaeVersion returns the formae version to use for schema resolution.
// The FORMAE_VERSION env var is set by the agent when spawning plugins.
func getFormaeVersion() string {
	return os.Getenv(FormaeVersionEnvVar)
}

// RunConfig contains options for starting a plugin with RunWithManifest.
type RunConfig struct {
	// FormaeSchemaPath overrides the formae base schema path.
	// If empty, uses the remote package URL.
	// Only needed for local development of the formae core schemas.
	FormaeSchemaPath string
}

// SetupPlugin reads the manifest and extracts schemas, then wraps the plugin.
// This is useful for testing or when you need to inspect the wrapped plugin
// before starting it.
//
// Schema resolution:
//   - Plugin schema: Located at <plugin-binary-dir>/schema/pkl/PklProject
//   - Formae base schema: Uses remote package URL (unless FormaeSchemaPath is set)
//
// Plugins must be installed via `make install` which places both the binary
// and schema in ~/.pel/formae/plugins/<namespace>/v<version>/
//
// NOTE: The plugin directory structure will be refactored to use <name>
// instead of <namespace> once e2e testing is complete.
func SetupPlugin(ctx context.Context, p plugin.ResourcePlugin, config RunConfig) (plugin.FullResourcePlugin, error) {
	// 1. Find plugin directory (where the binary is running from)
	pluginDir, err := getPluginDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine plugin directory: %w", err)
	}

	// 2. Read manifest
	manifest, err := plugin.ReadManifestFromDir(pluginDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest from %s: %w", pluginDir, err)
	}

	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	// 3. Resolve schema paths
	pluginSchemaPath := filepath.Join(pluginDir, "schema", "pkl", "PklProject")
	if _, err := os.Stat(pluginSchemaPath); err != nil {
		return nil, fmt.Errorf("plugin schema not found at %s (did you run 'make install'?): %w", pluginSchemaPath, err)
	}

	formaeSchemaPath := config.FormaeSchemaPath
	if formaeSchemaPath == "" {
		// Use remote package URL with the formae version from agent (or manifest fallback)
		formaeSchemaPath = FormaeSchemaPackageURL + getFormaeVersion()
	}

	// 4. Build dependencies for schema extraction
	deps := []descriptors.Dependency{
		{Name: "formae", Value: formaeSchemaPath},
		{Name: manifest.Name, Value: pluginSchemaPath},
	}

	// 5. Extract schemas
	typeDescriptors, err := descriptors.ExtractSchemaFromDependencies(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("failed to extract schemas: %w", err)
	}

	// 6. Convert to ResourceDescriptor and Schema map
	resourceDescriptors := make([]plugin.ResourceDescriptor, 0, len(typeDescriptors))
	schemas := make(map[string]model.Schema, len(typeDescriptors))
	for _, td := range typeDescriptors {
		resourceDescriptors = append(resourceDescriptors, plugin.ResourceDescriptor{
			Type:                                     td.Type,
			ParentResourceTypesWithMappingProperties: td.ParentResourceTypesWithMappingProperties,
			Extractable:                              td.Schema.Extractable,
			Discoverable:                             td.Schema.Discoverable,
		})
		schemas[td.Type] = td.Schema
	}

	// 7. Wrap the plugin
	wrapped, err := plugin.WrapPlugin(p, manifest, resourceDescriptors, schemas)
	if err != nil {
		return nil, fmt.Errorf("failed to wrap plugin: %w", err)
	}

	return wrapped, nil
}

// getPluginDir returns the directory containing the running plugin binary.
func getPluginDir() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	return filepath.Dir(execPath), nil
}

// SetupPluginFromDir is like SetupPlugin but allows specifying the plugin directory explicitly.
// This is useful for testing or when running plugins from non-standard locations.
func SetupPluginFromDir(ctx context.Context, p plugin.ResourcePlugin, pluginDir string, config RunConfig) (plugin.FullResourcePlugin, error) {
	// 1. Read manifest
	manifest, err := plugin.ReadManifestFromDir(pluginDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest from %s: %w", pluginDir, err)
	}

	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	// 2. Resolve schema paths
	pluginSchemaPath := filepath.Join(pluginDir, "schema", "pkl", "PklProject")
	if _, err := os.Stat(pluginSchemaPath); err != nil {
		return nil, fmt.Errorf("plugin schema not found at %s: %w", pluginSchemaPath, err)
	}

	formaeSchemaPath := config.FormaeSchemaPath
	if formaeSchemaPath == "" {
		// Use remote package URL with the formae version from agent (or manifest fallback)
		formaeSchemaPath = FormaeSchemaPackageURL + getFormaeVersion()
	}

	// 3. Build dependencies for schema extraction
	deps := []descriptors.Dependency{
		{Name: "formae", Value: formaeSchemaPath},
		{Name: manifest.Name, Value: pluginSchemaPath},
	}

	// 4. Extract schemas
	typeDescriptors, err := descriptors.ExtractSchemaFromDependencies(ctx, deps)
	if err != nil {
		return nil, fmt.Errorf("failed to extract schemas: %w", err)
	}

	// 5. Convert to ResourceDescriptor and Schema map
	resourceDescriptors := make([]plugin.ResourceDescriptor, 0, len(typeDescriptors))
	schemas := make(map[string]model.Schema, len(typeDescriptors))
	for _, td := range typeDescriptors {
		resourceDescriptors = append(resourceDescriptors, plugin.ResourceDescriptor{
			Type:                                     td.Type,
			ParentResourceTypesWithMappingProperties: td.ParentResourceTypesWithMappingProperties,
			Extractable:                              td.Schema.Extractable,
			Discoverable:                             td.Schema.Discoverable,
		})
		schemas[td.Type] = td.Schema
	}

	// 6. Wrap the plugin
	wrapped, err := plugin.WrapPlugin(p, manifest, resourceDescriptors, schemas)
	if err != nil {
		return nil, fmt.Errorf("failed to wrap plugin: %w", err)
	}

	return wrapped, nil
}

// RunWithManifest starts a plugin using the manifest and schemas.
// This is the recommended entry point for external plugins.
//
// The plugin must be installed via `make install` which places:
//   - Binary at ~/.pel/formae/plugins/<namespace>/v<version>/<binary>
//   - Schema at ~/.pel/formae/plugins/<namespace>/v<version>/schema/pkl/
//   - Manifest at ~/.pel/formae/plugins/<namespace>/v<version>/formae-plugin.pkl
//
// For built-in plugins that implement FullResourcePlugin directly, use plugin.Run() instead.
func RunWithManifest(p plugin.ResourcePlugin, config RunConfig) {
	ctx := context.Background()
	wrapped, err := SetupPlugin(ctx, p, config)
	if err != nil {
		log.Fatalf("Failed to setup plugin: %v", err)
	}

	// Start the plugin
	plugin.Run(wrapped)
}
