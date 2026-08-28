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

// A consumer follows a producer's plain mutable field through a resolvable.
// The producer's field changes to a value that is fully known at plan time (it
// is a literal in the forma). The consumer's declaration is untouched.
//
// The consumer must stay in the changeset with a patch that moves it to the
// producer's new value: its declared intent is "follow that field", and the
// field's post-apply value is right there in the forma.
func TestGenerateResourceUpdates_ConsumerFollowsChangedProducerField(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Value"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "parent",
				Type:       "FakeAWS::Versioned::Parent",
				Stack:      "test-stack",
				Target:     "test-target",
				Schema:     producerSchema,
				Ksuid:      producerKsuid,
				Properties: json.RawMessage(`{"Name": "parent-1", "Value": "hello"}`),
			},
			{
				Label:  "consumer",
				Type:   "FakeAWS::Versioned::Consumer",
				Stack:  "test-stack",
				Target: "test-target",
				Schema: consumerSchema,
				Ksuid:  consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "consumer-1", "ParentRef": {"$ref": "formae://%s#/Value", "$value": "hello"}}`,
					producerKsuid)),
			},
		},
	}

	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Only the producer's mutable field changes; its new value is a literal.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:      "parent",
				Type:       "FakeAWS::Versioned::Parent",
				Stack:      "test-stack",
				Target:     "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "parent-1", "Value": "world"}`),
			},
			{
				Label:  "consumer",
				Type:   "FakeAWS::Versioned::Consumer",
				Stack:  "test-stack",
				Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "consumer-1",
					"ParentRef": {
						"$res":      true,
						"$label":    "parent",
						"$type":     "FakeAWS::Versioned::Parent",
						"$stack":    "test-stack",
						"$property": "Value"
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

	require.Contains(t, planned, "parent", "producer whose field changed must be updated")

	consumer, ok := planned["consumer"]
	require.True(t, ok, "consumer following the producer's changed field must stay in the changeset")
	assert.Contains(t, string(consumer.DesiredState.PatchDocument), "world",
		"the consumer's patch must carry the producer's new value")
}

// A populated SetOnce property keeps its persisted value regardless of what
// the forma resubmits, so a consumer referencing it must NOT be scheduled for
// the resubmitted value: classification runs on effective desired state.
func TestGenerateResourceUpdates_ConsumerIgnoresResubmittedSetOnceProducerField(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Value"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "parent", Type: "FakeAWS::Versioned::Parent",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "parent-1", "Value": {"$value": "hello", "$strategy": "SetOnce"}}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Versioned::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "consumer-1", "ParentRef": {"$ref": "formae://%s#/Value", "$value": "hello"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "parent", Type: "FakeAWS::Versioned::Parent",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "parent-1", "Value": {"$value": "world-resubmitted", "$strategy": "SetOnce"}}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Versioned::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "consumer-1",
					"ParentRef": {
						"$res": true, "$label": "parent",
						"$type": "FakeAWS::Versioned::Parent",
						"$stack": "test-stack", "$property": "Value"
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

	for i := range updates {
		if updates[i].DesiredState.Label == "consumer" {
			assert.NotContains(t, string(updates[i].DesiredState.PatchDocument), "world-resubmitted",
				"the consumer must not be scheduled for a value the producer never adopts")
		}
	}
}

// A literal move propagates through a two-hop chain in one command: the
// root's field changes, the middle resource references it, and a consumer
// references the middle resource. All three must be planned, with the
// consumer's patch carrying the root's new value.
func TestGenerateResourceUpdates_LiteralMovesThroughTwoHopChain(t *testing.T) {
	ds, _ := GetDeps(t)

	rootSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Value"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	middleSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "ParentRef"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}

	rootKsuid := util.NewID()
	middleKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label:      "root",
				Type:       "FakeAWS::Versioned::Parent",
				Stack:      "test-stack",
				Target:     "test-target",
				Schema:     rootSchema,
				Ksuid:      rootKsuid,
				Properties: json.RawMessage(`{"Name": "root-1", "Value": "hello"}`),
			},
			{
				Label:  "middle",
				Type:   "FakeAWS::Versioned::Consumer",
				Stack:  "test-stack",
				Target: "test-target",
				Schema: middleSchema,
				Ksuid:  middleKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "middle-1", "ParentRef": {"$ref": "formae://%s#/Value", "$value": "hello"}}`,
					rootKsuid)),
			},
			{
				Label:  "consumer",
				Type:   "FakeAWS::Versioned::Consumer",
				Stack:  "test-stack",
				Target: "test-target",
				Schema: consumerSchema,
				Ksuid:  consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "consumer-1", "ParentRef": {"$ref": "formae://%s#/ParentRef", "$value": "hello"}}`,
					middleKsuid)),
			},
		},
	}

	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Only the root's mutable field changes; its new value is a literal. The
	// middle and consumer are resubmitted with the $res sugar, unchanged.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label:      "root",
				Type:       "FakeAWS::Versioned::Parent",
				Stack:      "test-stack",
				Target:     "test-target",
				Schema:     rootSchema,
				Properties: json.RawMessage(`{"Name": "root-1", "Value": "world"}`),
			},
			{
				Label:  "middle",
				Type:   "FakeAWS::Versioned::Consumer",
				Stack:  "test-stack",
				Target: "test-target",
				Schema: middleSchema,
				Properties: json.RawMessage(`{
					"Name": "middle-1",
					"ParentRef": {
						"$res":      true,
						"$label":    "root",
						"$type":     "FakeAWS::Versioned::Parent",
						"$stack":    "test-stack",
						"$property": "Value"
					}
				}`),
			},
			{
				Label:  "consumer",
				Type:   "FakeAWS::Versioned::Consumer",
				Stack:  "test-stack",
				Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "consumer-1",
					"ParentRef": {
						"$res":      true,
						"$label":    "middle",
						"$type":     "FakeAWS::Versioned::Consumer",
						"$stack":    "test-stack",
						"$property": "ParentRef"
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

	require.Contains(t, planned, "root", "root whose field changed must be updated")
	require.Contains(t, planned, "middle", "middle hop following the root's changed field must stay in the changeset")
	require.Contains(t, planned, "consumer", "consumer at the end of the chain must stay in the changeset")

	consumer := planned["consumer"]
	assert.Contains(t, string(consumer.DesiredState.PatchDocument), "world",
		"the consumer's patch must carry the root's new value through the two-hop chain")
}
