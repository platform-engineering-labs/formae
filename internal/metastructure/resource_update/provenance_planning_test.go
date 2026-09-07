// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const boundGeneratorKsuid = "2genabcdefghijklmnopqrstuvw"

// desiredGenProperties is the consumer's desired document: one secret
// property bound to the generator's "value" output, in the translated shape
// command translation leaves behind.
var desiredGenProperties = json.RawMessage(
	`{"Password":{"$gen":true,"$generator":"` + boundGeneratorKsuid + `","$output":"value","$visibility":"Opaque"}}`)

// storedGenProperties is the same binding as it sits at rest after a value
// has been drawn: the value hashed, and the generation it came from recorded
// as the occurrence's provenance.
func storedGenProperties(resolvedFrom string) json.RawMessage {
	return json.RawMessage(`{"Password":{"$gen":true,"$generator":"` + boundGeneratorKsuid +
		`","$output":"value","$visibility":"Opaque","$hashed":true,"$value":"` +
		strings.Repeat("a", 64) + `","$resolvedFrom":"` + resolvedFrom + `"}}`)
}

func genSpec(mutate func(*pkgmodel.PasswordGenerator)) *pkgmodel.PasswordGenerator {
	g := &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "default",
		Length: 32, Uppercase: true, Lowercase: true, Digits: true,
		RequireEachIncludedType: true,
	}
	mutate(g)
	return g
}

func heldGeneration(t *testing.T, generationID string, drawnUnder pkgmodel.Generator) pkgmodel.GeneratorIdentity {
	t.Helper()
	spec, err := json.Marshal(drawnUnder)
	require.NoError(t, err)
	return pkgmodel.GeneratorIdentity{ID: boundGeneratorKsuid, GenerationID: generationID, GenerationSpec: spec}
}

// classifyGenOccurrence runs the full seam for one generator-bound secret:
// the resolver's answer for the generator output, then the per-occurrence
// provenance classification that decides whether the occurrence is planned.
func classifyGenOccurrence(
	t *testing.T,
	stored json.RawMessage,
	identity pkgmodel.GeneratorIdentity,
	desiredSpec pkgmodel.Generator,
) (OccurrenceRecord, resolver.ResolvableProperties) {
	t.Helper()
	return classifyGenOccurrenceAt(t, stored, identity, desiredSpec, pkgmodel.Schema{})
}

// createOnlySchema puts the bound secret on a destination formae can never
// update in place.
var createOnlySchema = pkgmodel.Schema{
	Identifier: "Name",
	Fields:     []string{"Name", "Password"},
	Hints:      map[string]pkgmodel.FieldHint{"Password": {CreateOnly: true}},
}

func classifyGenOccurrenceAt(
	t *testing.T,
	stored json.RawMessage,
	identity pkgmodel.GeneratorIdentity,
	desiredSpec pkgmodel.Generator,
	schema pkgmodel.Schema,
) (OccurrenceRecord, resolver.ResolvableProperties) {
	t.Helper()
	consumer := pkgmodel.Resource{
		Label: "consumer", Type: "Test::Consumer", Stack: "default",
		Properties: desiredGenProperties,
	}
	answers, err := resolver.LoadResolvablePropertiesFromStacks(consumer, nil, nil,
		func(string) (pkgmodel.GeneratorIdentity, pkgmodel.Generator) { return identity, desiredSpec })
	require.NoError(t, err)

	records := buildProvenanceRecords(desiredGenProperties, stored, answers, schema, false)
	require.Len(t, records, 1)
	return records[0], answers
}

// An unmoved generation makes the occurrence stable, so an ordinary re-apply
// of a generator-bound secret plans nothing for it.
func TestBuildProvenanceRecords_UnmovedGenerationIsStable(t *testing.T) {
	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	rec, answers := classifyGenOccurrence(t,
		storedGenProperties(provenance.DigestOfString("generation-1")),
		heldGeneration(t, "generation-1", spec), spec)

	assert.Equal(t, OccurrenceStable, rec.Class)
	assert.True(t, answers.StableSuppressedAt("Password"))
}

// A moved generation plans the occurrence.
func TestBuildProvenanceRecords_MovedGenerationIsPlanned(t *testing.T) {
	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	rec, answers := classifyGenOccurrence(t,
		storedGenProperties(provenance.DigestOfString("generation-0")),
		heldGeneration(t, "generation-1", spec), spec)

	assert.NotEqual(t, OccurrenceStable, rec.Class)
	assert.False(t, answers.StableSuppressedAt("Password"))
	assert.True(t, answers.ConvergeMarkedAt("Password"))
}

// A spec edit the current generation cannot satisfy plans the occurrence even
// though the generation identity itself has not moved.
func TestBuildProvenanceRecords_UnsatisfiedSpecPlansDespiteUnmovedGeneration(t *testing.T) {
	drawn := genSpec(func(*pkgmodel.PasswordGenerator) {})
	desired := genSpec(func(g *pkgmodel.PasswordGenerator) { g.Length = 64 })
	rec, answers := classifyGenOccurrence(t,
		storedGenProperties(provenance.DigestOfString("generation-1")),
		heldGeneration(t, "generation-1", drawn), desired)

	assert.Equal(t, OccurrenceConvergeUnknown, rec.Class)
	assert.False(t, answers.StableSuppressedAt("Password"))
	assert.True(t, answers.ConvergeMarkedAt("Password"))
}

// A spec edit the current generation still satisfies leaves it stable.
func TestBuildProvenanceRecords_SatisfiedSpecEditStaysStable(t *testing.T) {
	drawn := genSpec(func(g *pkgmodel.PasswordGenerator) { g.Symbols = false; g.RequireEachIncludedType = false })
	desired := genSpec(func(g *pkgmodel.PasswordGenerator) { g.Symbols = true; g.RequireEachIncludedType = false })
	rec, answers := classifyGenOccurrence(t,
		storedGenProperties(provenance.DigestOfString("generation-1")),
		heldGeneration(t, "generation-1", drawn), desired)

	assert.Equal(t, OccurrenceStable, rec.Class)
	assert.True(t, answers.StableSuppressedAt("Password"))
}

// A generator holding no generation yet must be planned, so a first apply
// materializes a value.
func TestBuildProvenanceRecords_NoGenerationYetIsPlanned(t *testing.T) {
	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	rec, answers := classifyGenOccurrence(t,
		storedGenProperties(provenance.DigestOfString("generation-1")),
		pkgmodel.GeneratorIdentity{ID: boundGeneratorKsuid}, spec)

	assert.Equal(t, OccurrenceConvergeUnknown, rec.Class)
	assert.False(t, answers.StableSuppressedAt("Password"))
}

// A binding that has never been written falls to normal diff semantics
// rather than the unknown-movement rule, so the create path is untouched.
func TestBuildProvenanceRecords_FirstDeclarationOfAGenBindingIsPlanned(t *testing.T) {
	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	rec, answers := classifyGenOccurrence(t, nil,
		heldGeneration(t, "generation-1", spec), spec)

	assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	assert.False(t, rec.HasStoredWritten)
	assert.False(t, answers.StableSuppressedAt("Password"))
}

// The reconcile path resolves the generation a $gen occurrence is bound to
// and classifies the occurrence against it, so an unmoved generation reaches
// the plan as stable.
func TestGenerateResourceUpdates_GenOccurrenceIsClassifiedAgainstItsGeneration(t *testing.T) {
	ds, _ := GetDeps(t)

	spec := genSpec(func(g *pkgmodel.PasswordGenerator) { g.Stack = "test-stack" })
	require.NoError(t, ds.StoreGeneratorGeneration("db-password", "test-stack", boundGeneratorKsuid, "generation-1", spec))

	consumerSchema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Password"},
		Hints:      map[string]pkgmodel.FieldHint{"Password": {Opaque: true}},
	}
	consumerKsuid := util.NewID()

	existingStack := &pkgmodel.Forma{
		Stacks: []pkgmodel.Stack{{Label: "test-stack"}},
		Resources: []pkgmodel.Resource{{
			Label: "consumer", Type: "FakeAWS::Generated::Consumer",
			Stack: "test-stack", Target: "test-target",
			Schema: consumerSchema, Ksuid: consumerKsuid,
			Properties: json.RawMessage(`{"Name":"c","Password":{"$gen":true,"$generator":"` + boundGeneratorKsuid +
				`","$output":"value","$visibility":"Opaque","$hashed":true,"$value":"` + strings.Repeat("a", 64) +
				`","$resolvedFrom":"` + provenance.DigestOfString("generation-1") + `"}}`),
		}},
	}
	_, err := ds.StoreStack(existingStack, "previous-command")
	require.NoError(t, err)

	declared, err := json.Marshal(spec)
	require.NoError(t, err)

	forma := &pkgmodel.Forma{
		Stacks:     []pkgmodel.Stack{{Label: "test-stack"}},
		Generators: []json.RawMessage{declared},
		Targets: []pkgmodel.Target{
			{Label: "test-target", Config: json.RawMessage(`{"Region":"us-east-1"}`), Namespace: "test"},
		},
		Resources: []pkgmodel.Resource{{
			Label: "consumer", Type: "FakeAWS::Generated::Consumer",
			Stack: "test-stack", Target: "test-target",
			Schema: consumerSchema,
			Properties: json.RawMessage(
				`{"Name":"c2","Password":{"$gen":true,"$label":"db-password","$stack":"test-stack","$output":"value"}}`),
		}},
	}
	existingTargets := []*pkgmodel.Target{
		{Label: "test-target", Config: json.RawMessage(`{"Region":"us-east-1"}`), Namespace: "test"},
	}

	updates, err := GenerateResourceUpdates(forma, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile,
		FormaCommandSourceUser, existingTargets, ds, nil, nil, false)
	require.NoError(t, err)

	var records []OccurrenceRecord
	for _, update := range updates {
		if update.DesiredState.Label == "consumer" {
			records = append(records, update.ProvenanceRecords...)
		}
	}
	require.Len(t, records, 1)
	assert.Equal(t, "Password", records[0].DestinationPath)
	assert.Equal(t, OccurrenceKindGenerator, records[0].DesiredIdentity.Kind)
	assert.Equal(t, OccurrenceStable, records[0].Class)
}

// An unmoved generation is stable at a createOnly destination too: nothing
// moved, so nothing is replaced.
func TestBuildProvenanceRecords_CreateOnlyUnmovedGenerationIsStable(t *testing.T) {
	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	rec, answers := classifyGenOccurrenceAt(t,
		storedGenProperties(provenance.DigestOfString("generation-1")),
		heldGeneration(t, "generation-1", spec), spec, createOnlySchema)

	assert.Equal(t, OccurrenceStable, rec.Class)
	assert.True(t, answers.StableSuppressedAt("Password"))
}

// A moved generation plans at a createOnly destination, where planning means
// the resource is replaced rather than updated in place.
func TestBuildProvenanceRecords_CreateOnlyMovedGenerationIsPlanned(t *testing.T) {
	spec := genSpec(func(*pkgmodel.PasswordGenerator) {})
	rec, answers := classifyGenOccurrenceAt(t,
		storedGenProperties(provenance.DigestOfString("generation-0")),
		heldGeneration(t, "generation-1", spec), spec, createOnlySchema)

	assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	assert.False(t, answers.StableSuppressedAt("Password"))
}

// A spec edit the held generation can no longer satisfy is SUPPRESSED at a
// createOnly destination, unlike at a mutable one. The redraw is reported
// through the same empty digest that means "movement unknown", and the
// unknown-movement rule is ruled never to replace a createOnly destination.
// The conflation is accepted: a generator-bound secret on a createOnly
// destination is rejected at the admission preflight in a later slice, and
// suppressing an unchanged resource is the safe direction in the interim.
// See generationRootDigest.
func TestBuildProvenanceRecords_CreateOnlyUnsatisfiedSpecIsSuppressedByTheUnknownRule(t *testing.T) {
	drawn := genSpec(func(*pkgmodel.PasswordGenerator) {})
	desired := genSpec(func(g *pkgmodel.PasswordGenerator) { g.Length = 64 })
	rec, answers := classifyGenOccurrenceAt(t,
		storedGenProperties(provenance.DigestOfString("generation-1")),
		heldGeneration(t, "generation-1", drawn), desired, createOnlySchema)

	assert.Equal(t, OccurrenceStable, rec.Class)
	assert.True(t, answers.StableSuppressedAt("Password"))
}
