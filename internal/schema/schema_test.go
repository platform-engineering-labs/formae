// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package schema

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSchemaPlugin implements SchemaPlugin with only Name() and FileExtension()
// returning useful values; all other methods panic since they are not needed for
// registry tests.
type mockSchemaPlugin struct {
	name          string
	fileExtension string
}

func (m *mockSchemaPlugin) Name() string          { return m.name }
func (m *mockSchemaPlugin) FileExtension() string { return m.fileExtension }
func (m *mockSchemaPlugin) SupportsExtract() bool { panic("not implemented") }
func (m *mockSchemaPlugin) FormaeConfig(_ string) (*model.Config, error) {
	panic("not implemented")
}
func (m *mockSchemaPlugin) Evaluate(_ string, _ model.Command, _ model.FormaApplyMode, _ map[string]string) (*model.Forma, error) {
	panic("not implemented")
}
func (m *mockSchemaPlugin) SerializeForma(_ *model.Forma, _ *SerializeOptions) (string, error) {
	panic("not implemented")
}
func (m *mockSchemaPlugin) GenerateSourceCode(_ *model.Forma, _ string, _ []string, _ SchemaLocation) (GenerateSourcesResult, error) {
	panic("not implemented")
}
func (m *mockSchemaPlugin) ProjectInit(_ string, _ []string, _ SchemaLocation) error {
	panic("not implemented")
}
func (m *mockSchemaPlugin) ProjectProperties(_ string) (map[string]model.Prop, error) {
	panic("not implemented")
}

func TestRegistry_RegisterAndGetByName(t *testing.T) {
	r := NewRegistry()
	plugin := &mockSchemaPlugin{name: "pkl", fileExtension: ".pkl"}

	r.Register(plugin)

	got, err := r.Get("pkl")
	require.NoError(t, err)
	assert.Equal(t, plugin, got)
}

func TestRegistry_RegisterAndGetByFileExtension(t *testing.T) {
	r := NewRegistry()
	plugin := &mockSchemaPlugin{name: "pkl", fileExtension: ".pkl"}

	r.Register(plugin)

	got, err := r.GetByFileExtension(".pkl")
	require.NoError(t, err)
	assert.Equal(t, plugin, got)
}

func TestRegistry_GetUnknownNameReturnsError(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestRegistry_GetByUnknownExtensionReturnsError(t *testing.T) {
	r := NewRegistry()

	_, err := r.GetByFileExtension(".xyz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".xyz")
}

func TestRegistry_SupportedSchemasReturnsSortedNames(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSchemaPlugin{name: "yaml", fileExtension: ".yaml"})
	r.Register(&mockSchemaPlugin{name: "json", fileExtension: ".json"})
	r.Register(&mockSchemaPlugin{name: "pkl", fileExtension: ".pkl"})

	schemas := r.SupportedSchemas()
	assert.Equal(t, []string{"json", "pkl", "yaml"}, schemas)
}

func TestRegistry_SupportedFileExtensionsReturnsSortedExtensions(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSchemaPlugin{name: "yaml", fileExtension: ".yaml"})
	r.Register(&mockSchemaPlugin{name: "json", fileExtension: ".json"})
	r.Register(&mockSchemaPlugin{name: "pkl", fileExtension: ".pkl"})

	extensions := r.SupportedFileExtensions()
	assert.Equal(t, []string{".json", ".pkl", ".yaml"}, extensions)
}

func TestRegistry_RegisterMultipleAndRetrieveEach(t *testing.T) {
	r := NewRegistry()
	pkl := &mockSchemaPlugin{name: "pkl", fileExtension: ".pkl"}
	json := &mockSchemaPlugin{name: "json", fileExtension: ".json"}
	yaml := &mockSchemaPlugin{name: "yaml", fileExtension: ".yaml"}

	r.Register(pkl)
	r.Register(json)
	r.Register(yaml)

	got, err := r.Get("pkl")
	require.NoError(t, err)
	assert.Equal(t, pkl, got)

	got, err = r.Get("json")
	require.NoError(t, err)
	assert.Equal(t, json, got)

	got, err = r.Get("yaml")
	require.NoError(t, err)
	assert.Equal(t, yaml, got)

	got, err = r.GetByFileExtension(".pkl")
	require.NoError(t, err)
	assert.Equal(t, pkl, got)

	got, err = r.GetByFileExtension(".json")
	require.NoError(t, err)
	assert.Equal(t, json, got)

	got, err = r.GetByFileExtension(".yaml")
	require.NoError(t, err)
	assert.Equal(t, yaml, got)
}
