// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"testing"
)

func TestFindPluginByNamespace_CaseInsensitive(t *testing.T) {
	coordinator := &TestPluginCoordinator{
		plugins: make(map[string]*RegisteredPluginInfo),
	}

	// Register a plugin with lowercase namespace
	coordinator.plugins["aws"] = &RegisteredPluginInfo{
		Namespace: "aws",
	}

	tests := []struct {
		name      string
		namespace string
		wantFound bool
	}{
		{"exact match lowercase", "aws", true},
		{"exact match uppercase", "AWS", true},
		{"mixed case", "Aws", true},
		{"all caps", "AWS", true},
		{"not found", "azure", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, found := coordinator.findPluginByNamespace(tt.namespace)
			if found != tt.wantFound {
				t.Errorf("findPluginByNamespace(%q) found = %v, want %v", tt.namespace, found, tt.wantFound)
			}
		})
	}
}

func TestFindPluginByNamespace_ReturnsCorrectPlugin(t *testing.T) {
	coordinator := &TestPluginCoordinator{
		plugins: make(map[string]*RegisteredPluginInfo),
	}

	// Register plugins with different namespaces
	coordinator.plugins["aws"] = &RegisteredPluginInfo{Namespace: "aws"}
	coordinator.plugins["azure"] = &RegisteredPluginInfo{Namespace: "azure"}
	coordinator.plugins["oci"] = &RegisteredPluginInfo{Namespace: "oci"}

	// Look up with different cases
	plugin, found := coordinator.findPluginByNamespace("AZURE")
	if !found {
		t.Fatal("expected to find azure plugin")
	}
	if plugin.Namespace != "azure" {
		t.Errorf("expected Namespace = azure, got %s", plugin.Namespace)
	}
}
