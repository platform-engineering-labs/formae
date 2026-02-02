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

func TestMetastructure_ApplyFormaWithRes(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "5678",
					ResourceProperties: json.RawMessage(`{"Id": "1234"}`),
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Host",
					Properties:   string(`{"Id": "1234"}`),
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

		hostKsuid := util.NewID()
		initialResourceHost := &pkgmodel.Resource{
			Label:              "snarf-host",
			Type:               "FakeAWS::S3::Host",
			Properties:         json.RawMessage(`{"name": "host1"}`),
			Stack:              "test-stack",
			Target:             "test-target",
			Ksuid:              hostKsuid,
			ReadOnlyProperties: json.RawMessage(`{"Id": "1234"}`),
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints: map[string]pkgmodel.FieldHint{
					"name": {
						CreateOnly: true,
					},
				},
				Fields: []string{"name"},
			},
		}

		// Store the host resource directly (for reference lookup)
		initForma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{*initialResourceHost},
			Targets: []pkgmodel.Target{{
				Label:  "test-target",
				Config: json.RawMessage(`{}`),
			}},
		}
		_, err = m.Datastore.BulkStoreResources(initForma.Resources, "host-setup-command")
		require.NoError(t, err)

		initialResource := &pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			// Use resolvable object - system will translate to $ref automatically
			Properties: json.RawMessage(`{
				"name": "bucket1", 
				"snarf-host-id": {
					"$res": true,
					"$label": "snarf-host",
					"$type": "FakeAWS::S3::Host",
					"$stack": "test-stack",
					"$property": "Id"
				}
			}`),
			Stack:  "test-stack",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "name",
				Hints: map[string]pkgmodel.FieldHint{
					"name": {CreateOnly: true},
				},
				Fields: []string{"name", "versioning", "snarf-host-id"},
			},
		}

		// Apply the bucket resource (this creates 1 FormaCommand)
		bucketForma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				*initialResource,
			},
			Targets: []pkgmodel.Target{{
				Label:  "test-target",
				Config: json.RawMessage(`{}`),
			}},
		}

		m.ApplyForma(bucketForma, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModePatch,
		}, "test")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			assert.Equal(t, 1, len(fas))
			assert.Equal(t, 1, len(fas[0].ResourceUpdates))

			bucketUpdate := fas[0].ResourceUpdates[0]
			return bucketUpdate.State == resource_update.ResourceUpdateStateSuccess &&
				bucketUpdate.Operation == resource_update.OperationCreate
		}, 3*time.Second, 100*time.Millisecond)

		fas, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, fas, 1, "Should have exactly 1 FormaCommand")

		bucketCommand := fas[0]
		require.Len(t, bucketCommand.ResourceUpdates, 1)

		bucketUpdate := bucketCommand.ResourceUpdates[0]
		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, bucketUpdate.State)
		assert.Equal(t, resource_update.OperationCreate, bucketUpdate.Operation)

		var props map[string]any
		err = json.Unmarshal(bucketUpdate.DesiredState.Properties, &props)
		require.NoError(t, err)

		snarfHostID, ok := props["snarf-host-id"].(map[string]any)
		require.True(t, ok, "snarf-host-id should be present")

		// Should have both $ref (now in KSUID format) and $value (resolved)
		refString, hasRef := snarfHostID["$ref"].(string)
		require.True(t, hasRef, "Should have $ref")

		// Should reference the host by its KSUID, not triplet format
		assert.Regexp(t, `^formae://[a-zA-Z0-9]+#/Id$`, refString, "Should be KSUID format")
		assert.Contains(t, refString, hostKsuid, "Should reference the host's KSUID")

		resolvedValue, hasValue := snarfHostID["$value"]
		require.True(t, hasValue, "Should have resolved $value")
		assert.Equal(t, "1234", resolvedValue, "Should have resolved to the host's Id")
	})
}
