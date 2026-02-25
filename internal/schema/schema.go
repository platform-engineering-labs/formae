// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package schema

import (
	"errors"
	"fmt"
	"sort"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

// SchemaLocation specifies where to resolve schemas from.
type SchemaLocation string

const (
	// SchemaLocationRemote resolves schemas from the package registry (default).
	SchemaLocationRemote SchemaLocation = "remote"
	// SchemaLocationLocal resolves schemas from locally installed plugins.
	SchemaLocationLocal SchemaLocation = "local"
)

// GenerateSourcesResult captures the outcome of a source code generation operation.
type GenerateSourcesResult struct {
	TargetPath            string
	ProjectPath           string
	ResourceCount         int
	InitializedNewProject bool
	Warnings              []string
}

// SerializeOptions controls how resources are serialized by a schema plugin.
type SerializeOptions struct {
	Schema         string
	Beautify       bool
	Colorize       bool
	Simplified     bool
	SchemaLocation SchemaLocation
}

// ErrFailedToGenerateSources is returned when source code generation fails.
var ErrFailedToGenerateSources = errors.New("failed to generate source code")

// SchemaPlugin defines the interface that schema plugins must implement.
type SchemaPlugin interface {
	Name() string
	FileExtension() string
	SupportsExtract() bool
	FormaeConfig(path string) (*model.Config, error)
	Evaluate(path string, cmd model.Command, mode model.FormaApplyMode, props map[string]string) (*model.Forma, error)
	SerializeForma(resources *model.Forma, options *SerializeOptions) (string, error)
	GenerateSourceCode(forma *model.Forma, targetPath string, includes []string, schemaLocation SchemaLocation) (GenerateSourcesResult, error)
	ProjectInit(path string, include []string, schemaLocation SchemaLocation) error
	ProjectProperties(path string) (map[string]model.Prop, error)
}

// Registry stores schema plugins indexed by name and file extension.
type Registry struct {
	byName      map[string]SchemaPlugin
	byExtension map[string]SchemaPlugin
}

// DefaultRegistry is the package-level registry used by default.
var DefaultRegistry = NewRegistry()

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byName:      make(map[string]SchemaPlugin),
		byExtension: make(map[string]SchemaPlugin),
	}
}

// Register adds a schema plugin to the registry, indexed by both its name and
// file extension.
func (r *Registry) Register(plugin SchemaPlugin) {
	r.byName[plugin.Name()] = plugin
	r.byExtension[plugin.FileExtension()] = plugin
}

// Get returns the schema plugin registered under the given name, or an error if
// no such plugin exists.
func (r *Registry) Get(name string) (SchemaPlugin, error) {
	plugin, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("no schema plugin registered with name %q", name)
	}
	return plugin, nil
}

// GetByFileExtension returns the schema plugin registered for the given file
// extension, or an error if no such plugin exists.
func (r *Registry) GetByFileExtension(ext string) (SchemaPlugin, error) {
	plugin, ok := r.byExtension[ext]
	if !ok {
		return nil, fmt.Errorf("no schema plugin registered for file extension %q", ext)
	}
	return plugin, nil
}

// SupportedSchemas returns a sorted list of all registered schema plugin names.
func (r *Registry) SupportedSchemas() []string {
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SupportedFileExtensions returns a sorted list of all registered file
// extensions.
func (r *Registry) SupportedFileExtensions() []string {
	extensions := make([]string, 0, len(r.byExtension))
	for ext := range r.byExtension {
		extensions = append(extensions, ext)
	}
	sort.Strings(extensions)
	return extensions
}
