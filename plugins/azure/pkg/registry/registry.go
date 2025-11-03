// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package registry

import (
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/client"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/config"
	"github.com/platform-engineering-labs/formae/plugins/azure/pkg/prov"
)

var registry = &Registry{
	provisioners: make(map[string]func(client *client.Client, config *config.Config) prov.Provisioner),
	descriptors:  make(map[string]plugin.ResourceDescriptor),
	schemas:      make(map[string]model.Schema),
}

type Registry struct {
	provisioners map[string]func(client *client.Client, config *config.Config) prov.Provisioner
	descriptors  map[string]plugin.ResourceDescriptor
	schemas      map[string]model.Schema
}

// Register registers a resource type with its provisioner
func Register(name string, descriptor plugin.ResourceDescriptor, schema model.Schema,
	f func(client *client.Client, config *config.Config) prov.Provisioner) {
	if _, exists := registry.provisioners[name]; !exists {
		registry.provisioners[name] = f
		registry.descriptors[name] = descriptor
		registry.schemas[name] = schema
	}
}

// Get returns a provisioner for the given resource type
func Get(name string, client *client.Client, config *config.Config) prov.Provisioner {
	return registry.provisioners[name](client, config)
}

// HasProvisioner checks if a provisioner exists for the resource type
func HasProvisioner(name string) bool {
	_, exists := registry.provisioners[name]
	return exists
}

// SupportedResources returns all registered resource descriptors
func SupportedResources() []plugin.ResourceDescriptor {
	var supported []plugin.ResourceDescriptor
	for _, resource := range registry.descriptors {
		supported = append(supported, resource)
	}
	return supported
}

// SchemaForResourceType returns the schema for a resource type
func SchemaForResourceType(resourceType string) model.Schema {
	return registry.schemas[resourceType]
}
