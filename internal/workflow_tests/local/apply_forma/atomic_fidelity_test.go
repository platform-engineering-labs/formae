// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync/atomic"
	"testing"

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

func customResource(stack, spec string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:  "issuer",
		Type:   "FakeAWS::Custom::Resource",
		Stack:  stack,
		Target: "test-target",
		Schema: pkgmodel.Schema{
			Identifier: "FormaeId",
			Fields:     []string{"ApiVersion", "Kind", "FormaeId", "Spec"},
			Hints: map[string]pkgmodel.FieldHint{
				"Spec": {UpdateMethod: pkgmodel.FieldUpdateMethodAtomic, PreserveEmptyValues: true},
			},
		},
		Properties: json.RawMessage(`{
			"ApiVersion": "cert-manager.io/v1",
			"Kind": "ClusterIssuer",
			"FormaeId": "cert-manager.io/v1/ClusterIssuer//issuer",
			"Spec": ` + spec + `
		}`),
	}
}

// A preserveEmptyValues-hinted field whose declaration IS an empty object
// member reaches the plugin verbatim on create, re-applies clean, and a later
// change arrives as one whole-field value still carrying the empty member.
func TestApplyForma_PreserveEmptySpec_VerbatimCreateCleanReapplyWholeValueUpdate(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var createSpec, updateSpec atomic.Value
		var updateCalls atomic.Int32
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				createSpec.Store(gjson.GetBytes(req.Properties, "Spec").Raw)
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationCreate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "cr-create-1",
					NativeID:        "cr-native-1",
				}}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				updateCalls.Add(1)
				updateSpec.Store(gjson.GetBytes(req.DesiredProperties, "Spec").Raw)
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:       resource.OperationUpdate,
					OperationStatus: resource.OperationStatusSuccess,
					RequestID:       "cr-update-1",
					NativeID:        "cr-native-1",
				}}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		targets := []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}}
		forma := func(spec string) *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks:    []pkgmodel.Stack{{Label: stack}},
				Resources: []pkgmodel.Resource{customResource(stack, spec)},
				Targets:   targets,
			}
		}

		_, err = m.ApplyForma(forma(`{"selfSigned":{}}`), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		created, _ := createSpec.Load().(string)
		assert.JSONEq(t, `{"selfSigned":{}}`, created,
			"the plugin must receive the empty-object member verbatim on create")

		resp, err := m.ApplyForma(forma(`{"selfSigned":{}}`), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired, "identical re-apply must plan nothing")

		_, err = m.ApplyForma(forma(`{"selfSigned":{},"other":"x"}`), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)
		require.Greater(t, updateCalls.Load(), int32(0), "the change must reach the plugin")
		updated, _ := updateSpec.Load().(string)
		assert.JSONEq(t, `{"selfSigned":{},"other":"x"}`, updated,
			"the update carries the whole field still holding the empty member")
	})
}
