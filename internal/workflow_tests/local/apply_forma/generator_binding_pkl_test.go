// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_local

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// authoredGeneratorBindingForma is the forma this test applies: a
// PasswordGenerator and a plugin resource whose secret-bearing property is
// bound to that generator's `value` output, authored in PKL rather than as a
// hand-built envelope.
const authoredGeneratorBindingForma = "internal/schema/pkl/testdata/forma/generator_binding_test.pkl"

// pklCreateCapture records the SecretString each Create reached the provider
// with. Returning nil falls through to FakeAWS's own handling, so capturing is
// the only behaviour it adds.
type pklCreateCapture struct {
	mu     sync.Mutex
	byName map[string]string
}

func (c *pklCreateCapture) overrides() *plugin.ResourcePluginOverrides {
	c.byName = map[string]string{}
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			props := gjson.ParseBytes(req.Properties)
			c.byName[props.Get("Name").String()] = props.Get("SecretString").String()
			return nil, nil
		},
	}
}

func (c *pklCreateCapture) valueFor(name string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.byName[name]
	return v, ok
}

// A generator binding authored in PKL applies end to end: evaluating the forma
// renders the $gen envelope from `pw.gen.value`, and applying the evaluated
// forma draws the value and delivers the credential itself to the provider.
// The forma is evaluated rather than hand-built, so the binding is exercised
// over a shape a forma author can write.
func TestApplyForma_PklAuthoredGeneratorBinding_DrawnValueReachesTheProvider(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		forma, err := pkl.PKL{}.Evaluate(
			authoredGeneratorBindingForma,
			pkgmodel.CommandApply,
			pkgmodel.FormaApplyModeReconcile,
			nil,
		)
		require.NoError(t, err, "the authored forma must evaluate")
		require.Len(t, forma.Resources, 1)
		require.Len(t, forma.Generators, 1)
		require.True(t, gjson.GetBytes(forma.Resources[0].Properties, "SecretString.$gen").Bool(),
			"precondition: eval must render the binding as a $gen envelope")

		capture := &pklCreateCapture{}
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, capture.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			incomplete, loadErr := m.Datastore.LoadIncompleteFormaCommands()
			return loadErr == nil && len(incomplete) == 0
		}, 30*time.Second, 100*time.Millisecond, "the apply must reach a terminal state")

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 1)
		require.Equal(t, forma_command.CommandStateSuccess, cmds[0].State,
			"a PKL-authored generator binding must apply")

		drawn, ok := capture.valueFor("db")
		require.True(t, ok, "the provider must have been called for the bound secret")
		assert.Len(t, drawn, 24, "the provider receives the drawn value, at the authored length")
		assert.NotContains(t, drawn, "$gen",
			"the provider must receive the drawn value, never the envelope naming it")
	})
}
