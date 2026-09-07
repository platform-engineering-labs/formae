// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/generator_update"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The refusal names destinations an operator has to go and find, so the order
// it names them in has to be the same every time the same apply is refused.
// None of the datastore backends order the rows they answer with, so the order
// is imposed here.
func TestRefuseUnreachableGeneratorDestinations_NamesDestinationsInAStableOrder(t *testing.T) {
	generator := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "gen-stack"}
	generator.SetID("gen1")
	draws := []generator_update.GeneratorUpdate{{Generator: generator, StackLabel: "gen-stack"}}

	destination := func(ksuid, stack, label string) *pkgmodel.Resource {
		return &pkgmodel.Resource{
			Ksuid: ksuid, Stack: stack, Label: label,
			Type: "FakeAWS::SecretsManager::Secret",
		}
	}
	rows := []*pkgmodel.Resource{
		destination("res3", "beta-stack", "gamma"),
		destination("res1", "alpha-stack", "beta"),
		destination("res4", "beta-stack", "alpha"),
		destination("res2", "alpha-stack", "alpha"),
	}

	refuse := func(rows []*pkgmodel.Resource) []apimodel.UnreachableGeneratorDestination {
		err := refuseUnreachableGeneratorDestinations(
			draws, map[string][]*pkgmodel.Resource{"gen1": rows}, nil)
		var unreachable apimodel.FormaGeneratorDestinationsUnreachableError
		require.ErrorAs(t, err, &unreachable)
		return unreachable.Unreachable
	}

	want := []apimodel.UnreachableGeneratorDestination{
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "alpha-stack", Label: "alpha", Type: "FakeAWS::SecretsManager::Secret"},
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "alpha-stack", Label: "beta", Type: "FakeAWS::SecretsManager::Secret"},
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "beta-stack", Label: "alpha", Type: "FakeAWS::SecretsManager::Secret"},
		{GeneratorLabel: "db-password", GeneratorStack: "gen-stack", Stack: "beta-stack", Label: "gamma", Type: "FakeAWS::SecretsManager::Secret"},
	}
	assert.Equal(t, want, refuse(rows), "the destinations must be named by stack and then by label")

	reversed := make([]*pkgmodel.Resource, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		reversed = append(reversed, rows[i])
	}
	assert.Equal(t, want, refuse(reversed),
		"the order the datastore answers in must not reach the operator")
}
