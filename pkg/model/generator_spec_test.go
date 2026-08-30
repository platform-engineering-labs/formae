//go:build unit

package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func pw(mutate func(*pkgmodel.PasswordGenerator)) *pkgmodel.PasswordGenerator {
	g := &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "durable",
		Length: 32, Uppercase: true, Lowercase: true, Digits: true,
		Symbols: false, ExcludeCharacters: "", RequireEachIncludedType: true,
	}
	mutate(g)
	return g
}

func TestGenerationSatisfies_IdenticalSpecIsSatisfied(t *testing.T) {
	assert.True(t, pkgmodel.GenerationSatisfies(pw(func(*pkgmodel.PasswordGenerator) {}),
		pw(func(*pkgmodel.PasswordGenerator) {})))
}

func TestGenerationSatisfies_WideningTheAlphabetIsSatisfied(t *testing.T) {
	// The drawn value uses only the narrower alphabet, so it is still
	// drawable under the wider one. Enabling symbols must not regenerate,
	// provided the wider spec does not also demand a symbol be present.
	drawn := pw(func(g *pkgmodel.PasswordGenerator) { g.Symbols = false; g.RequireEachIncludedType = false })
	desired := pw(func(g *pkgmodel.PasswordGenerator) { g.Symbols = true; g.RequireEachIncludedType = false })
	assert.True(t, pkgmodel.GenerationSatisfies(drawn, desired))
}

func TestGenerationSatisfies_NarrowingTheAlphabetIsNotSatisfied(t *testing.T) {
	// A drawn value may contain a symbol; the desired spec excludes symbols,
	// so it may no longer be acceptable.
	drawn := pw(func(g *pkgmodel.PasswordGenerator) { g.Symbols = true })
	desired := pw(func(g *pkgmodel.PasswordGenerator) { g.Symbols = false })
	assert.False(t, pkgmodel.GenerationSatisfies(drawn, desired))
}

func TestGenerationSatisfies_ExcludingADrawableCharacterIsNotSatisfied(t *testing.T) {
	drawn := pw(func(*pkgmodel.PasswordGenerator) {})
	desired := pw(func(g *pkgmodel.PasswordGenerator) { g.ExcludeCharacters = "0" })
	assert.False(t, pkgmodel.GenerationSatisfies(drawn, desired))
}

func TestGenerationSatisfies_LengthChangeIsNotSatisfied(t *testing.T) {
	drawn := pw(func(*pkgmodel.PasswordGenerator) {})
	desired := pw(func(g *pkgmodel.PasswordGenerator) { g.Length = 40 })
	assert.False(t, pkgmodel.GenerationSatisfies(drawn, desired))
}

func TestGenerationSatisfies_NewlyRequiredClassNotGuaranteedIsNotSatisfied(t *testing.T) {
	// The drawn spec guaranteed nothing, so no class is known to be present.
	drawn := pw(func(g *pkgmodel.PasswordGenerator) { g.RequireEachIncludedType = false })
	desired := pw(func(g *pkgmodel.PasswordGenerator) { g.RequireEachIncludedType = true })
	assert.False(t, pkgmodel.GenerationSatisfies(drawn, desired))
}

func TestGenerationSatisfies_DroppingARequirementIsSatisfied(t *testing.T) {
	drawn := pw(func(g *pkgmodel.PasswordGenerator) { g.RequireEachIncludedType = true })
	desired := pw(func(g *pkgmodel.PasswordGenerator) { g.RequireEachIncludedType = false })
	assert.True(t, pkgmodel.GenerationSatisfies(drawn, desired))
}

func TestGenerationSatisfies_DifferentGeneratorTypesAreNotSatisfied(t *testing.T) {
	assert.False(t, pkgmodel.GenerationSatisfies(pw(func(*pkgmodel.PasswordGenerator) {}), nil))
}

// A typed nil generator is not a satisfied generation. The rotation decision
// must fail safe rather than panic when a lookup yields no generator.
func TestGenerationSatisfies_TypedNilIsNotSatisfied(t *testing.T) {
	var missing *pkgmodel.PasswordGenerator
	assert.False(t, pkgmodel.GenerationSatisfies(missing, pw(func(*pkgmodel.PasswordGenerator) {})))
	assert.False(t, pkgmodel.GenerationSatisfies(pw(func(*pkgmodel.PasswordGenerator) {}), missing))
}
