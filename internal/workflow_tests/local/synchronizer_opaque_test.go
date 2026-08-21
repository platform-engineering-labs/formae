// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestSynchronizer_OpaqueSecretOutOfBandDrift is the known-good baseline: a single
// resource with a schema-opaque "secret" field is applied with value
// "secret-INITIAL". A background sync then reads back a DRIFTED plaintext
// ("secret-DRIFTED") and the datastore must ingest that drift hashed at rest,
// as {"$value":sha256("secret-DRIFTED"),"$hashed":true,"$visibility":"Opaque"}.
func TestSynchronizer_OpaqueSecretOutOfBandDrift(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const driftedSecret = "secret-DRIFTED"

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "1",
						NativeID:        "1",
					},
				}, nil
			},
			// Out-of-band drift: the live read reports a different secret value.
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   `{"name":"my-secret","secret":"` + driftedSecret + `"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = true
		cfg.Agent.Synchronization.Interval = 2 * time.Second
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		schema := pkgmodel.Schema{
			Identifier: "name",
			Fields:     []string{"name", "secret"},
			Hints: map[string]pkgmodel.FieldHint{
				"secret": {Opaque: true},
			},
		}
		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: stack}},
			Resources: []pkgmodel.Resource{{
				Label:      "my-secret",
				Type:       "FakeAWS::Resource",
				Stack:      stack,
				Target:     "test-target",
				Schema:     schema,
				Properties: json.RawMessage(`{"name":"my-secret","secret":"secret-INITIAL"}`),
			}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		expectedHash := pkgmodel.ComputeValueHash(driftedSecret)

		// Wait until the drifted secret has been ingested (hashed) by a sync cycle.
		require.Eventually(t, func() bool {
			resources, err := m.Datastore.LoadResourcesByStack(stack)
			if err != nil || len(resources) != 1 {
				return false
			}
			secret := gjson.GetBytes(resources[0].Properties, "secret")
			return secret.Get("$hashed").Bool() && secret.Get("$value").String() == expectedHash
		}, 10*time.Second, 100*time.Millisecond, "sync should ingest drifted secret hashed at rest")

		resources, err := m.Datastore.LoadResourcesByStack(stack)
		require.NoError(t, err)
		require.Len(t, resources, 1)

		secret := gjson.GetBytes(resources[0].Properties, "secret")
		t.Logf("stored secret envelope after sync: %s", secret.Raw)
		assert.True(t, secret.Get("$hashed").Bool(), "secret must be hashed at rest")
		assert.Equal(t, expectedHash, secret.Get("$value").String(), "secret $value must be sha256(driftedSecret)")
		assert.Equal(t, "Opaque", secret.Get("$visibility").String(), "secret must carry Opaque visibility")
		assert.NotContains(t, string(resources[0].Properties), driftedSecret, "plaintext must never be stored")
	})
}
