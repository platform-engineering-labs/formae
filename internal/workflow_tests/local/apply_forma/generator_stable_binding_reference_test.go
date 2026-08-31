// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

const refParentType = "FakeAWS::Noted::Parent"

// referencingSecretStore is a fake secret store for a resource that carries a
// generator-bound opaque field AND a reference to a sibling resolved at
// execution time. Like deliveredValues it stands in for FakeAWS's own
// SecretsManager handling because FakeAWS mints one constant NativeID for
// every resource it creates.
//
// enrich decides what a Read reports for the opaque field: an enriching store
// returns the live secret on every Read (Secrets Manager's GetSecretValue), a
// non-enriching one never returns it at all (a write-only credential).
type referencingSecretStore struct {
	mu      sync.Mutex
	enrich  bool
	updates map[string][]string
	desired map[string][]string
	patches map[string][]string
	stored  map[string]json.RawMessage
}

func newReferencingSecretStore(enrich bool) *referencingSecretStore {
	return &referencingSecretStore{
		enrich:  enrich,
		updates: map[string][]string{},
		desired: map[string][]string{},
		patches: map[string][]string{},
		stored:  map[string]json.RawMessage{},
	}
}

func (s *referencingSecretStore) overrides() *plugin.ResourcePluginOverrides {
	return &plugin.ResourcePluginOverrides{
		Create: func(req *resource.CreateRequest) (*resource.CreateResult, error) {
			label := gjson.GetBytes(req.Properties, "Name").String()
			nativeID := "native-" + label
			s.mu.Lock()
			s.stored[nativeID] = req.Properties
			s.mu.Unlock()
			return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				NativeID:           nativeID,
				ResourceProperties: req.Properties,
			}}, nil
		},
		Update: func(req *resource.UpdateRequest) (*resource.UpdateResult, error) {
			props := gjson.ParseBytes(req.DesiredProperties)
			label := props.Get("Name").String()
			s.mu.Lock()
			if req.ResourceType != refParentType {
				patch := ""
				if req.PatchDocument != nil {
					patch = string(*req.PatchDocument)
				}
				s.updates[label] = append(s.updates[label], props.Get("SecretString").String())
				s.desired[label] = append(s.desired[label], string(req.DesiredProperties))
				s.patches[label] = append(s.patches[label], patch)
			}
			s.stored[req.NativeID] = req.DesiredProperties
			s.mu.Unlock()
			return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
				Operation:          resource.OperationUpdate,
				OperationStatus:    resource.OperationStatusSuccess,
				NativeID:           req.NativeID,
				ResourceProperties: req.DesiredProperties,
			}}, nil
		},
		Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
			s.mu.Lock()
			stored := s.stored[req.NativeID]
			enrich := s.enrich
			s.mu.Unlock()
			if len(stored) == 0 {
				stored = json.RawMessage("{}")
			}
			if req.ResourceType != refParentType && !enrich {
				reported, err := sjson.DeleteBytes(append(json.RawMessage(nil), stored...), "SecretString")
				if err != nil {
					return nil, err
				}
				stored = reported
			}
			return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(stored)}, nil
		},
	}
}

func (s *referencingSecretStore) updatedWith(label string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.updates[label]...)
}

func (s *referencingSecretStore) desiredFor(label string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.desired[label]...)
}

func (s *referencingSecretStore) patchesFor(label string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.patches[label]...)
}

// referencingSecretSchema is secretSchema plus a non-opaque field that holds a
// reference to a sibling resource.
func referencingSecretSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Id",
		Fields:     []string{"Name", "Description", "ParentRef", "SecretString"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {Opaque: true},
		},
	}
}

// A resource may hold a generator-bound opaque field and a reference to a
// sibling at the same time. Editing an unrelated field on such a resource
// leaves the binding stable, so nothing is drawn; the reference is still
// re-resolved at execution time, which re-derives the patch from the resolved
// document. That regeneration must carry the stable binding the same way the
// provider write does, so the edit beside the credential applies.
func TestApplyForma_GeneratorBoundSecret_UnrelatedEditBesideAnExecutionTimeReference(t *testing.T) {
	for _, tc := range []struct {
		name   string
		enrich bool
	}{
		{name: "enriching_read", enrich: true},
		{name: "non_enriching_read", enrich: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
				store := newReferencingSecretStore(tc.enrich)

				cfg := test_helpers.NewTestMetastructureConfig()
				cfg.Agent.Synchronization.Enabled = false
				m, def, err := test_helpers.NewTestMetastructureWithConfig(t, store.overrides(), cfg)
				defer def()
				require.NoError(t, err)

				stack := "test-stack-" + util.NewID()
				formaWithDescription := func(description string) *pkgmodel.Forma {
					return &pkgmodel.Forma{
						Stacks:     []pkgmodel.Stack{{Label: stack}},
						Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
						Generators: []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)},
						Resources: []pkgmodel.Resource{
							{
								Label:      "parent",
								Type:       refParentType,
								Stack:      stack,
								Target:     "test-target",
								Schema:     pkgmodel.Schema{Identifier: "Name", Fields: []string{"Name", "Value"}},
								Properties: json.RawMessage(`{"Name":"parent","Value":"hello"}`),
							},
							{
								Label:  "alpha",
								Type:   "FakeAWS::SecretsManager::Secret",
								Stack:  stack,
								Target: "test-target",
								Schema: referencingSecretSchema(),
								Properties: json.RawMessage(`{
									"Name": "alpha",
									"Description": "` + description + `",
									"ParentRef": {
										"$res":      true,
										"$label":    "parent",
										"$type":     "` + refParentType + `",
										"$stack":    "` + stack + `",
										"$property": "Name"
									},
									"SecretString": {
										"$gen":        true,
										"$label":      "db-password",
										"$stack":      "` + stack + `",
										"$output":     "value",
										"$visibility": "Opaque"
									}
								}`),
							},
						},
					}
				}

				_, err = m.ApplyForma(formaWithDescription("before"),
					&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
				require.NoError(t, err)
				waitForApplyComplete(t, m)

				firstGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
				require.NoError(t, err)
				require.NotEmpty(t, firstGeneration.GenerationID, "precondition: the first apply draws")

				appliedBinding := storedBinding(t, m, stack, "alpha")
				require.True(t, appliedBinding.Get("$hashed").Bool(),
					"precondition: the drawn value is stored as a digest")
				appliedValue := appliedBinding.Get("$value").String()
				require.NotEmpty(t, appliedValue)
				appliedResolvedFrom := appliedBinding.Get("$resolvedFrom").String()
				require.NotEmpty(t, appliedResolvedFrom,
					"precondition: the destination is stamped with the generation it was drawn from")

				_, err = m.ApplyForma(formaWithDescription("after"),
					&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
				require.NoError(t, err)
				waitForApplyComplete(t, m)

				cmds, err := m.Datastore.LoadFormaCommands()
				require.NoError(t, err)
				require.Len(t, cmds, 2)
				for _, cmd := range cmds {
					require.Equal(t, forma_command.CommandStateSuccess, cmd.State,
						"a stable binding beside a re-resolved reference must not block the edit")
				}

				updates := store.updatedWith("alpha")
				require.Len(t, updates, 1, "the unrelated edit must reach the provider")
				assert.NotContains(t, updates[0], "$gen",
					"a stable binding is frozen to the preserved sentinel, never dispatched as a bare envelope")

				desired := store.desiredFor("alpha")
				require.Len(t, desired, 1)
				assert.Contains(t, desired[0], `"SecretString":{"$opaque":"preserved"}`,
					"the credential must be handed to the provider as a preserved sentinel")
				assert.NotContains(t, desired[0], "$hashed",
					"a stored digest must never reach the provider in a secret's place")

				patches := store.patchesFor("alpha")
				require.Len(t, patches, 1)
				assert.Contains(t, patches[0], "/Description", "the edited field must be in the patch")
				assert.NotContains(t, patches[0], "/SecretString",
					"a binding nothing wrote must produce no patch op")

				secondGeneration, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
				require.NoError(t, err)
				assert.Equal(t, firstGeneration.GenerationID, secondGeneration.GenerationID,
					"an edit that leaves every binding stable must not draw")

				// What the row keeps is what the next apply classifies
				// against: the same digest, still marked a digest, still
				// stamped with the generation it was drawn from.
				edited := storedBinding(t, m, stack, "alpha")
				assert.Equal(t, appliedValue, edited.Get("$value").String())
				assert.True(t, edited.Get("$hashed").Bool())
				assert.Equal(t, appliedResolvedFrom, edited.Get("$resolvedFrom").String())

				resp, err := m.ApplyForma(formaWithDescription("after"),
					&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
				require.NoError(t, err)
				assert.False(t, resp.Simulation.ChangesRequired,
					"an unrelated edit must not leave the binding looking moved on the next apply")
				assert.Len(t, store.updatedWith("alpha"), 1, "no third write may reach the provider")
			})
		})
	}
}
