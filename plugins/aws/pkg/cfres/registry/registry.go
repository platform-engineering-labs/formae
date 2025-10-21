// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package registry

import (
	"log/slog"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
)

var registry = make(map[string]map[resource.Operation]func(cfg *config.Config) prov.Provisioner)

func Register(name string, operations []resource.Operation, f func(cfg *config.Config) prov.Provisioner) {
	if _, exists := registry[name]; !exists {
		registry[name] = make(map[resource.Operation]func(cfg *config.Config) prov.Provisioner)
	}
	for _, operation := range operations {
		registry[name][operation] = f
	}
}

func Get(name string, operation resource.Operation, cfg *config.Config) prov.Provisioner {
	if !HasProvisioner(name, operation) {
		slog.Error("Provisioner not found in registry", "name", name, "operation", operation, "registry_keys", getRegistryKeys())
		return nil
	}

	provisioner := registry[name][operation](cfg)
	return provisioner
}

func getRegistryKeys() []string {
	var keys []string
	for k := range registry {
		keys = append(keys, k)
	}
	return keys
}

func HasProvisioner(name string, operation resource.Operation) bool {
	_, exists := registry[name][operation]
	return exists
}
