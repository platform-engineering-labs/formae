// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestReferenceFreePropertiesDoNotObserveUnrelatedResources(t *testing.T) {
	for _, properties := range []string{`{}`, `{"name":"after"}`, `{"nested":{"items":["literal",true,3]}}`} {
		t.Run(properties, func(t *testing.T) {
			observed := 0
			props, err := LoadResolvablePropertiesFromStacks(
				pkgmodel.Resource{Properties: json.RawMessage(properties)},
				map[string][]*pkgmodel.Resource{"unrelated": {{Ksuid: "elsewhere", Properties: json.RawMessage(`{"name":"unrelated"}`)}}},
				nil, nil, func(string, *pkgmodel.Resource) { observed++ },
			)
			require.NoError(t, err)
			require.Empty(t, props.props)
			require.Zero(t, observed)
		})
	}
}

func TestGeneratorOnlyPropertiesPreserveAnswersWithUnrelatedResources(t *testing.T) {
	spec := password(func(*pkgmodel.PasswordGenerator) {})
	identity := generationOf(t, "generation-before-early-return", spec)
	lookups, observations := 0, 0
	props, err := LoadResolvablePropertiesFromStacks(
		genConsumer(),
		map[string][]*pkgmodel.Resource{"unrelated": {{Ksuid: "elsewhere"}}},
		nil,
		func(id string) (pkgmodel.GeneratorIdentity, pkgmodel.Generator) {
			lookups++
			require.Equal(t, generatorKsuid, id)
			return identity, spec
		},
		func(string, *pkgmodel.Resource) { observations++ },
	)
	require.NoError(t, err)
	require.Equal(t, 1, lookups)
	require.Zero(t, observations)
	answer, ok := props.Answer(generatorKsuid, "value")
	require.True(t, ok)
	require.Equal(t, AnswerDeferred, answer.Kind)
	require.True(t, answer.Opaque)
	require.Equal(t, provenance.DigestOfString(identity.GenerationID), answer.SourceRootDigest)
	_, exists := props.Get(generatorKsuid, "value")
	require.False(t, exists, "a generator output is deferred until execution")
}

func BenchmarkLoadResolvablePropertiesWithoutReferences(b *testing.B) {
	for _, count := range []int{0, 20000} {
		b.Run(fmt.Sprintf("resources=%d", count), func(b *testing.B) {
			resources := make([]*pkgmodel.Resource, count)
			for i := range resources {
				resources[i] = &pkgmodel.Resource{Ksuid: fmt.Sprintf("scale-%05d", i)}
			}
			stacks := map[string][]*pkgmodel.Resource{"scale": resources}
			resource := pkgmodel.Resource{Properties: json.RawMessage(`{"name":"after"}`)}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				props, err := LoadResolvablePropertiesFromStacks(resource, stacks, nil, nil)
				if err != nil || len(props.props) != 0 {
					b.Fatalf("unexpected result: properties=%v, error=%v", props.props, err)
				}
			}
		})
	}
}
