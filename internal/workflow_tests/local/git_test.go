// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
)

func TestMetastructure_FormaAppliedSuccess(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					},
				}, nil
			},
		}

		o, def, err := test_helpers.NewTestMetastructure(t, overrides)

		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		f := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{
				{
					Label: "test-stack",
				},
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		o.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		assert.Eventually(t, func() bool {
			fas, err := o.Datastore.LoadFormaCommands()
			if err != nil {
				t.Errorf("Failed to load forma commands: %v", err)
			}
			return len(fas) == 1 && len(fas[0].ResourceUpdates) == 1 && fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess && fas[0].ResourceUpdates[0].Version != ""
		}, 3*time.Second, 100)
	})
}

func TestMetastructure_FormaAppliedPartialSuccess(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				if request.DesiredState.Label == "test-resource2" {
					return &resource.CreateResult{
						ProgressResult: &resource.ProgressResult{
							Operation:       resource.OperationCreate,
							OperationStatus: resource.OperationStatusFailure,
						}}, nil
				}
				return &resource.CreateResult{
					ProgressResult: &resource.ProgressResult{
						Operation:          resource.OperationCreate,
						OperationStatus:    resource.OperationStatusSuccess,
						ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					},
				}, nil
			},
		}

		o, def, err := test_helpers.NewTestMetastructure(t, overrides)

		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		f := &pkgmodel.Forma{
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
					Label:      "test-resource1",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
				{
					Label:      "test-resource2",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack2",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}

		o.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		assert.Eventually(t, func() bool {
			fas, err := o.Datastore.LoadFormaCommands()
			if err != nil {
				t.Errorf("Failed to load forma commands: %v", err)
			}
			resource1Index := slices.IndexFunc(fas[0].ResourceUpdates, func(ru resource_update.ResourceUpdate) bool {
				return ru.DesiredState.Label == "test-resource1"
			})

			return len(fas) == 1 && len(fas[0].ResourceUpdates) == 2 && fas[0].ResourceUpdates[resource1Index].State == resource_update.ResourceUpdateStateSuccess && fas[0].ResourceUpdates[resource1Index].Version != ""
		}, 4*time.Second, 100)
	})
}
