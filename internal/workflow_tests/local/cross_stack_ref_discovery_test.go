// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit || integration

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"testing"

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

// A resource that references a resource in ANOTHER stack must stay discoverable
// through that reference: the stored document has to keep the $ref envelope
// after apply, and after a sync, so FindResourcesDependingOnMany still returns
// it. That query is what a rotation's consumer walk uses to find the resources
// that must follow a rotated value, so a cross-stack consumer whose $ref is
// flattened to its resolved literal silently drops out of every dependency
// walk — the installation runtime task-def (which references the durable
// secret's ARN across stacks) is exactly this shape.
//
// The provider and consumer are applied in SEPARATE commands so the consumer's
// reference is translated through the datastore cross-stack lookup, not the
// in-command triplet map — the production path.
func TestCrossStackReference_StaysDiscoverableAfterApplyAndSync(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		const providerLabel = "provider"
		const consumerLabel = "consumer"
		const providerStack = "stack-a"
		const consumerStack = "stack-b"

		var mu sync.Mutex
		// what the fake plugin returns from Read, keyed by native id: the
		// resolved (plugin-format) properties a Create was given, so a sync sees
		// the cloud's literal value rather than the enveloped desired state.
		cloud := map[string]json.RawMessage{}
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				nid := req.Label + "-native"
				mu.Lock()
				cloud[nid] = append(json.RawMessage{}, req.Properties...)
				mu.Unlock()
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           nid,
					ResourceProperties: req.Properties,
				}}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				mu.Lock()
				cloud[req.NativeID] = append(json.RawMessage{}, req.DesiredProperties...)
				mu.Unlock()
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					NativeID:           req.NativeID,
					ResourceProperties: req.DesiredProperties,
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				mu.Lock()
				p := cloud[req.NativeID]
				mu.Unlock()
				if len(p) == 0 {
					p = json.RawMessage("{}")
				}
				return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(p)}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false // drive sync manually
		cfg.Agent.Retry.MaxRetries = 0
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		providerKsuid := util.NewID()
		providerSchema := pkgmodel.Schema{Identifier: "name", Fields: []string{"name", "arn"}}
		consumerSchema := pkgmodel.Schema{Identifier: "name", Fields: []string{"name", "uses"}}

		providerForma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: providerStack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{{
				Label: providerLabel, Type: "FakeAWS::Resource", Stack: providerStack,
				Target: "test-target", Ksuid: providerKsuid, Schema: providerSchema,
				Properties: json.RawMessage(`{"name":"provider","arn":"arn:test:provider"}`),
			}},
		}

		// The consumer references the provider's clear "arn" property across
		// stacks — a $res naming the provider by label/type/stack, the shape an
		// author writes, which the engine translates to a $ref at apply.
		consumerForma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: consumerStack}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
			Resources: []pkgmodel.Resource{{
				Label: consumerLabel, Type: "FakeAWS::Resource", Stack: consumerStack,
				Target: "test-target", Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"name": "consumer",
					"uses": {"$res": true, "$label": "provider", "$type": "FakeAWS::Resource", "$stack": "stack-a", "$property": "arn"}
				}`),
			}},
		}

		reconcile := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}

		_, err = m.ApplyForma(providerForma, reconcile, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		_, err = m.ApplyForma(consumerForma, reconcile, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// The dependency query is what a rotation's consumer walk uses; the
		// consumer must come back for the provider's ksuid.
		dependsOnProvider := func() bool {
			deps, derr := m.Datastore.FindResourcesDependingOnMany([]string{providerKsuid})
			require.NoError(t, derr)
			for _, group := range deps {
				for _, d := range group {
					if d != nil && d.Label == consumerLabel {
						return true
					}
				}
			}
			return false
		}
		consumerRefsProvider := func() bool {
			resources, lerr := m.Datastore.LoadResourcesByStack(consumerStack)
			require.NoError(t, lerr)
			for _, r := range resources {
				if r.Label == consumerLabel {
					return gjson.GetBytes(r.Properties, "uses.$ref").Exists()
				}
			}
			return false
		}

		require.True(t, consumerRefsProvider(), "after apply, the consumer's stored document must keep the $ref to the provider")
		require.True(t, dependsOnProvider(), "after apply, the cross-stack consumer must be discoverable via FindResourcesDependingOnMany")

		require.NoError(t, m.ForceSync())
		waitForApplyComplete(t, m)

		require.True(t, consumerRefsProvider(), "after sync, the consumer's stored document must still keep the $ref to the provider")
		require.True(t, dependsOnProvider(), "after sync, the cross-stack consumer must still be discoverable via FindResourcesDependingOnMany")
	})
}
