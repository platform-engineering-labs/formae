// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// Two references to the same source property with different JSON extractions
// each receive their own extracted value.
func TestGenerateResourceUpdates_TwoExtractionsOfOneProperty_DoNotCollide(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Doc"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "User", "Pass"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Occurrence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "p", "Doc": "{\"user\":\"u1\",\"pass\":\"p1\"}"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(`{
					"Name": "c",
					"User": {"$ref": "formae://%[1]s#/Doc", "$value": "u1", "$json": "user"},
					"Pass": {"$ref": "formae://%[1]s#/Doc", "$value": "p1", "$json": "pass"}
				}`, producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Reconcile: the producer's Doc changes; the consumer's declaration is
	// unchanged, expressed via $res sugar with each occurrence carrying its
	// own $json extraction path.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Occurrence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p", "Doc": "{\"user\":\"u2\",\"pass\":\"p2\"}"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "c",
					"User": {
						"$res":      true,
						"$label":    "producer",
						"$type":     "FakeAWS::Occurrence::Producer",
						"$stack":    "test-stack",
						"$property": "Doc",
						"$json":     "user"
					},
					"Pass": {
						"$res":      true,
						"$label":    "producer",
						"$type":     "FakeAWS::Occurrence::Producer",
						"$stack":    "test-stack",
						"$property": "Doc",
						"$json":     "pass"
					}
				}`),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil)
	require.NoError(t, err)

	planned := map[string]*ResourceUpdate{}
	for i := range updates {
		planned[updates[i].DesiredState.Label] = &updates[i]
	}

	consumer, ok := planned["consumer"]
	require.True(t, ok, "consumer following the producer's changed Doc must stay in the changeset")
	patch := string(consumer.DesiredState.PatchDocument)
	assert.Contains(t, patch, "u2", "the User occurrence must extract its own leaf")
	assert.Contains(t, patch, "p2", "the Pass occurrence must extract its own leaf")
}

// A consumer declaring a requiredOnUpdate field follows a changed reference
// exactly like any other consumer, and an unchanged reference does not plan
// it.
func TestGenerateResourceUpdates_RequiredOnUpdateConsumer_BehavesIdentically(t *testing.T) {
	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Value"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef", "Token"},
		Hints:      map[string]pkgmodel.FieldHint{"Token": {RequiredOnUpdate: true}},
	}

	buildExistingStack := func(producerKsuid string) *pkgmodel.Forma {
		return &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label: "parent", Type: "FakeAWS::Occurrence::Parent",
					Stack: "test-stack", Target: "test-target",
					Schema: producerSchema, Ksuid: producerKsuid,
					Properties: json.RawMessage(`{"Name": "parent-1", "Value": "hello"}`),
				},
				{
					Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
					Stack: "test-stack", Target: "test-target",
					Schema: consumerSchema, Ksuid: util.NewID(),
					Properties: json.RawMessage(fmt.Sprintf(
						`{"Name": "consumer-1", "Token": "t1", "ParentRef": {"$ref": "formae://%s#/Value", "$value": "hello"}}`,
						producerKsuid)),
				},
			},
		}
	}

	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}
	targets := []pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	// Scenario A: the producer's Value changes, consumer declaration
	// (including the requiredOnUpdate Token) is unchanged -> consumer
	// follows the changed reference like any other consumer.
	t.Run("changed root plans the consumer", func(t *testing.T) {
		ds, _ := GetDeps(t)
		producerKsuid := util.NewID()

		_, err := ds.StoreStack(buildExistingStack(producerKsuid), "previous-command")
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
			Targets: targets,
			Resources: []pkgmodel.Resource{
				{
					Label: "parent", Type: "FakeAWS::Occurrence::Parent",
					Stack: "test-stack", Target: "test-target",
					Schema:     producerSchema,
					Properties: json.RawMessage(`{"Name": "parent-1", "Value": "world"}`),
				},
				{
					Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
					Stack: "test-stack", Target: "test-target",
					Schema: consumerSchema,
					Properties: json.RawMessage(`{
						"Name": "consumer-1",
						"Token": "t1",
						"ParentRef": {
							"$res":      true,
							"$label":    "parent",
							"$type":     "FakeAWS::Occurrence::Parent",
							"$stack":    "test-stack",
							"$property": "Value"
						}
					}`),
				},
			},
		}

		updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
			FormaCommandSourceUser, existingTargets, ds, nil, nil)
		require.NoError(t, err)

		planned := map[string]*ResourceUpdate{}
		for i := range updates {
			planned[updates[i].DesiredState.Label] = &updates[i]
		}

		consumer, ok := planned["consumer"]
		require.True(t, ok, "consumer following the producer's changed field must stay in the changeset")
		assert.Contains(t, string(consumer.DesiredState.PatchDocument), "world",
			"the consumer's patch must carry the producer's new value")
	})

	// Scenario B: the producer's Value is resubmitted unchanged, consumer
	// declaration is unchanged -> requiredOnUpdate alone must not conjure an
	// update for an otherwise unchanged consumer.
	t.Run("unchanged root plans nothing", func(t *testing.T) {
		ds, _ := GetDeps(t)
		producerKsuid := util.NewID()

		_, err := ds.StoreStack(buildExistingStack(producerKsuid), "previous-command")
		require.NoError(t, err)

		forma := &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
			Targets: targets,
			Resources: []pkgmodel.Resource{
				{
					Label: "parent", Type: "FakeAWS::Occurrence::Parent",
					Stack: "test-stack", Target: "test-target",
					Schema:     producerSchema,
					Properties: json.RawMessage(`{"Name": "parent-1", "Value": "hello"}`),
				},
				{
					Label: "consumer", Type: "FakeAWS::Occurrence::Consumer",
					Stack: "test-stack", Target: "test-target",
					Schema: consumerSchema,
					Properties: json.RawMessage(`{
						"Name": "consumer-1",
						"Token": "t1",
						"ParentRef": {
							"$res":      true,
							"$label":    "parent",
							"$type":     "FakeAWS::Occurrence::Parent",
							"$stack":    "test-stack",
							"$property": "Value"
						}
					}`),
				},
			},
		}

		updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
			FormaCommandSourceUser, existingTargets, ds, nil, nil)
		require.NoError(t, err)

		for i := range updates {
			if updates[i].DesiredState.Label == "consumer" {
				assert.Empty(t, string(updates[i].DesiredState.PatchDocument),
					"requiredOnUpdate alone must not conjure an update for an otherwise unchanged consumer")
			}
		}
	})
}
