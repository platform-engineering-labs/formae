// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A reconcile that removes a property another resource references is
// rejected at plan time: the reference would dangle the moment the removal
// executes.
func TestGenerateResourceUpdates_ReferenceToRemovedPropertyFails(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Name": {CreateOnly: true},
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "p", "Tags": [{"Key": "team", "Value": "x"}]}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Tags", "$value": "[{\"Key\":\"team\",\"Value\":\"x\"}]"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Reconcile: the producer no longer declares Tags at all (whole EntitySet
	// property removed), while the consumer's declaration is unchanged.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "c",
					"Ref": {
						"$res":      true,
						"$label":    "producer",
						"$type":     "FakeAWS::Absence::Producer",
						"$stack":    "test-stack",
						"$property": "Tags"
					}
				}`),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	_, err = GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.Error(t, err)

	var refErr ReferenceToRemovedPropertyError
	require.True(t, errors.As(err, &refErr), "expected ReferenceToRemovedPropertyError, got: %v", err)
	assert.Equal(t, "consumer", refErr.ConsumerLabel)
	assert.Equal(t, "producer", refErr.SourceLabel)
	assert.Equal(t, "Tags", refErr.PropertyPath)
}

// In patch mode, omitting a property means leave it unchanged: a reference
// to it resolves the persisted value and nothing is removed.
func TestGenerateResourceUpdates_PatchModeOmission_LeavesReferenceUndisturbed(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Name": {CreateOnly: true},
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "p", "Tags": [{"Key": "team", "Value": "x"}]}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Tags", "$value": "[{\"Key\":\"team\",\"Value\":\"x\"}]"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Patch mode: producer omits Tags, but patch semantics leave omitted
	// fields untouched rather than removing them.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p"}`),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	_, err = GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModePatch,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)
}

// Reconcile omission of a plain property is not removal: no remove op is
// minted, the persisted value stands, and the reference stays quiet.
func TestGenerateResourceUpdates_ReconcileOmissionWithoutRemoval_KeepsPersistedResolution(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Note"},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::PlainProducer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "p", "Note": "n1"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Note", "$value": "n1"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Reconcile: the producer's forma declaration simply omits Note (a plain
	// scalar field with no collection hint); the consumer is unchanged.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::PlainProducer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Note", "$value": "n1"}}`,
					producerKsuid)),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)

	for i := range updates {
		if updates[i].DesiredState.Label == "consumer" {
			assert.Empty(t, string(updates[i].DesiredState.PatchDocument),
				"no consumer change should be planned for an omitted-but-not-removed property")
		}
	}
}

// A provider-computed output is never declared; a reference to it resolves
// the persisted value.
func TestGenerateResourceUpdates_ReadOnlyOutputResolvesFromPersisted(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Mutable", "Arn"},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::OutputProducer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties:         json.RawMessage(`{"Name": "p", "Mutable": "m1"}`),
				ReadOnlyProperties: json.RawMessage(`{"Arn": "arn:probe"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Arn", "$value": "arn:probe"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Reconcile changes an unrelated mutable field on the producer; Arn is
	// never declared (it is provider-computed) and the consumer is unchanged.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::OutputProducer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p", "Mutable": "m2"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Arn", "$value": "arn:probe"}}`,
					producerKsuid)),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)

	for i := range updates {
		if updates[i].DesiredState.Label == "consumer" {
			assert.NotContains(t, string(updates[i].DesiredState.PatchDocument), "Arn",
				"consumer must not be spuriously planned on the Arn path")
		}
	}
}

// A forward reference to a resource created in this command defers to
// execution-time resolution.
func TestGenerateResourceUpdates_ForwardReferenceDefers(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Arn"},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	// No persisted rows: both resources are brand new.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::NewProducer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p"}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "c",
					"Ref": {
						"$res":      true,
						"$label":    "producer",
						"$type":     "FakeAWS::Absence::NewProducer",
						"$stack":    "test-stack",
						"$property": "Arn"
					}
				}`),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)

	planned := map[string]*ResourceUpdate{}
	for i := range updates {
		planned[updates[i].DesiredState.Label] = &updates[i]
	}

	producer, ok := planned["producer"]
	require.True(t, ok, "producer must be planned")
	assert.Equal(t, OperationCreate, producer.Operation)

	consumer, ok := planned["consumer"]
	require.True(t, ok, "consumer must be planned")
	assert.Equal(t, OperationCreate, consumer.Operation)

	require.NotEmpty(t, consumer.RemainingResolvables, "the forward reference must defer to execution-time resolution")
	found := false
	for _, uri := range consumer.RemainingResolvables {
		if uri.PropertyPath() == "Arn" {
			found = true
		}
	}
	assert.True(t, found, "the producer's Arn URI must remain as a resolvable, expected among: %v", consumer.RemainingResolvables)
}

// Removing a property together with the resource that references it is not a
// dangling reference: both leave in the same command.
func TestGenerateResourceUpdates_RemovedPropertyWithDeletedConsumer_NoError(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Name": {CreateOnly: true},
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "p", "Tags": [{"Key": "team", "Value": "x"}]}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Tags", "$value": "[{\"Key\":\"team\",\"Value\":\"x\"}]"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Reconcile: the producer no longer declares Tags at all, and the
	// consumer is dropped from the forma entirely — it is being deleted in
	// this same command, not left dangling.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p"}`),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	_, err = GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)
}

// Removing one member of a collection does not dangle a reference to the
// collection itself.
func TestGenerateResourceUpdates_MemberRemovalDoesNotDangleCollectionReference(t *testing.T) {
	ds, _ := GetDeps(t)

	producerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Tags"},
		Hints: map[string]pkgmodel.FieldHint{
			"Name": {CreateOnly: true},
			"Tags": {UpdateMethod: pkgmodel.FieldUpdateMethodEntitySet, IndexField: "Key"},
		},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Ref"},
	}

	producerKsuid := util.NewID()
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema: producerSchema, Ksuid: producerKsuid,
				Properties: json.RawMessage(`{"Name": "p", "Tags": [{"Key": "team", "Value": "x"}, {"Key": "env", "Value": "prod"}]}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: consumerKsuid,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Tags", "$value": "[{\"Key\":\"team\",\"Value\":\"x\"},{\"Key\":\"env\",\"Value\":\"prod\"}]"}}`,
					producerKsuid)),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	// Reconcile: Tags stays declared, but only one of the two persisted
	// members survives — the other is dropped. The consumer references the
	// collection root, not the dropped member.
	forma := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{
			{
				Label: "producer", Type: "FakeAWS::Absence::Producer",
				Stack: "test-stack", Target: "test-target",
				Schema:     producerSchema,
				Properties: json.RawMessage(`{"Name": "p", "Tags": [{"Key": "team", "Value": "x"}]}`),
			},
			{
				Label: "consumer", Type: "FakeAWS::Absence::Consumer",
				Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(fmt.Sprintf(
					`{"Name": "c", "Ref": {"$ref": "formae://%s#/Tags", "$value": "[{\"Key\":\"team\",\"Value\":\"x\"},{\"Key\":\"env\",\"Value\":\"prod\"}]"}}`,
					producerKsuid)),
			},
		},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region": "us-east-1"}`), Namespace: "test"},
	}

	_, err = GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)
}
