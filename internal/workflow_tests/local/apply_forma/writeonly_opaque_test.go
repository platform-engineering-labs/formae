// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestWriteOnlyOpaqueLiteralStoredAsCanonicalValue checks that a schema-opaque
// field supplied as a literal string, whose plugin Read never returns a value
// (a write-only field, as with an AWS RDS master password that the cloud omits
// on every read), is persisted through ApplyForma as a canonical formae.Value
// envelope carrying $strategy, not a bare {$value,$visibility,$hashed}.
//
// $strategy is what the extract generator uses to recognize the value as opaque.
// On a field whose type union includes formae.Resolvable it must be present, or
// the generator emits a label-less {$res,$visibility:Opaque} that fails to
// evaluate. The test asserts the canonical shape at rest in the resources table,
// in ExtractResources output, and again after a sync.
func TestWriteOnlyOpaqueLiteralStoredAsCanonicalValue(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const plaintextSecret = "TestPassword123!"

		// Write-only: the plugin Read never returns SecretString.
		overrides := &plugin.ResourcePluginOverrides{
			Read: func(r *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: r.ResourceType,
					Properties:   `{"Name":"my-secret"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::SecretsManager::Secret",
				Stack:      stack,
				Target:     "test-target",
				Schema:     secretSchema(),
				Properties: json.RawMessage(`{"Name":"my-secret","SecretString":"` + plaintextSecret + `"}`),
			}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		assertCanonicalOpaque := func(t *testing.T, props json.RawMessage, where string) {
			t.Helper()
			var m map[string]any
			require.NoError(t, json.Unmarshal(props, &m))
			sv, ok := m["SecretString"].(map[string]any)
			require.Truef(t, ok, "%s: SecretString must be an opaque envelope, got %v", where, m["SecretString"])
			assert.Equalf(t, "Opaque", sv["$visibility"], "%s: visibility", where)
			assert.Equalf(t, true, sv["$hashed"], "%s: hashed marker", where)
			assert.Equalf(t, "Update", sv["$strategy"], "%s: canonical $strategy", where)
			assert.NotEqualf(t, plaintextSecret, sv["$value"], "%s: value must be hashed", where)
		}

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assertCanonicalOpaque(t, resources[0].Properties, "resources table")

		extracted, err := m.ExtractResources("stack:" + stack)
		require.NoError(t, err)
		require.NotNil(t, extracted)
		require.Len(t, extracted.Resources, 1)
		assertCanonicalOpaque(t, extracted.Resources[0].Properties, "ExtractResources")

		// A sync must not regress the canonical shape.
		require.NoError(t, m.ForceSync())
		waitForApplyComplete(t, m)
		resourcesAfter, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resourcesAfter, 1)
		assertCanonicalOpaque(t, resourcesAfter[0].Properties, "resources table after sync")
	})
}
