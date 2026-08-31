// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A drawing generator, with the KSUID its $gen envelopes name.
func setOnceTestDraw() []generator_update.GeneratorUpdate {
	generator := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "gen-stack"}
	generator.SetID("gen1")
	return []generator_update.GeneratorUpdate{{Generator: generator, StackLabel: "gen-stack"}}
}

func setOnceTestResource(ksuid, stack, label, properties string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Ksuid: ksuid, Stack: stack, Label: label,
		Type:       "FakeAWS::SecretsManager::Secret",
		Properties: json.RawMessage(properties),
	}
}

// An update whose desired and stored documents differ, which is the ordinary
// shape of an apply over a resource that already exists.
func setOnceTestUpdate(desired, prior pkgmodel.Resource) resource_update.ResourceUpdate {
	return resource_update.ResourceUpdate{
		Operation:    resource_update.OperationUpdate,
		DesiredState: desired,
		PriorState:   prior,
	}
}

const (
	// The destination shape translation produces: a $gen envelope naming the
	// generator by KSUID.
	genBinding = `{"$gen":true,"$generator":"gen1","$output":"value","$visibility":"Opaque"}`
	// A consumer's envelope after it has resolved once, carrying the SetOnce
	// strategy it inherited from the value it read.
	setOnceRefToSeed = `{"$ref":"formae://res1#/SecretString","$visibility":"Opaque","$strategy":"SetOnce","$value":"digest"}`
	// The same consumer as the applied forma declares it, before resolution
	// puts a strategy or a value on it.
	refToSeed = `{"$ref":"formae://res1#/SecretString","$visibility":"Opaque"}`
)

// A lookup over a fixed live graph, which also records what it was asked, so a
// test can pin that a scope was never widened by a query nobody needed.
type fakeConsumerLookup struct {
	byKsuid map[string][]*pkgmodel.Resource
	asked   []string
}

func (f *fakeConsumerLookup) FindResourcesDependingOn(ksuid string) ([]*pkgmodel.Resource, error) {
	f.asked = append(f.asked, ksuid)
	return f.byKsuid[ksuid], nil
}

func requireSetOnceRefusal(t *testing.T, err error) []apimodel.SetOnceGeneratorField {
	t.Helper()
	var refusal apimodel.FormaGeneratorBoundToSetOnceFieldError
	require.ErrorAs(t, err, &refusal)
	return refusal.Fields
}

// The field the generator writes into directly is a node of the graph like any
// other, so a SetOnce strategy on it refuses the command and the field is named.
func TestRefuseSetOnceGeneratorFields_RefusesTheDestinationFieldAndNamesIt(t *testing.T) {
	setOnceBinding := `{"$gen":true,"$generator":"gen1","$output":"value","$visibility":"Opaque","$strategy":"SetOnce"}`
	seed := setOnceTestResource("res1", "app", "seed", `{"SecretString":`+setOnceBinding+`}`)

	err := refuseSetOnceGeneratorFields(
		setOnceTestDraw(), nil,
		[]resource_update.ResourceUpdate{setOnceTestUpdate(seed, seed)},
		&fakeConsumerLookup{})

	fields := requireSetOnceRefusal(t, err)
	assert.Equal(t, []apimodel.SetOnceGeneratorField{{
		GeneratorLabel: "db-password", GeneratorStack: "gen-stack",
		Stack: "app", Label: "seed", Type: "FakeAWS::SecretsManager::Secret",
		Field: "SecretString",
	}}, fields)
}

// The refusal is graph-wide. A destination that accepts the drawn value can
// still carry it on to a consumer that will not, and the drawn credential
// stops there just as surely, so the consumer's field is what gets named.
func TestRefuseSetOnceGeneratorFields_RefusesADownstreamConsumerField(t *testing.T) {
	seed := setOnceTestResource("res1", "app", "seed", `{"SecretString":`+genBinding+`}`)
	consumerDesired := setOnceTestResource("res2", "app", "api", `{"DbPassword":`+refToSeed+`}`)
	consumerPrior := setOnceTestResource("res2", "app", "api", `{"DbPassword":`+setOnceRefToSeed+`}`)

	err := refuseSetOnceGeneratorFields(
		setOnceTestDraw(), nil,
		[]resource_update.ResourceUpdate{
			setOnceTestUpdate(seed, seed),
			setOnceTestUpdate(consumerDesired, consumerPrior),
		},
		&fakeConsumerLookup{})

	fields := requireSetOnceRefusal(t, err)
	assert.Equal(t, []apimodel.SetOnceGeneratorField{{
		GeneratorLabel: "db-password", GeneratorStack: "gen-stack",
		Stack: "app", Label: "api", Type: "FakeAWS::SecretsManager::Secret",
		Field: "DbPassword",
	}}, fields, "the consumer's own field is the one that refuses the value")
}

// A consumer the command does not declare is still a node of the graph: the
// value it holds diverges from the seed's the moment the seed rotates, and no
// later apply can level them.
func TestRefuseSetOnceGeneratorFields_RefusesALiveConsumerOutsideTheCommand(t *testing.T) {
	seed := setOnceTestResource("res1", "app", "seed", `{"SecretString":`+genBinding+`}`)
	live := setOnceTestResource("res2", "jobs", "worker", `{"DbPassword":`+setOnceRefToSeed+`}`)

	lookup := &fakeConsumerLookup{byKsuid: map[string][]*pkgmodel.Resource{"res1": {&live}}}
	err := refuseSetOnceGeneratorFields(
		setOnceTestDraw(), nil,
		[]resource_update.ResourceUpdate{setOnceTestUpdate(seed, seed)},
		lookup)

	fields := requireSetOnceRefusal(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "worker", fields[0].Label)
	assert.Equal(t, "jobs", fields[0].Stack)
	assert.Equal(t, "DbPassword", fields[0].Field)
}

// The scope is the generator's reachable graph, not the stacks the command
// touches. A forma full of setOnce credentials that the generator does not
// reach is applied, or replacing one seed with a generator becomes a migration
// of every credential beside it.
func TestRefuseSetOnceGeneratorFields_AdmitsSetOnceFieldsTheGeneratorDoesNotReach(t *testing.T) {
	seed := setOnceTestResource("res1", "app", "seed", `{"SecretString":`+genBinding+`}`)
	reachedConsumer := setOnceTestResource("res2", "app", "api", `{"DbPassword":`+refToSeed+`}`)

	otherSeed := setOnceTestResource("res3", "app", "legacy-seed",
		`{"SecretString":{"$value":"digest","$visibility":"Opaque","$strategy":"SetOnce"}}`)
	otherConsumer := setOnceTestResource("res4", "app", "legacy-api",
		`{"DbPassword":{"$ref":"formae://res3#/SecretString","$visibility":"Opaque","$strategy":"SetOnce","$value":"digest"}}`)
	liveOther := setOnceTestResource("res5", "jobs", "legacy-worker",
		`{"DbPassword":{"$ref":"formae://res3#/SecretString","$visibility":"Opaque","$strategy":"SetOnce","$value":"digest"}}`)

	lookup := &fakeConsumerLookup{byKsuid: map[string][]*pkgmodel.Resource{"res3": {&liveOther}}}
	err := refuseSetOnceGeneratorFields(
		setOnceTestDraw(), nil,
		[]resource_update.ResourceUpdate{
			setOnceTestUpdate(seed, seed),
			setOnceTestUpdate(reachedConsumer, reachedConsumer),
			setOnceTestUpdate(otherSeed, otherSeed),
			setOnceTestUpdate(otherConsumer, otherConsumer),
		},
		lookup)

	require.NoError(t, err, "a setOnce credential graph beside the generator's own is none of its business")
	assert.NotContains(t, lookup.asked, "res3",
		"a resource outside the generator's graph must not even be expanded")
}

// A forma that draws nothing has no graph to walk, and the datastore is never
// asked about one.
func TestRefuseSetOnceGeneratorFields_AdmitsAFormaWithNoGenerator(t *testing.T) {
	plain := setOnceTestResource("res1", "app", "config",
		`{"SecretString":{"$value":"digest","$visibility":"Opaque","$strategy":"SetOnce"}}`)

	lookup := &fakeConsumerLookup{}
	err := refuseSetOnceGeneratorFields(nil, nil,
		[]resource_update.ResourceUpdate{setOnceTestUpdate(plain, plain)}, lookup)

	require.NoError(t, err)
	assert.Empty(t, lookup.asked)
}

// Every offending field goes into one refusal, in an order that does not
// depend on the order the datastore answered in: an operator has to go and
// find each one, and two runs of the same refused apply must name them the
// same way.
func TestRefuseSetOnceGeneratorFields_NamesEveryFieldInAStableOrder(t *testing.T) {
	seed := setOnceTestResource("res1", "app", "seed", `{"SecretString":`+genBinding+`}`)
	consumer := func(ksuid, stack, label string) pkgmodel.Resource {
		return setOnceTestResource(ksuid, stack, label, `{"DbPassword":`+setOnceRefToSeed+`}`)
	}

	refuse := func(order []*pkgmodel.Resource) []apimodel.SetOnceGeneratorField {
		lookup := &fakeConsumerLookup{byKsuid: map[string][]*pkgmodel.Resource{"res1": order}}
		return requireSetOnceRefusal(t, refuseSetOnceGeneratorFields(
			setOnceTestDraw(), nil,
			[]resource_update.ResourceUpdate{setOnceTestUpdate(seed, seed)},
			lookup))
	}

	gamma := consumer("res2", "jobs", "gamma")
	alpha := consumer("res3", "app", "alpha")
	beta := consumer("res4", "app", "beta")

	want := []apimodel.SetOnceGeneratorField{
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "app", Label: "alpha", Type: "FakeAWS::SecretsManager::Secret", Field: "DbPassword"},
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "app", Label: "beta", Type: "FakeAWS::SecretsManager::Secret", Field: "DbPassword"},
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "jobs", Label: "gamma", Type: "FakeAWS::SecretsManager::Secret", Field: "DbPassword"},
	}
	assert.Equal(t, want, refuse([]*pkgmodel.Resource{&gamma, &alpha, &beta}))
	assert.Equal(t, want, refuse([]*pkgmodel.Resource{&beta, &gamma, &alpha}))
}

// A live destination the datastore indexes for the generator seeds the walk
// too, so a consumer of a destination whose own document the command never
// re-declares is still reached.
func TestRefuseSetOnceGeneratorFields_SeedsFromLiveDestinationsAsWell(t *testing.T) {
	liveSeed := setOnceTestResource("res1", "app", "seed", `{"SecretString":`+genBinding+`}`)
	liveConsumer := setOnceTestResource("res2", "app", "api", `{"DbPassword":`+setOnceRefToSeed+`}`)

	lookup := &fakeConsumerLookup{byKsuid: map[string][]*pkgmodel.Resource{"res1": {&liveConsumer}}}
	err := refuseSetOnceGeneratorFields(
		setOnceTestDraw(),
		map[string][]*pkgmodel.Resource{"gen1": {&liveSeed}},
		nil, lookup)

	fields := requireSetOnceRefusal(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "DbPassword", fields[0].Field)
}
