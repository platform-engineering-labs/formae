// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package descriptors

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"runtime"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/descriptors/gen"
)

//go:embed pkl/generated_resources.pkl
var embeddedResourcesData []byte

//go:embed pkl/types.pkl
var embeddedTypesData []byte

type TypeInformation struct {
	Schema                               model.Schema
	ParentResourcesWithMappingProperties map[string][]plugin.ListParameter
	Discoverable                         bool
}

// LoadDescriptors loads resource descriptors from embedded PKL files and returns a map of Type -> Schema
func LoadDescriptors(ctx context.Context) (map[string]TypeInformation, error) {
	// Create a temporary directory for both files
	tmpDir, err := os.MkdirTemp("", "pkl_resources_*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// Write the resources file
	resourcesFile := filepath.Join(tmpDir, "generated_resources.pkl")
	if err = os.WriteFile(resourcesFile, embeddedResourcesData, 0644); err != nil {
		return nil, err
	}

	// Write the types file
	typesFile := filepath.Join(tmpDir, "types.pkl")
	if err = os.WriteFile(typesFile, embeddedTypesData, 0644); err != nil {
		return nil, err
	}

	// Load from the temporary resources file
	resource, err := gen.LoadFromPath(ctx, resourcesFile)
	if err != nil {
		return nil, err
	}

	// Convert to map of Type -> Schema
	typeInformation := make(map[string]TypeInformation)
	for _, resourceType := range resource.Resources {
		var tagsValue string
		if resourceType.GetSchema().GetTags() != nil {
			tagsValue = *resourceType.GetSchema().GetTags()
		}
		hints := make(map[string]model.FieldHint)
		for k, v := range resourceType.GetSchema().GetHints() {
			hints[k] = model.FieldHint{
				CreateOnly: v.GetCreateOnly(),
				Persist:    v.GetPersist(),
				WriteOnly:  v.GetWriteOnly(),
				Required:   v.GetRequired(),
			}
		}
		schema := model.Schema{
			Identifier:       resourceType.GetSchema().GetIdentifier(),
			Tags:             tagsValue,
			Fields:           resourceType.GetSchema().GetFields(),
			Hints:            hints,
			Nonprovisionable: resourceType.GetSchema().GetNonprovisionable(),
		}
		listParameters := make(map[string][]plugin.ListParameter)
		for parentType, mappingProperties := range *resourceType.GetParentResourceTypesWithMappingProperties() {
			listParams := make([]plugin.ListParameter, 0, len(mappingProperties))
			for _, prop := range mappingProperties {
				listParams = append(listParams, plugin.ListParameter{
					ParentProperty: prop.GetParentProperty(),
					ListProperty:   prop.GetListParameter(),
				})
			}
			listParameters[parentType] = listParams
		}
		typeInformation[resourceType.GetType()] = TypeInformation{
			Schema:                               schema,
			ParentResourcesWithMappingProperties: listParameters,
			Discoverable:                         resourceType.GetDiscoverable(),
		}
	}

	return typeInformation, nil
}

// GetResourcesPath returns the absolute path to the generated_resources.pkl file (for development)
func GetResourcesPath() string {
	return filepath.Join(filepath.Dir(getCurrentFile()), "pkl", "generated_resources.pkl")
}

// GetTypesPath returns the absolute path to the types.pkl file (for development)
func GetTypesPath() string {
	return filepath.Join(filepath.Dir(getCurrentFile()), "pkl", "types.pkl")
}

func getCurrentFile() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return currentFile
}

// LoadDescriptorsFromPath loads descriptors from a specific path (for testing or custom paths)
func LoadDescriptorsFromPath(ctx context.Context, path string) (map[string]model.Schema, error) {
	resource, err := gen.LoadFromPath(ctx, path)
	if err != nil {
		return nil, err
	}

	schemas := make(map[string]model.Schema)
	for _, resourceType := range resource.Resources {
		hints := make(map[string]model.FieldHint)
		for k, v := range resourceType.GetSchema().GetHints() {
			hints[k] = model.FieldHint{
				CreateOnly: v.GetCreateOnly(),
				Persist:    v.GetPersist(),
				WriteOnly:  v.GetWriteOnly(),
				Required:   v.GetRequired(),
			}
		}
		schema := model.Schema{
			Identifier: resourceType.GetSchema().GetIdentifier(),
			Tags:       *resourceType.GetSchema().GetTags(),
			//CreateOnly:       resourceType.GetSchema().GetCreateOnly(),
			Fields: resourceType.GetSchema().GetFields(),
			Hints:  hints,
			//Metadata:         resourceType.GetSchema().GetMetadata(),
			Nonprovisionable: resourceType.GetSchema().GetNonprovisionable(),
		}
		schemas[resourceType.GetType()] = schema
	}

	return schemas, nil
}
