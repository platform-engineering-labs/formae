// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const (
	chainRefRootType     = "FakeAWS::Chain::Root"
	chainRefMiddleType   = "FakeAWS::Chain::Middle"
	chainRefConsumerType = "FakeAWS::Chain::Consumer"
)

// chainRefUpdateCall records one plugin Update request: which resource it
// targeted and the full desired document it carried.
type chainRefUpdateCall struct {
	ResourceType      string
	Label             string
	DesiredProperties json.RawMessage
}

// chainRefUpdateCapture accumulates every plugin Update call across a test run.
type chainRefUpdateCapture struct {
	mu    sync.Mutex
	calls []chainRefUpdateCall
}

func (c *chainRefUpdateCapture) add(call chainRefUpdateCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

// forLabel returns the last captured Update call for the given resource
// label, if any.
func (c *chainRefUpdateCapture) forLabel(label string) (chainRefUpdateCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var found chainRefUpdateCall
	ok := false
	for _, call := range c.calls {
		if call.Label == label {
			found = call
			ok = true
		}
	}
	return found, ok
}

// chainRefFieldValue reads a named top-level property out of a resource
// properties document, unwrapping a resolved {"$ref":...,"$value":...}
// envelope if the field arrives that way instead of as a plain scalar.
func chainRefFieldValue(raw json.RawMessage, field string) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	v, ok := m[field]
	if !ok {
		return "", false
	}
	switch val := v.(type) {
	case string:
		return val, true
	case map[string]any:
		if s, ok := val["$value"].(string); ok {
			return s, true
		}
	}
	return "", false
}

// chainRefProps builds the properties document a FakeAWS chain resource
// reports as its cloud state, given the single value it carries.
func chainRefProps(resourceType, value string) json.RawMessage {
	switch resourceType {
	case chainRefRootType:
		return json.RawMessage(fmt.Sprintf(`{"Name":"root","Color":"%s"}`, value))
	case chainRefMiddleType:
		return json.RawMessage(fmt.Sprintf(`{"Name":"middle","RootColor":"%s"}`, value))
	case chainRefConsumerType:
		return json.RawMessage(fmt.Sprintf(`{"Name":"consumer","MiddleColor":"%s"}`, value))
	}
	return nil
}

// chainRefStateFor returns the state cell holding the last-known value for a
// resource type, and the name of the field that value lives under.
func chainRefStateFor(resourceType string, root, middle, consumer *chainRefState) (*chainRefState, string) {
	switch resourceType {
	case chainRefRootType:
		return root, "Color"
	case chainRefMiddleType:
		return middle, "RootColor"
	case chainRefConsumerType:
		return consumer, "MiddleColor"
	}
	return nil, ""
}

// chainRefState is a mutex-guarded string: the last value a FakeAWS chain
// resource reported as its cloud state.
type chainRefState struct {
	mu    sync.Mutex
	value string
}

func newChainRefState(initial string) *chainRefState {
	return &chainRefState{value: initial}
}

func (s *chainRefState) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *chainRefState) set(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = v
}

// chainRefSchema returns the schema for a chain resource whose only mutable
// field is named field.
func chainRefSchema(field string) pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", field},
		Hints: map[string]pkgmodel.FieldHint{
			"Name": {CreateOnly: true},
		},
	}
}

// chainRefForma builds the three-resource chain (root -> middle -> consumer)
// for the given root literal. middle references root's Color field via $res
// sugar; consumer references middle's RootColor field via $res sugar, so
// consumer is two hops from the literal that changes.
func chainRefForma(rootColor string) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "root",
				Type:       chainRefRootType,
				Properties: json.RawMessage(fmt.Sprintf(`{"Name":"root","Color":"%s"}`, rootColor)),
				Stack:      "test-stack",
				Target:     "test-target",
				Managed:    true,
				Schema:     chainRefSchema("Color"),
			},
			{
				Label: "middle",
				Type:  chainRefMiddleType,
				Properties: json.RawMessage(`{
					"Name": "middle",
					"RootColor": {
						"$res": true,
						"$label": "root",
						"$type": "` + chainRefRootType + `",
						"$stack": "test-stack",
						"$property": "Color"
					}
				}`),
				Stack:   "test-stack",
				Target:  "test-target",
				Managed: true,
				Schema:  chainRefSchema("RootColor"),
			},
			{
				Label: "consumer",
				Type:  chainRefConsumerType,
				Properties: json.RawMessage(`{
					"Name": "consumer",
					"MiddleColor": {
						"$res": true,
						"$label": "middle",
						"$type": "` + chainRefMiddleType + `",
						"$stack": "test-stack",
						"$property": "RootColor"
					}
				}`),
				Stack:   "test-stack",
				Target:  "test-target",
				Managed: true,
				Schema:  chainRefSchema("MiddleColor"),
			},
		},
		Targets: []pkgmodel.Target{{Label: "test-target"}},
	}
}

// TestApplyForma_LiteralMove_ConvergesTwoHopChainInOneApply pins the
// user-visible outcome of plan-time reference classification being
// recursive: a literal change on a chain root reaches every transitive
// follower in the same apply. The middle resource and the consumer both
// receive the new value, and a subsequent simulate plans no changes.
func TestApplyForma_LiteralMove_ConvergesTwoHopChainInOneApply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		root := newChainRefState("blue")
		middle := newChainRefState("blue")
		consumer := newChainRefState("blue")

		updates := &chainRefUpdateCapture{}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				state, field := chainRefStateFor(req.ResourceType, root, middle, consumer)
				if state == nil {
					return nil, nil
				}
				if v, ok := chainRefFieldValue(req.Properties, field); ok {
					state.set(v)
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label + "-create",
					NativeID:           req.Label,
					ResourceProperties: chainRefProps(req.ResourceType, state.get()),
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				state, _ := chainRefStateFor(req.ResourceType, root, middle, consumer)
				if state == nil {
					return nil, nil
				}
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   string(chainRefProps(req.ResourceType, state.get())),
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				state, field := chainRefStateFor(req.ResourceType, root, middle, consumer)
				if state == nil {
					return nil, nil
				}
				updates.add(chainRefUpdateCall{
					ResourceType:      req.ResourceType,
					Label:             req.Label,
					DesiredProperties: req.DesiredProperties,
				})
				if v, ok := chainRefFieldValue(req.DesiredProperties, field); ok {
					state.set(v)
				}
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label + "-update",
					NativeID:           req.NativeID,
					ResourceProperties: chainRefProps(req.ResourceType, state.get()),
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// ── Apply #1: create the chain with root's literal "blue" ────────────
		_, err = m.ApplyForma(chainRefForma("blue"), defaultApplyConfig(), "test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.NotEmpty(t, cmds)
		require.Equal(t, forma_command.CommandStateSuccess, cmds[0].State, "initial apply (create chain) must succeed")

		// ── Apply #2: move only root's literal from "blue" to "green" ────────
		_, err = m.ApplyForma(chainRefForma("green"), defaultApplyConfig(), "test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err = m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.NotEmpty(t, cmds)
		require.Equal(t, forma_command.CommandStateSuccess, cmds[0].State, "the literal move must succeed")

		middleUpdate, ok := updates.forLabel("middle")
		require.True(t, ok, "the middle resource must receive a plugin Update in the same apply")
		middleValue, ok := chainRefFieldValue(middleUpdate.DesiredProperties, "RootColor")
		require.True(t, ok, "middle's Update must carry a RootColor value")
		assert.Equal(t, "green", middleValue,
			"the middle resource's plugin Update must carry the new root literal")

		consumerUpdate, ok := updates.forLabel("consumer")
		require.True(t, ok, "the consumer resource must receive a plugin Update in the same apply")
		consumerValue, ok := chainRefFieldValue(consumerUpdate.DesiredProperties, "MiddleColor")
		require.True(t, ok, "consumer's Update must carry a MiddleColor value")
		assert.Equal(t, "green", consumerValue,
			"the consumer, two hops from the literal, must also converge in the same apply")

		// ── Simulate #3: same forma, chain is already converged ──────────────
		simConfig := defaultApplyConfig()
		simConfig.Simulate = true
		simResp, err := m.ApplyForma(chainRefForma("green"), simConfig, "test", "", "")
		require.NoError(t, err)
		assert.False(t, simResp.Simulation.ChangesRequired,
			"a converged chain must plan no changes")
		assert.Empty(t, simResp.Simulation.Command.ResourceUpdates,
			"a converged chain must plan zero resource updates")
	})
}
