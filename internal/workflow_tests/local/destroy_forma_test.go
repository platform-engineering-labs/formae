// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
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

func TestMetastructure_ApplyThenDestroyForma(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			Delete: func(request *resource.DeleteRequest) (*resource.DeleteResult, error) {
				return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationDelete,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "1234",
					NativeID:        "5678",
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
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
			},
			Resources: []pkgmodel.Resource{
				{
					Label:      "test-resource-destroy",
					Type:       "FakeAWS::S3::Bucket",
					Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
					Stack:      "test-stack1",
					Target:     "test-target",
				},
			},
			Targets: []pkgmodel.Target{
				{
					Label: "test-target",
				},
			},
		}
		m.ApplyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			if len(fas) != 1 || len(fas[0].ResourceUpdates) != 1 {
				return false
			}

			return fas[0].ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				fas[0].State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

		applyStack, err := m.Datastore.LoadStack("test-stack1")
		assert.NoError(t, err)
		assert.Len(t, applyStack.Resources, 1)

		m.DestroyForma(f, &config.FormaCommandConfig{
			Mode: pkgmodel.FormaApplyModeReconcile,
		}, "test")

		// Wait for destroy to complete
		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			if err != nil {
				return false
			}

			if len(fas) != 2 {
				return false
			}

			// Find the destroy command by type (don't rely on order)
			var destroyForma *forma_command.FormaCommand
			for _, fc := range fas {
				if fc.Command == pkgmodel.CommandDestroy {
					destroyForma = fc
					break
				}
			}
			if destroyForma == nil {
				return false
			}

			if len(destroyForma.ResourceUpdates) != 1 {
				return false
			}

			return destroyForma.ResourceUpdates[0].State == resource_update.ResourceUpdateStateSuccess &&
				destroyForma.State == forma_command.CommandStateSuccess
		}, 5*time.Second, 100*time.Millisecond)

		fas, err := m.Datastore.LoadFormaCommands()
		assert.NoError(t, err)
		assert.Equal(t, 2, len(fas))

		// Find apply and destroy commands by type (don't rely on order)
		var applyForma, destroyForma *forma_command.FormaCommand
		for _, fc := range fas {
			switch fc.Command {
			case pkgmodel.CommandApply:
				applyForma = fc
			case pkgmodel.CommandDestroy:
				destroyForma = fc
			}
		}

		assert.NotNil(t, applyForma, "should have an apply command")
		assert.NotNil(t, destroyForma, "should have a destroy command")
		assert.Equal(t, 1, len(destroyForma.ResourceUpdates))

		assert.Equal(t, resource_update.ResourceUpdateStateSuccess, destroyForma.ResourceUpdates[0].State)

		assert.Equal(t, forma_command.CommandStateSuccess, destroyForma.State)
		destroyStack, err := m.Datastore.LoadStack("test-stack1")
		assert.Nil(t, err)
		assert.Nil(t, destroyStack)

		// Verify the stack was also deleted
		stack, err := m.Datastore.GetStackByLabel("test-stack1")
		assert.NoError(t, err)
		assert.Nil(t, stack, "Stack should be deleted when all resources are destroyed")
	})
}
