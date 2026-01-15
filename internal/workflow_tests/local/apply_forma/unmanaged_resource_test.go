// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestApplyForma_ReconcileFormaContainingUnmanagedResource(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		unmanagedProperties := json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`)
		managedProperties := json.RawMessage(`{
			"BucketName": "my-unique-bucket-name",
			"VersioningConfiguration": {
			"Status": "Enabled"
			},
			"Tags": [
			{
			"Key": "FormaeStackLabel",
			"Value": "infrastructure"
			},
			{
			"Key": "FormaeResourceLabel",
			"Value": "my-s3-bucket"
			},
			{
			"Key": "Environment",
			"Value": "Production"
			}
			]
			}`)

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return nil, fmt.Errorf("resource already exists")
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   string(unmanagedProperties),
				}, nil
			},
			Update: func(request *resource.UpdateRequest) (*resource.UpdateResult, error) {
				return &resource.UpdateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationUpdate,
						OperationStatus:    resource.OperationStatusSuccess,
						NativeID:           request.NativeID,
						ResourceProperties: managedProperties,
					},
				}, nil
			},
		}
		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		unmanagedResource := pkgmodel.Resource{
			Label:  "my-s3-bucket",
			Type:   "FakeAWS::S3::Bucket",
			Stack:  "$unmanaged",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "BucketName",
				Tags:       "Tags",
				Hints: map[string]pkgmodel.FieldHint{
					"BucketName": {
						CreateOnly: true,
					},
				},
				Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
			},
			Properties: unmanagedProperties,
			Managed:    false,
		}

		// First persist initial resource
		unmanagedStack := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
			Resources: []pkgmodel.Resource{unmanagedResource},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "FakeAWS"}},
		}
		_, err = m.Datastore.StoreStack(unmanagedStack, "discovery-command-1")
		assert.NoError(t, err)

		managedResource := pkgmodel.Resource{
			Label:  "my-s3-bucket",
			Type:   "FakeAWS::S3::Bucket",
			Stack:  "infrastructure",
			Target: "test-target",
			Schema: pkgmodel.Schema{
				Identifier: "BucketName",
				Tags:       "Tags",
				Hints: map[string]pkgmodel.FieldHint{
					"BucketName": {
						CreateOnly: true,
					},
				},
				Fields: []string{"BucketName", "VersioningConfiguration", "Tags"},
			},
			Properties: managedProperties,
			Managed:    true,
		}

		forma := &pkgmodel.Forma{
			Stacks:    []pkgmodel.Stack{{Label: "infrastructure"}},
			Resources: []pkgmodel.Resource{managedResource},
			Targets:   []pkgmodel.Target{{Label: "test-target", Namespace: "FakeAWS"}},
		}

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false}, "test-client-id")
		assert.NoError(t, err)

		assert.Eventually(t, func() bool {
			res, err := m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			return len(res) == 1 && res[0].State == forma_command.CommandStateSuccess
		},
			50*time.Second, 500*time.Millisecond)

		commands, err := m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Equal(t, 1, len(commands))
		assert.Equal(t, 1, len(commands[0].ResourceUpdates))
		actualKsuidURI := commands[0].ResourceUpdates[0].DesiredState.URI()

		fromDb, err := m.Datastore.LoadResource(actualKsuidURI)
		assert.NoError(t, err)
		assert.Equal(t, true, fromDb.Managed)
		assert.Equal(t, "infrastructure", fromDb.Stack)
	})
}
