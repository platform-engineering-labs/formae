// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"

	"github.com/stretchr/testify/assert"
)

func TestMetastructure_ApplyFormaFailed(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if strings.EqualFold(request.Resource.Label, "test-resource2") {
					return nil, fmt.Errorf("failed to create resource")
				} else {
					return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
						Operation:       resource.OperationCreate,
						OperationStatus: resource.OperationStatusSuccess,
						RequestID:       "1234",
						NativeID:        "5678",
						ResourceType:    request.Resource.Type,
					}}, nil
				}
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
				{
					Label: "test-stack2",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
		}

		m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			fas_incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			assert.NoError(t, err)

			return len(fas) == 1 && len(fas_incomplete) == 0 && fas[0].State == forma_command.CommandStateFailed
		}, 5*time.Second, 100*time.Millisecond)
	})
}

func TestMetastructure_ApplyFormaFailedAfterRetries(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if strings.EqualFold(request.Resource.Label, "test-resource2") {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
							ErrorCode:       resource.OperationErrorCodeNetworkFailure, // This is a recoverable error
						},
					}, nil
				} else {
					return nil, nil
				}
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		forma := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack1",
				},
				{
					Label: "test-stack2",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:  "test-resource1",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack1",
					Target: "test-target",
				},
				{
					Label:  "test-resource2",
					Type:   "FakeAWS::S3::Bucket",
					Stack:  "test-stack2",
					Target: "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label:     "test-target",
					Namespace: "test-namespace",
				},
			},
		}

		m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			assert.NoError(t, err)
			fas_incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			assert.NoError(t, err)

			return len(fas) == 1 && len(fas_incomplete) == 0 &&
				fas[0].ResourceUpdates[1].State == resource_update.ResourceUpdateStateFailed &&
				fas[0].ResourceUpdates[1].ProgressResult[0].Attempts == 4
		}, 15*time.Second, 500*time.Millisecond)
	})
}
