// © 2025 Platform Engineering Labs Inc.
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

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TestMetastructure_ReconcileResolvesReadOnlyOutputReference verifies that a
// dependent resolving a reference to another resource's read-only output
// resolves correctly under a reconcile apply. Read-only/computed outputs (an
// IAM Role's Arn, a Secret's Id) are segregated into ReadOnlyProperties rather
// than Properties, so resolution must consult both. Here the source resource's
// Arn lives only in ReadOnlyProperties and a newly-added dependent references
// it via `.res.arn`; the reference must resolve to the concrete Arn value.
//
// The sibling TestMetastructure_ApplyFormaWithRes covers the same shape in
// patch mode; this exercises the reconcile path with a new dependent against a
// pre-existing source.
func TestMetastructure_ReconcileResolvesReadOnlyOutputReference(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const hostArn = "arn:fake:host/host1"
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "5678",
					ResourceProperties: json.RawMessage(`{"name":"bucket1"}`),
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Host",
					Properties:   `{"name":"host1","Arn":"` + hostArn + `"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:  "test-target",
			Config: json.RawMessage(`{}`),
		})

		// Existing source resource whose Arn lives only in ReadOnlyProperties.
		hostKsuid := util.NewID()
		host := pkgmodel.Resource{
			Label:              "snarf-host",
			Type:               "FakeAWS::S3::Host",
			Properties:         json.RawMessage(`{"name": "host1"}`),
			ReadOnlyProperties: json.RawMessage(`{"Arn": "` + hostArn + `"}`),
			Stack:              "test-stack",
			Target:             "test-target",
			Ksuid:              hostKsuid,
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints:      map[string]pkgmodel.FieldHint{"name": {CreateOnly: true}},
				Fields:     []string{"name"},
			},
		}
		_, err = m.Datastore.BulkStoreResources([]pkgmodel.Resource{host}, "host-setup-command")
		require.NoError(t, err)

		// New dependent referencing the source's read-only Arn output via $res.
		bucket := pkgmodel.Resource{
			Label: "test-bucket",
			Type:  "FakeAWS::S3::Bucket",
			Properties: json.RawMessage(`{
				"name": "bucket1",
				"host-arn": {
					"$res": true,
					"$label": "snarf-host",
					"$type": "FakeAWS::S3::Host",
					"$stack": "test-stack",
					"$property": "Arn"
				}
			}`),
			Stack:  "test-stack",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints:      map[string]pkgmodel.FieldHint{"name": {CreateOnly: true}},
				Fields:     []string{"name", "host-arn"},
			},
		}

		// Reconcile mode describes the full stack, so the (unchanged) source is
		// included alongside the new dependent.
		forma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{host, bucket},
			Targets:   []pkgmodel.Target{{Label: "test-target", Config: json.RawMessage(`{}`)}},
		}

		m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		var bucketUpdate resource_update.ResourceUpdate
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			if len(fas) != 1 {
				return false
			}
			for _, ru := range fas[0].ResourceUpdates {
				if ru.DesiredState.Label == "test-bucket" {
					bucketUpdate = ru
					return ru.State == resource_update.ResourceUpdateStateSuccess ||
						ru.State == resource_update.ResourceUpdateStateFailed
				}
			}
			return false
		}, 5*time.Second, 100*time.Millisecond)

		require.Equal(t, resource_update.ResourceUpdateStateSuccess, bucketUpdate.State,
			"dependent referencing a read-only output must resolve, not fail: %q",
			bucketUpdate.MostRecentFailureMessage())

		var props map[string]any
		require.NoError(t, json.Unmarshal(bucketUpdate.DesiredState.Properties, &props))
		hostArnRef, ok := props["host-arn"].(map[string]any)
		require.True(t, ok, "host-arn ref should be present")
		assert.Equal(t, hostArn, hostArnRef["$value"],
			"a read-only output reference must resolve to the source's Arn")
	})
}
