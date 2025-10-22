// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package workflow_tests

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
)

func TestMetastructure_ResourcePluginsForNamespace(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		d, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		if err == nil {
			// Test with a valid namespace (aws)
			p, err := d.PluginManager.ResourcePlugin("aws")
			if err != nil {
				t.Errorf("Couldn't get resource plugin for 'aws' namespace: %v", err)
			}

			if p == nil {
				t.Errorf("Resource plugin is nil")
			}

			// Try with a wrong/nonexistent namespace
			p, err = d.PluginManager.ResourcePlugin("foobar")
			if err == nil {
				t.Errorf("Expected error for nonexistent 'foobar' namespace but got nil")
			}

			if p != nil {
				t.Errorf("Resource plugin is not nil")
			}
		}
	})
}
