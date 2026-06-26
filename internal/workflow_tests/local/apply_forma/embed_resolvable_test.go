// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestMetastructure_RejectsOpaqueEmbed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// No plugin overrides needed — the rejection happens before any plugin call.
		m, def, err := test_helpers.NewTestMetastructure(t, &plugin.ResourcePluginOverrides{})
		defer def()
		require.NoError(t, err)

		// Build a $res envelope with $visibility:"Opaque", then frame it in a
		// $template so it looks like an embedded opaque resolvable.
		opaqueEnvelope := `{"$res":true,"$label":"some-host","$type":"FakeAWS::S3::Host","$stack":"test-stack","$property":"SecretKey","$visibility":"Opaque"}`
		framedSpan := pkgmodel.FrameEnvelope(opaqueEnvelope)

		embedField := map[string]any{
			"$embed":     true,
			"$template":  "prefix-" + framedSpan + "-suffix",
		}
		embedFieldJSON, err := json.Marshal(embedField)
		require.NoError(t, err)

		propsJSON, err := json.Marshal(map[string]any{
			"Name":         "consumer-resource",
			"functionCode": json.RawMessage(embedFieldJSON),
		})
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{{
				Label:      "consumer",
				Type:       "FakeAWS::S3::Bucket",
				Stack:      "test-stack",
				Target:     "test-target",
				Properties: json.RawMessage(propsJSON),
			}},
			Targets: []pkgmodel.Target{{
				Label:     "test-target",
				Namespace: "test-namespace",
			}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client-id")

		require.Error(t, err, "apply should be rejected when an opaque resolvable is embedded in a string field")
		assert.Contains(t, err.Error(), "opaque")
		assert.Contains(t, err.Error(), "embed")

		// No FormaCommand or resource row should have been persisted.
		fas, dsErr := m.Datastore.LoadFormaCommands()
		require.NoError(t, dsErr)
		assert.Empty(t, fas, "no FormaCommand should be persisted on opaque-embed rejection")

		resources, dsErr := m.Datastore.LoadAllResources()
		require.NoError(t, dsErr)
		assert.Empty(t, resources, "no resource row should be persisted on opaque-embed rejection")
	})
}

func TestMetastructure_RejectsOpaqueEmbed_InArray(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// Rejection must happen even when the $embed node is nested inside an array
		// of objects (the validator previously only walked object values, so an
		// opaque embed inside an array would slip through — security leak).
		m, def, err := test_helpers.NewTestMetastructure(t, &plugin.ResourcePluginOverrides{})
		defer def()
		require.NoError(t, err)

		// Build a $res envelope with $visibility:"Opaque", framed for embed.
		opaqueEnvelope := `{"$res":true,"$label":"some-host","$type":"FakeAWS::S3::Host","$stack":"test-stack","$property":"SecretKey","$visibility":"Opaque"}`
		framedSpan := pkgmodel.FrameEnvelope(opaqueEnvelope)

		embedField := map[string]any{
			"$embed":    true,
			"$template": "prefix-" + framedSpan + "-suffix",
		}

		// Place the embed node inside an array of objects — one innocent entry and
		// one that carries the opaque embed. The validator must descend into the
		// array to find it.
		propsJSON, err := json.Marshal(map[string]any{
			"Name": "consumer-resource",
			"statements": []any{
				map[string]any{"action": "s3:GetObject"},
				map[string]any{"document": embedField},
			},
		})
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{{
				Label:      "consumer",
				Type:       "FakeAWS::S3::Bucket",
				Stack:      "test-stack",
				Target:     "test-target",
				Properties: json.RawMessage(propsJSON),
			}},
			Targets: []pkgmodel.Target{{
				Label:     "test-target",
				Namespace: "test-namespace",
			}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test-client-id")

		require.Error(t, err, "apply should be rejected when an opaque resolvable is embedded inside an array element")
		assert.Contains(t, err.Error(), "opaque")
		assert.Contains(t, err.Error(), "embed")

		// No FormaCommand or resource row should have been persisted.
		fas, dsErr := m.Datastore.LoadFormaCommands()
		require.NoError(t, dsErr)
		assert.Empty(t, fas, "no FormaCommand should be persisted on opaque-embed-in-array rejection")

		resources, dsErr := m.Datastore.LoadAllResources()
		require.NoError(t, dsErr)
		assert.Empty(t, resources, "no resource row should be persisted on opaque-embed-in-array rejection")
	})
}

// TestEmbed_ResolvesToPluginInOneApply verifies that when a host and a consumer
// (whose functionCode uses $embed referencing the host's Id) are applied together
// in one FormaCommand:
//   - the system creates the host before the consumer (ordering requirement), and
//   - the consumer's plugin Create receives the assembled plain string (not the
//     structured envelope).
func TestEmbed_ResolvesToPluginInOneApply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const hostID = "resolved-host-id-1234"

		var mu sync.Mutex
		var createOrder []string
		var consumerReceivedProperties json.RawMessage

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				mu.Lock()
				createOrder = append(createOrder, req.Label)
				if req.Label == "embed-consumer" {
					consumerReceivedProperties = append(json.RawMessage{}, req.Properties...)
				}
				mu.Unlock()

				nativeID := req.Label + "-native"
				props := req.Properties
				if req.Label == "embed-host" {
					props = json.RawMessage(`{"Id":"` + hostID + `","name":"host1"}`)
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           nativeID,
					ResourceProperties: props,
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				if req.NativeID == "embed-host-native" {
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   `{"Id":"` + hostID + `","name":"host1"}`,
					}, nil
				}
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"functionCode":"cf.kvs('` + hostID + `')","name":"consumer1"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:  "test-target",
			Config: json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		hostKsuid := util.NewID()

		// Build the $res envelope referencing the host's Id, then frame it for embed.
		resEnvJSON, err := json.Marshal(map[string]any{
			"$res":      true,
			"$label":    "embed-host",
			"$type":     "FakeAWS::S3::Host",
			"$stack":    "embed-stack",
			"$property": "Id",
		})
		require.NoError(t, err)
		framedSpan := pkgmodel.FrameEnvelope(string(resEnvJSON))

		embedField := map[string]any{
			"$embed":    true,
			"$template": "cf.kvs('" + framedSpan + "')",
		}
		embedFieldJSON, err := json.Marshal(embedField)
		require.NoError(t, err)

		consumerPropsJSON, err := json.Marshal(map[string]any{
			"name":         "consumer1",
			"functionCode": json.RawMessage(embedFieldJSON),
		})
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "embed-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:  "embed-host",
					Type:   "FakeAWS::S3::Host",
					Stack:  "embed-stack",
					Target: "test-target",
					Ksuid:  hostKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "name",
						Fields:     []string{"name", "Id"},
					},
					Properties:         json.RawMessage(`{"name":"host1"}`),
					ReadOnlyProperties: json.RawMessage(`{"Id":"` + hostID + `"}`),
				},
				{
					Label:      "embed-consumer",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "embed-stack",
					Target:     "test-target",
					Properties: json.RawMessage(consumerPropsJSON),
					Schema: pkgmodel.Schema{
						Identifier: "name",
						Fields:     []string{"name", "functionCode"},
					},
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target", Config: json.RawMessage(`{}`)}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			for _, ru := range fas[0].ResourceUpdates {
				if ru.State != resource_update.ResourceUpdateStateSuccess {
					return false
				}
			}
			return len(fas[0].ResourceUpdates) == 2
		}, 5*time.Second, 100*time.Millisecond, "both resources should be created successfully")

		// Assert ordering: host must be created before consumer.
		mu.Lock()
		order := append([]string{}, createOrder...)
		received := append(json.RawMessage{}, consumerReceivedProperties...)
		mu.Unlock()

		require.Len(t, order, 2, "exactly two Create calls expected")
		assert.Equal(t, "embed-host", order[0], "host must be created before consumer")
		assert.Equal(t, "embed-consumer", order[1], "consumer must be created after host")

		// Assert the consumer received the assembled plain string, not a $embed envelope.
		require.NotEmpty(t, received, "consumer Create must have been called")
		fcResult := gjson.GetBytes(received, "functionCode")
		require.True(t, fcResult.Exists(), "functionCode must be present in plugin Create request")
		assert.Equal(t, "cf.kvs('"+hostID+"')", fcResult.String(),
			"plugin Create must receive the assembled plain string with the resolved host Id")
		assert.False(t, gjson.GetBytes(received, "functionCode.$embed").Bool(),
			"plugin Create must NOT receive a $embed envelope")
	})
}

// TestEmbed_PersistedStateStaysStructured verifies that after a successful apply
// the consumer resource row in the datastore retains the structured $embed envelope
// (not the assembled plain string). This is the persist-structured invariant: the
// agent always stores intent, never the flattened plugin view.
func TestEmbed_PersistedStateStaysStructured(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const hostID = "structured-host-id-5678"

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				var props json.RawMessage
				if req.Label == "embed-host" {
					props = json.RawMessage(`{"Id":"` + hostID + `","name":"host1"}`)
				} else {
					props = req.Properties
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           req.Label + "-native",
					ResourceProperties: props,
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				if req.NativeID == "embed-host-native" {
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   `{"Id":"` + hostID + `","name":"host1"}`,
					}, nil
				}
				// Consumer: return the assembled plain string (as the cloud would).
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"functionCode":"cf.kvs('` + hostID + `')","name":"consumer1"}`,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:  "test-target",
			Config: json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		hostKsuid := util.NewID()

		resEnvJSON, err := json.Marshal(map[string]any{
			"$res":      true,
			"$label":    "embed-host",
			"$type":     "FakeAWS::S3::Host",
			"$stack":    "embed-stack",
			"$property": "Id",
		})
		require.NoError(t, err)
		framedSpan := pkgmodel.FrameEnvelope(string(resEnvJSON))

		embedFieldJSON, err := json.Marshal(map[string]any{
			"$embed":    true,
			"$template": "cf.kvs('" + framedSpan + "')",
		})
		require.NoError(t, err)

		consumerPropsJSON, err := json.Marshal(map[string]any{
			"name":         "consumer1",
			"functionCode": json.RawMessage(embedFieldJSON),
		})
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "embed-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:  "embed-host",
					Type:   "FakeAWS::S3::Host",
					Stack:  "embed-stack",
					Target: "test-target",
					Ksuid:  hostKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "name",
						Fields:     []string{"name", "Id"},
					},
					Properties:         json.RawMessage(`{"name":"host1"}`),
					ReadOnlyProperties: json.RawMessage(`{"Id":"` + hostID + `"}`),
				},
				{
					Label:      "embed-consumer",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "embed-stack",
					Target:     "test-target",
					Properties: json.RawMessage(consumerPropsJSON),
					Schema: pkgmodel.Schema{
						Identifier: "name",
						Fields:     []string{"name", "functionCode"},
					},
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target", Config: json.RawMessage(`{}`)}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			for _, ru := range fas[0].ResourceUpdates {
				if ru.State != resource_update.ResourceUpdateStateSuccess {
					return false
				}
			}
			return len(fas[0].ResourceUpdates) == 2
		}, 5*time.Second, 100*time.Millisecond, "both resources should be created successfully")

		// Load the consumer resource from the datastore.
		resources, err := m.Datastore.LoadResourcesByStack("embed-stack")
		require.NoError(t, err)

		var consumer *pkgmodel.Resource
		for _, r := range resources {
			if r.Label == "embed-consumer" {
				consumer = r
				break
			}
		}
		require.NotNil(t, consumer, "embed-consumer resource must be persisted")

		// The stored functionCode must retain the $embed envelope — NOT the assembled string.
		fcResult := gjson.GetBytes(consumer.Properties, "functionCode")
		require.True(t, fcResult.Exists(), "functionCode must be present in stored resource")
		assert.True(t, fcResult.Get("$embed").Bool(),
			"stored functionCode must be the structured $embed envelope, not the assembled plain string")
		assert.NotEmpty(t, fcResult.Get("$template").String(),
			"stored functionCode must retain the $template field")
		assert.NotEqual(t, "cf.kvs('"+hostID+"')", fcResult.String(),
			"stored functionCode must NOT be the assembled plain string")
	})
}

// TestEmbed_SyncPreservesEnvelope verifies that after a sync (where the plugin's
// Read returns the assembled plain string as the cloud would), the stored desired
// state of the consumer still carries the $embed envelope. This is the
// merge-preservation invariant: the agent never lets a sync Read overwrite
// user-provided resolvable structures.
func TestEmbed_SyncPreservesEnvelope(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const hostID = "sync-host-id-9012"

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				var props json.RawMessage
				if req.Label == "embed-host" {
					props = json.RawMessage(`{"Id":"` + hostID + `","name":"host1"}`)
				} else {
					props = req.Properties
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label,
					NativeID:           req.Label + "-native",
					ResourceProperties: props,
				}}, nil
			},
			// Read always returns the assembled plain string — simulating what the cloud
			// returns for a lambda functionCode.
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				if req.NativeID == "embed-host-native" {
					return &resource.ReadResult{
						ResourceType: req.ResourceType,
						Properties:   `{"Id":"` + hostID + `","name":"host1"}`,
					}, nil
				}
				// Consumer: return the assembled plain string (cloud-native view).
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   `{"functionCode":"cf.kvs('` + hostID + `')","name":"consumer1"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false // manual sync control
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		_, err = m.Datastore.CreateTarget(&pkgmodel.Target{
			Label:  "test-target",
			Config: json.RawMessage(`{}`),
		})
		require.NoError(t, err)

		hostKsuid := util.NewID()

		resEnvJSON, err := json.Marshal(map[string]any{
			"$res":      true,
			"$label":    "embed-host",
			"$type":     "FakeAWS::S3::Host",
			"$stack":    "embed-stack",
			"$property": "Id",
		})
		require.NoError(t, err)
		framedSpan := pkgmodel.FrameEnvelope(string(resEnvJSON))

		embedFieldJSON, err := json.Marshal(map[string]any{
			"$embed":    true,
			"$template": "cf.kvs('" + framedSpan + "')",
		})
		require.NoError(t, err)

		consumerPropsJSON, err := json.Marshal(map[string]any{
			"name":         "consumer1",
			"functionCode": json.RawMessage(embedFieldJSON),
		})
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "embed-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label:  "embed-host",
					Type:   "FakeAWS::S3::Host",
					Stack:  "embed-stack",
					Target: "test-target",
					Ksuid:  hostKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "name",
						Fields:     []string{"name", "Id"},
					},
					Properties:         json.RawMessage(`{"name":"host1"}`),
					ReadOnlyProperties: json.RawMessage(`{"Id":"` + hostID + `"}`),
				},
				{
					Label:      "embed-consumer",
					Type:       "FakeAWS::S3::Bucket",
					Stack:      "embed-stack",
					Target:     "test-target",
					Properties: json.RawMessage(consumerPropsJSON),
					Schema: pkgmodel.Schema{
						Identifier: "name",
						Fields:     []string{"name", "functionCode"},
					},
				},
			},
			Targets: []pkgmodel.Target{{Label: "test-target", Config: json.RawMessage(`{}`)}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")
		require.NoError(t, err)

		// Wait for the apply to complete.
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil || len(fas) != 1 {
				return false
			}
			for _, ru := range fas[0].ResourceUpdates {
				if ru.State != resource_update.ResourceUpdateStateSuccess {
					return false
				}
			}
			return len(fas[0].ResourceUpdates) == 2
		}, 5*time.Second, 100*time.Millisecond, "apply must complete before triggering sync")

		// Trigger a manual sync. The Read override returns the assembled plain string
		// for the consumer — simulating what the cloud returns for a lambda's code.
		err = m.ForceSync()
		require.NoError(t, err)

		// Wait for the sync command to complete successfully.
		require.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}
			for _, fc := range fas {
				if fc.Command == pkgmodel.CommandSync {
					for _, ru := range fc.ResourceUpdates {
						if ru.State != resource_update.ResourceUpdateStateSuccess {
							return false
						}
					}
					return len(fc.ResourceUpdates) > 0
				}
			}
			return false
		}, 5*time.Second, 100*time.Millisecond, "sync must complete successfully")

		// After sync, load the consumer from the datastore and verify the $embed is preserved.
		resources, err := m.Datastore.LoadResourcesByStack("embed-stack")
		require.NoError(t, err)

		var consumer *pkgmodel.Resource
		for _, r := range resources {
			if r.Label == "embed-consumer" {
				consumer = r
				break
			}
		}
		require.NotNil(t, consumer, "embed-consumer resource must still be in datastore after sync")

		fcResult := gjson.GetBytes(consumer.Properties, "functionCode")
		require.True(t, fcResult.Exists(), "functionCode must be present in stored resource after sync")
		assert.True(t, fcResult.Get("$embed").Bool(),
			"sync must preserve the $embed envelope: stored functionCode must still be structured")
		assert.NotEmpty(t, fcResult.Get("$template").String(),
			"$template must be preserved after sync")
		assert.NotEqual(t, "cf.kvs('"+hostID+"')", fcResult.String(),
			"stored functionCode must NOT be the assembled plain string after sync")
	})
}
