// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/constants"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestStackTransition_Integration(t *testing.T) {
	t.Run("unmanaged to new stack preserves KSUID", func(t *testing.T) {
		testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
			m, cleanup, err := test_helpers.NewTestMetastructure(t, nil)
			defer cleanup()
			require.NoError(t, err)

			target := newTestTarget()
			vpc := newIntegrationVPC("test-vpc", constants.UnmanagedStack, false)

			unmanagedForma := &pkgmodel.Forma{
				Stacks:    []pkgmodel.Stack{{Label: constants.UnmanagedStack}},
				Targets:   []pkgmodel.Target{target},
				Resources: []pkgmodel.Resource{vpc},
			}

			_, err = m.Datastore.StoreStack(unmanagedForma, "test-discovery")
			require.NoError(t, err)

			allStacks, err := m.Datastore.LoadAllStacks()
			require.NoError(t, err)
			require.Len(t, allStacks, 1)

			origVpcKsuid := allStacks[0].Resources[0].Ksuid

			managedForma := &pkgmodel.Forma{
				Stacks:  []pkgmodel.Stack{{Label: "managed-stack"}},
				Targets: []pkgmodel.Target{target},
				Resources: []pkgmodel.Resource{
					newIntegrationVPC("test-vpc", "managed-stack", true),
				},
			}

			updates, err := resource_update.GenerateResourceUpdates(
				managedForma,
				pkgmodel.CommandApply,
				pkgmodel.FormaApplyModeReconcile,
				resource_update.FormaCommandSourceUser,
				[]*pkgmodel.Target{&target},
				m.Datastore,
			)
			require.NoError(t, err)
			require.Len(t, updates, 1)

			update := updates[0]
			assert.Equal(t, resource_update.OperationUpdate, update.Operation)
			assert.Equal(t, origVpcKsuid, update.Resource.Ksuid)
			assert.Equal(t, "managed-stack", update.StackLabel)
		})
	})

	t.Run("verify update operations not create", func(t *testing.T) {
		testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
			m, cleanup, err := test_helpers.NewTestMetastructure(t, nil)
			defer cleanup()
			require.NoError(t, err)

			target := newTestTarget()
			vpc := newIntegrationVPC("transition-vpc", constants.UnmanagedStack, false)

			unmanagedForma := &pkgmodel.Forma{
				Stacks:    []pkgmodel.Stack{{Label: constants.UnmanagedStack}},
				Targets:   []pkgmodel.Target{target},
				Resources: []pkgmodel.Resource{vpc},
			}

			_, err = m.Datastore.StoreStack(unmanagedForma, "test-discovery")
			require.NoError(t, err)

			managedForma := &pkgmodel.Forma{
				Stacks:  []pkgmodel.Stack{{Label: "production"}},
				Targets: []pkgmodel.Target{target},
				Resources: []pkgmodel.Resource{
					newIntegrationVPC("transition-vpc", "production", true),
				},
			}

			allStacks, err := m.Datastore.LoadAllStacks()
			require.NoError(t, err)

			updates, err := resource_update.GenerateResourceUpdates(
				managedForma,
				pkgmodel.CommandApply,
				pkgmodel.FormaApplyModeReconcile,
				resource_update.FormaCommandSourceUser,
				[]*pkgmodel.Target{&target},
				m.Datastore,
			)
			require.NoError(t, err)
			require.Len(t, updates, 1)

			update := updates[0]
			assert.Equal(t, resource_update.OperationUpdate, update.Operation)
			assert.Equal(t, "transition-vpc", update.Resource.Label)
			assert.Equal(t, "production", update.StackLabel)

			if len(allStacks) > 0 && len(allStacks[0].Resources) > 0 {
				origKsuid := allStacks[0].Resources[0].Ksuid
				assert.Equal(t, origKsuid, update.Resource.Ksuid)
			}
		})
	})
}

func newTestTarget() pkgmodel.Target {
	return pkgmodel.Target{
		Label:     "test-target",
		Namespace: "test-namespace",
		Config:    json.RawMessage(`{}`),
	}
}

func newIntegrationVPC(label, stack string, managed bool) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      label,
		Type:       "AWS::EC2::VPC",
		Stack:      stack,
		Target:     "test-target",
		Properties: json.RawMessage(`{"CidrBlock": "10.0.0.0/16"}`),
		Managed:    managed,
	}
}
