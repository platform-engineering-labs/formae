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
