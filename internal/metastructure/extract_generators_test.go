// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGeneratorDatastore answers the four queries ExtractGenerators makes and
// inherits the panicking stubs for everything else.
type mockGeneratorDatastore struct {
	mockExtractDatastore

	allStacks    []*pkgmodel.Stack
	byStack      map[string][]pkgmodel.Generator
	identities   map[string]datastore.GeneratorIdentity // keyed "<label>/<stack>"
	rotation     []datastore.GeneratorRotationInfo
	destinations map[string][]*pkgmodel.Resource // keyed by generator KSUID
}

func (m *mockGeneratorDatastore) ListAllStacks() ([]*pkgmodel.Stack, error) {
	return m.allStacks, nil
}

func (m *mockGeneratorDatastore) LoadGeneratorsByStack(stackLabel string) ([]pkgmodel.Generator, error) {
	return m.byStack[stackLabel], nil
}

func (m *mockGeneratorDatastore) GetGeneratorIdentity(label, stackLabel string) (datastore.GeneratorIdentity, error) {
	return m.identities[label+"/"+stackLabel], nil
}

func (m *mockGeneratorDatastore) GetGeneratorsWithRotation() ([]datastore.GeneratorRotationInfo, error) {
	return m.rotation, nil
}

func (m *mockGeneratorDatastore) FindResourcesReferencingGenerator(ksuid string) ([]*pkgmodel.Resource, error) {
	return m.destinations[ksuid], nil
}

func stack(label string) *pkgmodel.Stack {
	return &pkgmodel.Stack{Label: label, ID: "stackid-" + label}
}

// TestExtractGenerators_ProjectsCadenceLastRotationAndDestinations covers the
// whole projection for a rotating generator whose destinations sit in two
// different stacks.
func TestExtractGenerators_ProjectsCadenceLastRotationAndDestinations(t *testing.T) {
	lastRotated := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	ds := &mockGeneratorDatastore{
		allStacks: []*pkgmodel.Stack{stack("db"), stack("app")},
		byStack: map[string][]pkgmodel.Generator{
			"db": {&pkgmodel.PasswordGenerator{
				Label:    "db-password",
				Stack:    "db",
				Length:   32,
				Digits:   true,
				Rotation: &pkgmodel.RotationSpec{EverySeconds: 86400},
			}},
		},
		identities: map[string]datastore.GeneratorIdentity{
			"db-password/db": {ID: "genksuid1", GenerationID: "generation-7"},
		},
		rotation: []datastore.GeneratorRotationInfo{
			{GeneratorID: "genksuid1", Label: "db-password", StackLabel: "db",
				IntervalSeconds: 86400, LastRotationAt: lastRotated},
		},
		destinations: map[string][]*pkgmodel.Resource{
			"genksuid1": {
				{Label: "primary", Stack: "db", Type: "AWS::RDS::DBInstance"},
				{Label: "worker", Stack: "app", Type: "AWS::ECS::TaskDefinition"},
			},
		},
	}
	m := &Metastructure{Datastore: ds}

	items, err := m.ExtractGenerators()
	require.NoError(t, err)
	require.Len(t, items, 1)

	got := items[0]
	assert.Equal(t, "db-password", got.Label)
	assert.Equal(t, "password", got.Type)
	assert.Equal(t, "db", got.Stack)
	assert.Equal(t, 86400, got.EverySeconds)
	assert.Equal(t, lastRotated, got.LastRotatedAt)
	assert.Equal(t, "generation-7", got.GenerationID)

	// Destinations are ordered by stack then label, so the count and the list
	// read the same on every fetch.
	require.Len(t, got.Destinations, 2)
	assert.Equal(t, "worker", got.Destinations[0].ResourceLabel)
	assert.Equal(t, "app", got.Destinations[0].StackLabel)
	assert.Equal(t, "primary", got.Destinations[1].ResourceLabel)
	assert.Equal(t, "db", got.Destinations[1].StackLabel)

	// Config is the declared spec, so the cadence and the length are readable
	// from it and the generator's own KSUID is not part of it.
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(got.Config, &cfg))
	assert.Equal(t, float64(32), cfg["Length"])
	assert.Equal(t, "password", cfg["Type"])
}

// TestExtractGenerators_NeverRotatedLeavesTheInstantZero covers a generator
// that declares a cadence but has never had a draw commit.
func TestExtractGenerators_NeverRotatedLeavesTheInstantZero(t *testing.T) {
	ds := &mockGeneratorDatastore{
		allStacks: []*pkgmodel.Stack{stack("db")},
		byStack: map[string][]pkgmodel.Generator{
			"db": {&pkgmodel.PasswordGenerator{
				Label:    "fresh",
				Stack:    "db",
				Length:   16,
				Rotation: &pkgmodel.RotationSpec{EverySeconds: 3600},
			}},
		},
		identities: map[string]datastore.GeneratorIdentity{
			"fresh/db": {ID: "genksuid2"},
		},
		rotation: []datastore.GeneratorRotationInfo{
			{GeneratorID: "genksuid2", Label: "fresh", StackLabel: "db", IntervalSeconds: 3600},
		},
	}
	m := &Metastructure{Datastore: ds}

	items, err := m.ExtractGenerators()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 3600, items[0].EverySeconds)
	assert.True(t, items[0].LastRotatedAt.IsZero())
	assert.Empty(t, items[0].GenerationID)
	assert.Empty(t, items[0].Destinations)
}

// TestExtractGenerators_NoCadenceCarriesNoInterval covers a generator that
// declares no rotation: nothing advances it, so it reports no interval.
func TestExtractGenerators_NoCadenceCarriesNoInterval(t *testing.T) {
	ds := &mockGeneratorDatastore{
		allStacks: []*pkgmodel.Stack{stack("db")},
		byStack: map[string][]pkgmodel.Generator{
			"db": {&pkgmodel.PasswordGenerator{Label: "static", Stack: "db", Length: 8}},
		},
		identities: map[string]datastore.GeneratorIdentity{
			"static/db": {ID: "genksuid3", GenerationID: "generation-1"},
		},
	}
	m := &Metastructure{Datastore: ds}

	items, err := m.ExtractGenerators()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Zero(t, items[0].EverySeconds)
	assert.True(t, items[0].LastRotatedAt.IsZero())
	assert.Equal(t, "generation-1", items[0].GenerationID)
}

// TestExtractGenerators_WithdrawsADestinationsDrawnValue covers that the
// projection never carries a destination's properties. A bound destination
// holds the generator's value under a $gen envelope; only its label and stack
// reach the inventory.
func TestExtractGenerators_WithdrawsADestinationsDrawnValue(t *testing.T) {
	const drawn = "hunter2-the-drawn-value"
	ds := &mockGeneratorDatastore{
		allStacks: []*pkgmodel.Stack{stack("db")},
		byStack: map[string][]pkgmodel.Generator{
			"db": {&pkgmodel.PasswordGenerator{Label: "db-password", Stack: "db", Length: 32}},
		},
		identities: map[string]datastore.GeneratorIdentity{
			"db-password/db": {ID: "genksuid4", GenerationID: "generation-9"},
		},
		destinations: map[string][]*pkgmodel.Resource{
			"genksuid4": {{
				Label: "primary",
				Stack: "db",
				Type:  "AWS::RDS::DBInstance",
				Properties: json.RawMessage(`{"MasterPassword":{"$gen":true,` +
					`"$generator":"genksuid4","$output":"value",` +
					`"$visibility":"Opaque","$value":"` + drawn + `"}}`),
			}},
		},
	}
	m := &Metastructure{Datastore: ds}

	items, err := m.ExtractGenerators()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Len(t, items[0].Destinations, 1)

	// Marshalling the item renders every json.RawMessage it holds as the JSON
	// it carries, so the assertion reads the text rather than a byte array.
	encoded, err := json.Marshal(items[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), drawn)

	// The destination carries nothing but its label and stack, which is what
	// keeps its properties — and the envelope in them — out of the projection.
	assert.Equal(t, "primary", items[0].Destinations[0].ResourceLabel)
	assert.Equal(t, "db", items[0].Destinations[0].StackLabel)
}

// TestExtractGenerators_Empty covers a datastore with no generators at all.
func TestExtractGenerators_Empty(t *testing.T) {
	ds := &mockGeneratorDatastore{allStacks: []*pkgmodel.Stack{stack("db")}}
	m := &Metastructure{Datastore: ds}

	items, err := m.ExtractGenerators()
	require.NoError(t, err)
	assert.Empty(t, items)
}
