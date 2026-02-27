// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetastructure_ResourcePluginsForNamespace(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		d, def, err := test_helpers.NewTestMetastructure(t, nil)
		defer def()
		require.NoError(t, err)

		// FakeAWS is now injected directly via TestResourcePlugin (no longer a .so plugin)
		p := d.TestResourcePlugin
		require.NotNil(t, p, "TestResourcePlugin should be set by test helpers")
		assert.Equal(t, "FakeAWS", p.Namespace())

		// PluginManager should not find FakeAWS (it's no longer a .so plugin)
		_, err = d.PluginManager.ResourcePlugin("foobar")
		assert.Error(t, err, "Expected error for nonexistent 'foobar' namespace")
	})
}
