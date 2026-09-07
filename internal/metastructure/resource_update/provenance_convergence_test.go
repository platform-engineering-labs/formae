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

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func provenanceSecretSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "SecretString"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}, "SecretString": {Opaque: true}},
	}
}

func provenanceConsumerSchema() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Settings"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
}

// provenanceFixture seeds a stored secret source and a consumer whose NESTED
// field carries a resolved opaque reference envelope with provenance, then
// returns the ksuids.
func provenanceFixture(t *testing.T, ds *mockDatastore, storedSecret string, withProvenance bool) (string, string) {
	t.Helper()
	sourceKsuid := util.NewID()
	consumerKsuid := util.NewID()
	storedDigest := pkgmodel.ComputeValueHash(storedSecret)

	envelope := `{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + storedDigest + `","$hashed":true,"$visibility":"Opaque","$strategy":"Update"`
	if withProvenance {
		envelope += `,"$resolvedFrom":"` + provenance.FromStored(storedDigest) + `"`
	}
	envelope += `}`

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(), Ksuid: consumerKsuid,
				Properties: json.RawMessage(`{"Name":"contact","Settings":{"url":` + envelope + `}}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)
	return sourceKsuid, consumerKsuid
}

func provenanceForma(declaredSecret string) *pkgmodel.Forma {
	sourceProps := `{"Name":"secret"}`
	if declaredSecret != "" {
		sourceProps = fmt.Sprintf(`{"Name":"secret","SecretString":%q}`, declaredSecret)
	}
	return &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema:     provenanceSecretSchema(),
				Properties: json.RawMessage(sourceProps),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(),
				Properties: json.RawMessage(`{
					"Name": "contact",
					"Settings": {"url": {"$res": true, "$label": "secret", "$type": "Test::Secret", "$stack": "test-stack", "$property": "SecretString"}}
				}`),
			},
		},
	}
}

func generateProvenance(t *testing.T, ds *mockDatastore, forma *pkgmodel.Forma, force bool) map[string]*ResourceUpdate {
	t.Helper()
	existingTargets := []*pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}}
	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, FormaCommandSourceUser, existingTargets, ds, nil, nil, force)
	require.NoError(t, err)
	planned := map[string]*ResourceUpdate{}
	for i := range updates {
		label := updates[i].DesiredState.Label
		if label == "" {
			label = updates[i].PriorState.Label
		}
		planned[label] = &updates[i]
	}
	return planned
}

// The perpetual-churn defect: an unchanged secret-sourced consumer field must
// plan NOTHING, nested destinations included.
func TestGenerateResourceUpdates_UnchangedSecretConsumer_PlansNothing(t *testing.T) {
	ds, _ := GetDeps(t)
	provenanceFixture(t, ds, "hunter2", true)

	planned := generateProvenance(t, ds, provenanceForma(""), false)
	_, consumerPlanned := planned["contact"]
	assert.False(t, consumerPlanned,
		"an unchanged secret-sourced consumer must not churn")
	_, sourcePlanned := planned["secret"]
	assert.False(t, sourcePlanned, "the unchanged source must not plan either")
}

// A rotation declared in the same command plans the consumer, defers the
// value to execution, and persists no plaintext.
func TestGenerateResourceUpdates_RotatedSecret_PlansConsumerWithoutPlaintext(t *testing.T) {
	ds, _ := GetDeps(t)
	provenanceFixture(t, ds, "hunter2", true)

	planned := generateProvenance(t, ds, provenanceForma("rotated-secret"), false)
	consumer, ok := planned["contact"]
	require.True(t, ok, "a rotated source must plan its consumer")
	assert.NotEmpty(t, consumer.RemainingResolvables, "the value resolves live at execution")
	assert.NotContains(t, string(consumer.DesiredState.PatchDocument), "rotated-secret")
	assert.NotContains(t, string(consumer.DesiredState.Properties), "rotated-secret")
}

// A consumer without provenance converges exactly once.
func TestGenerateResourceUpdates_MissingProvenance_ConvergesOnce(t *testing.T) {
	ds, _ := GetDeps(t)
	provenanceFixture(t, ds, "hunter2", false)

	planned := generateProvenance(t, ds, provenanceForma(""), false)
	consumer, ok := planned["contact"]
	require.True(t, ok, "unknown provenance must converge once")
	assert.NotEmpty(t, consumer.RemainingResolvables)
}

// Force bypasses the suppression: the ratified re-assert path.
func TestGenerateResourceUpdates_ForceBypassesSuppression(t *testing.T) {
	ds, _ := GetDeps(t)
	provenanceFixture(t, ds, "hunter2", true)

	planned := generateProvenance(t, ds, provenanceForma(""), true)
	_, consumerPlanned := planned["contact"]
	assert.True(t, consumerPlanned, "force re-asserts the declared value")
}

// A legacy (bare-hex, unversioned) provenance value is unknown, never
// comparable: the consumer converges once.
func TestGenerateResourceUpdates_LegacyProvenance_ConvergesOnce(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	consumerKsuid := util.NewID()
	storedDigest := pkgmodel.ComputeValueHash("hunter2")

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(), Ksuid: consumerKsuid,
				Properties: json.RawMessage(`{"Name":"contact","Settings":{"url":{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + storedDigest + `","$hashed":true,"$visibility":"Opaque","$resolvedFrom":"` + storedDigest + `"}}}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := provenanceForma("")
	planned := generateProvenance(t, ds, forma, false)
	_, consumerPlanned := planned["contact"]
	assert.True(t, consumerPlanned, "legacy provenance must converge once, not suppress")
}

// A createOnly destination whose occurrence has a written value but unknown
// movement must never be replaced: replacement destroys the resource over a
// question the classifier cannot answer.
func TestGenerateResourceUpdates_CreateOnlyDestination_UnknownNeverReplaces(t *testing.T) {
	createOnlyConsumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Token"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}, "Token": {CreateOnly: true}},
	}
	consumerForma := func(sourceKsuid string) *pkgmodel.Forma {
		return &pkgmodel.Forma{
			Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
			Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
			Resources: []pkgmodel.Resource{
				{
					Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
					Schema:     provenanceSecretSchema(),
					Properties: json.RawMessage(`{"Name":"secret"}`),
				},
				{
					Label: "consumer", Type: "Test::CreateOnlyConsumer", Stack: "test-stack", Target: "test-target",
					Schema: createOnlyConsumerSchema,
					Properties: json.RawMessage(`{
						"Name": "consumer",
						"Token": {"$res": true, "$label": "secret", "$type": "Test::Secret", "$stack": "test-stack", "$property": "SecretString"}
					}`),
				},
			},
		}
	}

	{
		ds, _ := GetDeps(t)
		sourceKsuid := util.NewID()
		storedDigest := pkgmodel.ComputeValueHash("hunter2")
		existingStack := &pkgmodel.Forma{
			Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
			Resources: []pkgmodel.Resource{
				{
					Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
					Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
					Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
				},
				{
					Label: "consumer", Type: "Test::CreateOnlyConsumer", Stack: "test-stack", Target: "test-target",
					Schema: createOnlyConsumerSchema, Ksuid: util.NewID(),
					Properties: json.RawMessage(`{"Name":"consumer","Token":{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + storedDigest + `","$hashed":true,"$visibility":"Opaque"}}`),
				},
			},
		}
		_, err := ds.StoreStack(existingStack, "previous-command")
		require.NoError(t, err)

		planned := generateProvenance(t, ds, consumerForma(sourceKsuid), false)
		for label, u := range planned {
			assert.NotEqual(t, OperationDelete, u.Operation,
				"unknown provenance on a createOnly destination must never replace (got delete for %q)", label)
		}
	}
}

// opsIn decodes a patch document into op-by-path form.
func opsIn(t *testing.T, patchDoc json.RawMessage) map[string]any {
	t.Helper()
	var ops []struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	if len(patchDoc) > 0 {
		require.NoError(t, json.Unmarshal(patchDoc, &ops))
	}
	byPath := map[string]any{}
	for _, op := range ops {
		byPath[op.Path] = op.Value
	}
	return byPath
}

// A sibling edit next to a stable secret-sourced field plans only the sibling
// op: the secret occurrence stays out of the patch, while the reference stays
// listed as a resolvable so execution still writes it from the live source if
// the patch regenerates.
func TestGenerateResourceUpdates_StableSecretConsumer_SiblingEditPlansOnlySibling(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	storedDigest := pkgmodel.ComputeValueHash("hunter2")
	envelope := `{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + storedDigest + `","$hashed":true,"$visibility":"Opaque","$resolvedFrom":"` + provenance.FromStored(storedDigest) + `"}`

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(), Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"contact","Settings":{"recipient":"#infra-notification","url":` + envelope + `}}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema:     provenanceSecretSchema(),
				Properties: json.RawMessage(`{"Name":"secret"}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(),
				Properties: json.RawMessage(`{
					"Name": "contact",
					"Settings": {
						"recipient": "#platform-alerts",
						"url": {"$res": true, "$label": "secret", "$type": "Test::Secret", "$stack": "test-stack", "$property": "SecretString"}
					}
				}`),
			},
		},
	}

	planned := generateProvenance(t, ds, forma, false)
	consumer, ok := planned["contact"]
	require.True(t, ok, "the sibling change must plan the consumer")
	byPath := opsIn(t, consumer.DesiredState.PatchDocument)
	assert.Contains(t, byPath, "/Settings/recipient", "the sibling change must produce an op")
	assert.NotContains(t, byPath, "/Settings/url",
		"a stable secret occurrence must not enter the patch alongside a sibling edit")
	assert.Contains(t, consumer.RemainingResolvables, pkgmodel.FormaeURI("formae://"+sourceKsuid+"#/SecretString"),
		"the reference must still resolve at execution time")
}

// A reference repointed at a DIFFERENT source is a real change and plans,
// digests notwithstanding: suppressing every unresolved reference would
// silently stop honouring a repoint.
func TestGenerateResourceUpdates_RepointedSecretRef_PlansConsumer(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	otherKsuid := util.NewID()
	storedDigest := pkgmodel.ComputeValueHash("hunter2")
	envelope := `{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + storedDigest + `","$hashed":true,"$visibility":"Opaque","$resolvedFrom":"` + provenance.FromStored(storedDigest) + `"}`

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "secret-b", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: otherKsuid,
				Properties: json.RawMessage(`{"Name":"secret-b","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(), Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"contact","Settings":{"url":` + envelope + `}}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema:     provenanceSecretSchema(),
				Properties: json.RawMessage(`{"Name":"secret"}`),
			},
			{
				Label: "secret-b", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema:     provenanceSecretSchema(),
				Properties: json.RawMessage(`{"Name":"secret-b"}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(),
				Properties: json.RawMessage(`{
					"Name": "contact",
					"Settings": {"url": {"$res": true, "$label": "secret-b", "$type": "Test::Secret", "$stack": "test-stack", "$property": "SecretString"}}
				}`),
			},
		},
	}

	planned := generateProvenance(t, ds, forma, false)
	consumer, ok := planned["contact"]
	require.True(t, ok, "a reference naming a new source must plan")
	byPath := opsIn(t, consumer.DesiredState.PatchDocument)
	assert.Contains(t, byPath, "/Settings/url", "the repointed reference must produce an op")
}

// The stability decision is a property of the occurrence and its provenance,
// not of nesting depth: an unchanged top-level secret-sourced field plans
// nothing, exactly like the nested case.
func TestGenerateResourceUpdates_UnchangedTopLevelSecretConsumer_PlansNothing(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	storedDigest := pkgmodel.ComputeValueHash("R0ABCDEF1234567890")
	topLevelSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "IntegrationKey"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	envelope := `{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + storedDigest + `","$hashed":true,"$visibility":"Opaque","$resolvedFrom":"` + provenance.FromStored(storedDigest) + `"}`

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "integration", Type: "Test::Integration", Stack: "test-stack", Target: "test-target",
				Schema: topLevelSchema, Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"integration","IntegrationKey":` + envelope + `}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema:     provenanceSecretSchema(),
				Properties: json.RawMessage(`{"Name":"secret"}`),
			},
			{
				Label: "integration", Type: "Test::Integration", Stack: "test-stack", Target: "test-target",
				Schema: topLevelSchema,
				Properties: json.RawMessage(`{
					"Name": "integration",
					"IntegrationKey": {"$res": true, "$label": "secret", "$type": "Test::Secret", "$stack": "test-stack", "$property": "SecretString"}
				}`),
			},
		},
	}

	planned := generateProvenance(t, ds, forma, false)
	_, consumerPlanned := planned["integration"]
	assert.False(t, consumerPlanned, "an unchanged top-level secret-sourced field must not churn")
}

// A provider-side rotation absorbed by sync refreshes the source's stored
// digest; the consumer's provenance now disagrees with it, so the next
// reconcile plans the consumer. This is the second detectable rotation path
// (the first is a rotation declared in-command).
func TestGenerateResourceUpdates_SyncAbsorbedRotation_PlansConsumer(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	writtenDigest := pkgmodel.ComputeValueHash("hunter2")
	absorbedDigest := pkgmodel.ComputeValueHash("rotated-behind-formae")
	envelope := `{"$ref":"formae://` + sourceKsuid + `#/SecretString","$value":"` + writtenDigest + `","$hashed":true,"$visibility":"Opaque","$resolvedFrom":"` + provenance.FromStored(writtenDigest) + `"}`

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + absorbedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "contact", Type: "Test::Contact", Stack: "test-stack", Target: "test-target",
				Schema: provenanceConsumerSchema(), Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"contact","Settings":{"url":` + envelope + `}}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	planned := generateProvenance(t, ds, provenanceForma(""), false)
	consumer, ok := planned["contact"]
	require.True(t, ok, "a sync-absorbed rotation must plan the consumer on the next reconcile")
	assert.NotEmpty(t, consumer.RemainingResolvables, "the rotated value resolves live at execution")
}

// A READABLE reference is outside this classifier's scope: formae holds a
// comparable value on both sides, so the ordinary value comparison decides,
// and a changed source value still plans an op carrying the new value.
func TestGenerateResourceUpdates_ReadableRefValueChanged_PlansConsumer(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	readableSourceSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Endpoint"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Url"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "endpoint", Type: "Test::Endpoint", Stack: "test-stack", Target: "test-target",
				Schema: readableSourceSchema, Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"endpoint","Endpoint":"new-endpoint"}`),
			},
			{
				Label: "client", Type: "Test::Client", Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"Name":"client","Url":{"$ref":"formae://` + sourceKsuid + `#/Endpoint","$value":"old-endpoint"}}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "endpoint", Type: "Test::Endpoint", Stack: "test-stack", Target: "test-target",
				Schema:     readableSourceSchema,
				Properties: json.RawMessage(`{"Name":"endpoint","Endpoint":"new-endpoint"}`),
			},
			{
				Label: "client", Type: "Test::Client", Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"Name": "client",
					"Url": {"$res": true, "$label": "endpoint", "$type": "Test::Endpoint", "$stack": "test-stack", "$property": "Endpoint"}
				}`),
			},
		},
	}

	planned := generateProvenance(t, ds, forma, false)
	consumer, ok := planned["client"]
	require.True(t, ok, "a changed readable reference value must plan")
	byPath := opsIn(t, consumer.DesiredState.PatchDocument)
	require.Contains(t, byPath, "/Url")
	assert.Equal(t, "new-endpoint", byPath["/Url"])
}

// A rotation must plan the consumer for a TOP-LEVEL destination exactly as it
// does for a nested one. A top-level unresolved reference flattens to an empty
// string, which the top-level empty-value filter would otherwise treat as PKL
// rendering noise and silently drop; the converge classification must carry
// the occurrence past that filter.
func TestGenerateResourceUpdates_RotatedSecret_TopLevelDestination_PlansConsumer(t *testing.T) {
	ds, _ := GetDeps(t)
	sourceKsuid := util.NewID()
	storedDigest := pkgmodel.ComputeValueHash("hunter2")
	consumerSchema := pkgmodel.Schema{
		Identifier: "BucketName",
		Fields:     []string{"BucketName", "AccessControl", "DbPassword"},
	}
	envelope := `{"$hashed":true,"$ref":"formae://` + sourceKsuid + `#/SecretString","$resolvedFrom":"` + provenance.FromStored(storedDigest) + `","$strategy":"Update","$value":"` + storedDigest + `","$visibility":"Opaque"}`

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema: provenanceSecretSchema(), Ksuid: sourceKsuid,
				Properties: json.RawMessage(`{"Name":"secret","SecretString":{"$value":"` + storedDigest + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
			},
			{
				Label: "bucket", Type: "Test::Bucket", Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema, Ksuid: util.NewID(),
				Properties: json.RawMessage(`{"AccessControl":"Private","BucketName":"bucket","DbPassword":` + envelope + `}`),
			},
		},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:  []pkgmodel.Stack{{Label: "test-stack"}},
		Targets: []pkgmodel.Target{{Label: "test-target", Namespace: "test", Config: json.RawMessage(`{}`)}},
		Resources: []pkgmodel.Resource{
			{
				Label: "secret", Type: "Test::Secret", Stack: "test-stack", Target: "test-target",
				Schema:     provenanceSecretSchema(),
				Properties: json.RawMessage(`{"Name":"secret","SecretString":"rotated-secret"}`),
			},
			{
				Label: "bucket", Type: "Test::Bucket", Stack: "test-stack", Target: "test-target",
				Schema: consumerSchema,
				Properties: json.RawMessage(`{
					"BucketName": "bucket",
					"AccessControl": "Private",
					"DbPassword": {"$res": true, "$label": "secret", "$type": "Test::Secret", "$stack": "test-stack", "$property": "SecretString"}
				}`),
			},
		},
	}

	planned := generateProvenance(t, ds, forma, false)
	consumer, ok := planned["bucket"]
	require.True(t, ok, "a rotation must plan a top-level consumer")
	byPath := opsIn(t, consumer.DesiredState.PatchDocument)
	assert.Contains(t, byPath, "/DbPassword", "the rotated occurrence must produce an op")
	assert.NotContains(t, string(consumer.DesiredState.PatchDocument), "rotated-secret")
	assert.NotEmpty(t, consumer.RemainingResolvables, "the value resolves live at execution")
}
