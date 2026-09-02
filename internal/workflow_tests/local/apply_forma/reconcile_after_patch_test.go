// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const (
	admissionProducerType = "FakeAWS::Admission::Producer"
	admissionConsumerType = "FakeAWS::Admission::Consumer"
)

// admissionState is a mutex-guarded string: the last value a fake resource
// reported as its cloud state.
type admissionState struct {
	mu    sync.Mutex
	value string
}

func (s *admissionState) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *admissionState) set(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = v
}

// admissionFieldValue reads a named top-level property, unwrapping a resolved
// {"$ref":...,"$value":...} envelope if the field arrives that way.
func admissionFieldValue(raw json.RawMessage, field string) (string, bool) {
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

func admissionProps(resourceType, value string) json.RawMessage {
	switch resourceType {
	case admissionProducerType:
		return json.RawMessage(fmt.Sprintf(`{"Name":"producer","Color":"%s"}`, value))
	case admissionConsumerType:
		return json.RawMessage(fmt.Sprintf(`{"Name":"consumer","ProducerColor":"%s"}`, value))
	}
	return nil
}

func admissionSchema(field string) pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", field},
		Hints: map[string]pkgmodel.FieldHint{
			"Name": {CreateOnly: true},
		},
	}
}

// admissionFullForma declares the producer with the given literal and the
// consumer referencing the producer's Color field.
func admissionFullForma(producerColor string) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "producer",
				Type:       admissionProducerType,
				Properties: json.RawMessage(fmt.Sprintf(`{"Name":"producer","Color":"%s"}`, producerColor)),
				Stack:      "test-stack",
				Target:     "test-target",
				Managed:    true,
				Schema:     admissionSchema("Color"),
			},
			{
				Label: "consumer",
				Type:  admissionConsumerType,
				Properties: json.RawMessage(`{
					"Name": "consumer",
					"ProducerColor": {
						"$res": true,
						"$label": "producer",
						"$type": "` + admissionProducerType + `",
						"$stack": "test-stack",
						"$property": "Color"
					}
				}`),
				Stack:   "test-stack",
				Target:  "test-target",
				Managed: true,
				Schema:  admissionSchema("ProducerColor"),
			},
		},
		Targets: []pkgmodel.Target{{Label: "test-target"}},
	}
}

// admissionProducerOnlyForma declares only the producer, for a patch that
// changes just the referenced field.
func admissionProducerOnlyForma(producerColor string) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "producer",
				NativeID:   "producer",
				Type:       admissionProducerType,
				Properties: json.RawMessage(fmt.Sprintf(`{"Name":"producer","Color":"%s"}`, producerColor)),
				Stack:      "test-stack",
				Target:     "test-target",
				Managed:    true,
				Schema:     admissionSchema("Color"),
			},
		},
		Targets: []pkgmodel.Target{{Label: "test-target"}},
	}
}

// A reconcile submitted after a patch changed a referenced producer field must
// be admitted without force when the forma already reflects the patched value:
// the producer's modification is absorbed (its declaration matches current
// state), and the consumer's pending convergence is explained by its own
// reference edge, not out-of-band drift. The reconcile then converges the
// consumer to the referenced value.
func TestApplyForma_ReconcileAfterPatchOnReferencedField_AdmittedAndConvergesConsumer(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		producer := &admissionState{value: "blue"}
		consumer := &admissionState{value: "blue"}

		stateFor := func(resourceType string) (*admissionState, string) {
			switch resourceType {
			case admissionProducerType:
				return producer, "Color"
			case admissionConsumerType:
				return consumer, "ProducerColor"
			}
			return nil, ""
		}

		var mu sync.Mutex
		consumerUpdateValues := []string{}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				state, field := stateFor(req.ResourceType)
				if state == nil {
					return nil, nil
				}
				if v, ok := admissionFieldValue(req.Properties, field); ok {
					state.set(v)
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label + "-create",
					NativeID:           req.Label,
					ResourceProperties: admissionProps(req.ResourceType, state.get()),
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				state, _ := stateFor(req.ResourceType)
				if state == nil {
					return nil, nil
				}
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   string(admissionProps(req.ResourceType, state.get())),
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				state, field := stateFor(req.ResourceType)
				if state == nil {
					return nil, nil
				}
				if v, ok := admissionFieldValue(req.DesiredProperties, field); ok {
					state.set(v)
					if req.ResourceType == admissionConsumerType {
						mu.Lock()
						consumerUpdateValues = append(consumerUpdateValues, v)
						mu.Unlock()
					}
				}
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label + "-update",
					NativeID:           req.NativeID,
					ResourceProperties: admissionProps(req.ResourceType, state.get()),
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Command 1: reconcile creates producer and consumer.
		_, err = m.ApplyForma(admissionFullForma("blue"), defaultApplyConfig(), "test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// Command 2: patch mode changes only the producer's referenced field.
		_, err = m.ApplyForma(
			admissionProducerOnlyForma("green"),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: false},
			"test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// Command 3: reconcile with the forma as it stands after the patch
		// (producer already declares green; the consumer's reference is
		// textually unchanged). Must be admitted without force.
		_, err = m.ApplyForma(admissionFullForma("green"), defaultApplyConfig(), "test", "", "")
		var rejected apimodel.FormaReconcileRejectedError
		if errors.As(err, &rejected) {
			t.Fatalf("reconcile after a patch on a referenced field was rejected for modifications its reference edge explains: %+v", rejected.ModifiedStacks)
		}
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.NotEmpty(t, cmds)
		require.Equal(t, forma_command.CommandStateSuccess, cmds[0].State, "the reconcile must succeed")

		// The reconcile must have converged the consumer to the patched value.
		mu.Lock()
		values := append([]string{}, consumerUpdateValues...)
		mu.Unlock()
		require.Contains(t, values, "green", "the consumer's plugin must receive the referenced field's patched value")
		require.Equal(t, "green", consumer.get(), "the consumer's cloud state must converge to the referenced value")
	})
}

// admissionCrossStackForma declares only the consumer's stack; the consumer
// references the producer's field across stacks.
func admissionCrossStackConsumerForma() *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "consumer-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "consumer",
				Type:  admissionConsumerType,
				Properties: json.RawMessage(`{
					"Name": "consumer",
					"ProducerColor": {
						"$res": true,
						"$label": "producer",
						"$type": "` + admissionProducerType + `",
						"$stack": "producer-stack",
						"$property": "Color"
					}
				}`),
				Stack:   "consumer-stack",
				Target:  "test-target",
				Managed: true,
				Schema:  admissionSchema("ProducerColor"),
			},
		},
		Targets: []pkgmodel.Target{{Label: "test-target"}},
	}
}

func admissionCrossStackProducerForma(color string) *pkgmodel.Forma {
	return &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "producer-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "producer",
				Type:       admissionProducerType,
				Properties: json.RawMessage(fmt.Sprintf(`{"Name":"producer","Color":"%s"}`, color)),
				Stack:      "producer-stack",
				Target:     "test-target",
				Managed:    true,
				Schema:     admissionSchema("Color"),
			},
		},
		Targets: []pkgmodel.Target{{Label: "test-target"}},
	}
}

// A reconcile of the consumer's stack after a patch changed the producer in a
// DIFFERENT stack: admission consults only the stacks the forma declares, so
// the producer-stack modification does not reject the consumer-stack
// reconcile, and the consumer still converges to the referenced value.
func TestApplyForma_ReconcileConsumerStack_AfterCrossStackProducerPatch_Admitted(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		producer := &admissionState{value: "blue"}
		consumer := &admissionState{value: "blue"}

		stateFor := func(resourceType string) (*admissionState, string) {
			switch resourceType {
			case admissionProducerType:
				return producer, "Color"
			case admissionConsumerType:
				return consumer, "ProducerColor"
			}
			return nil, ""
		}

		overrides := &plugin.ResourcePluginOverrides{
			Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
				state, field := stateFor(req.ResourceType)
				if state == nil {
					return nil, nil
				}
				if v, ok := admissionFieldValue(req.Properties, field); ok {
					state.set(v)
				}
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label + "-create",
					NativeID:           req.Label,
					ResourceProperties: admissionProps(req.ResourceType, state.get()),
				}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				state, _ := stateFor(req.ResourceType)
				if state == nil {
					return nil, nil
				}
				return &resource.ReadResult{
					ResourceType: req.ResourceType,
					Properties:   string(admissionProps(req.ResourceType, state.get())),
				}, nil
			},
			Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
				state, field := stateFor(req.ResourceType)
				if state == nil {
					return nil, nil
				}
				if v, ok := admissionFieldValue(req.DesiredProperties, field); ok {
					state.set(v)
				}
				return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationUpdate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          req.Label + "-update",
					NativeID:           req.NativeID,
					ResourceProperties: admissionProps(req.ResourceType, state.get()),
				}}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		// Create the producer, then the consumer referencing it cross-stack.
		_, err = m.ApplyForma(admissionCrossStackProducerForma("blue"), defaultApplyConfig(), "test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		_, err = m.ApplyForma(admissionCrossStackConsumerForma(), defaultApplyConfig(), "test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// Patch the producer's referenced field in its own stack.
		patchForma := admissionCrossStackProducerForma("green")
		patchForma.Resources[0].NativeID = "producer"
		_, err = m.ApplyForma(
			patchForma,
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch, Simulate: false},
			"test", "", "")
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		// Reconcile the consumer's stack only. Must be admitted without force.
		_, err = m.ApplyForma(admissionCrossStackConsumerForma(), defaultApplyConfig(), "test", "", "")
		var rejected apimodel.FormaReconcileRejectedError
		if errors.As(err, &rejected) {
			t.Fatalf("consumer-stack reconcile was rejected for a producer-stack modification: %+v", rejected.ModifiedStacks)
		}
		require.NoError(t, err)
		waitForApplyComplete(t, m)

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.NotEmpty(t, cmds)
		require.Equal(t, forma_command.CommandStateSuccess, cmds[0].State, "the consumer-stack reconcile must succeed")
		require.Equal(t, "green", consumer.get(), "the consumer must converge to the cross-stack referenced value")
	})
}
