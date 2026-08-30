// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package generator_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// stubSpecLookup answers GetGenerator from a (label, stack) keyed map. The
// generators it hands back carry no ID, exactly as a datastore load does:
// PasswordGenerator.ID is json:"-", so the KSUID never round-trips through
// generator_data.
type stubSpecLookup struct {
	byKey map[pkgmodel.GeneratorKey]pkgmodel.Generator
}

func (s *stubSpecLookup) GetGenerator(label, stackLabel string) (pkgmodel.Generator, error) {
	return s.byKey[pkgmodel.GeneratorKey{Label: label, Stack: stackLabel}], nil
}

func genEnvelopeProperties(path, generatorKsuid string) json.RawMessage {
	return json.RawMessage(`{"` + path + `":{"$gen":true,"$generator":"` + generatorKsuid + `","$output":"value","$visibility":"Opaque"}}`)
}

func TestSynthesizeDrawGeneratorUpdates_SecondConsumerOnUnchangedGeneratorDraws(t *testing.T) {
	// A stack already holds `db-password` with one applied consumer. The
	// author adds a second resource bound to the same generator; the
	// generator's own spec is untouched, so the row diff produces no
	// GeneratorUpdate at all. The new consumer still needs a value.
	const ksuid = "2abcDEFghiJKLmnoPQRstuVWxyz"

	stored := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 24}
	ds := &stubSpecLookup{byKey: map[pkgmodel.GeneratorKey]pkgmodel.Generator{
		{Label: "db-password", Stack: "default"}: stored,
	}}

	resourceUpdates := []resource_update.ResourceUpdate{
		// The pre-existing consumer: unchanged, provably stable.
		{
			DesiredState: pkgmodel.Resource{Label: "app-secret", Properties: genEnvelopeProperties("password", ksuid)},
			Operation:    resource_update.OperationUpdate,
			ProvenanceRecords: []resource_update.OccurrenceRecord{{
				DestinationPath: "password",
				DesiredIdentity: resource_update.OccurrenceIdentity{
					Kind: resource_update.OccurrenceKindGenerator, Ksuid: ksuid, PropertyPath: "value",
				},
				Class: resource_update.OccurrenceStable,
			}},
		},
		// The newly added consumer: no stored provenance at all.
		{
			DesiredState: pkgmodel.Resource{Label: "worker-secret", Properties: genEnvelopeProperties("password", ksuid)},
			Operation:    resource_update.OperationCreate,
		},
	}

	draws, err := SynthesizeDrawGeneratorUpdates(
		resourceUpdates,
		nil, // the generator's row did not change: no GeneratorUpdate exists
		map[pkgmodel.GeneratorKey]string{{Label: "db-password", Stack: "default"}: ksuid},
		ds,
	)
	require.NoError(t, err)
	require.Len(t, draws, 1)

	assert.Equal(t, GeneratorOperationDraw, draws[0].Operation)
	assert.Equal(t, GeneratorUpdateStateNotStarted, draws[0].State)
	assert.Equal(t, "default", draws[0].StackLabel)
	require.NotNil(t, draws[0].Generator)
	assert.Equal(t, "db-password", draws[0].Generator.GetLabel())
	// The KSUID a datastore load drops must be stamped back on, or the draw
	// has no identity to record its generation against.
	assert.Equal(t, ksuid, draws[0].Generator.GetID())
	assert.Nil(t, draws[0].ExistingGenerator)
}

func TestSynthesizeDrawGeneratorUpdates_StaleProvenanceOnUnchangedGeneratorRedraws(t *testing.T) {
	// The generator's spec is untouched and the destination has a written
	// value, but the generation the generator now holds is not the one that
	// value was stamped with — the shape left behind when a generation was
	// recorded and the destinations were never committed.
	const ksuid = "2abcDEFghiJKLmnoPQRstuVWxyz"

	rec := resource_update.OccurrenceRecord{
		DestinationPath: "password",
		DesiredIdentity: resource_update.OccurrenceIdentity{
			Kind: resource_update.OccurrenceKindGenerator, Ksuid: ksuid, PropertyPath: "value",
		},
		StoredIdentity: resource_update.OccurrenceIdentity{
			Kind: resource_update.OccurrenceKindGenerator, Ksuid: ksuid, PropertyPath: "value",
		},
		HasStoredWritten:  true,
		WrittenProvenance: provenance.DigestOfString("the generation the value was stamped with"),
		SourceRootDigest:  provenance.DigestOfString("the generation the generator holds now"),
		WrittenDigest:     provenance.DigestOfString("the value on the destination"),
	}
	resource_update.ClassifyOccurrence(&rec, true, false, false, func() (string, bool) { return "", false })
	require.NotEqual(t, resource_update.OccurrenceStable, rec.Class,
		"a destination stamped with a generation the generator no longer holds must not classify stable")

	stored := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 24}
	ds := &stubSpecLookup{byKey: map[pkgmodel.GeneratorKey]pkgmodel.Generator{
		{Label: "db-password", Stack: "default"}: stored,
	}}

	draws, err := SynthesizeDrawGeneratorUpdates(
		[]resource_update.ResourceUpdate{{
			DesiredState:      pkgmodel.Resource{Label: "app-secret", Properties: genEnvelopeProperties("password", ksuid)},
			Operation:         resource_update.OperationUpdate,
			ProvenanceRecords: []resource_update.OccurrenceRecord{rec},
		}},
		nil,
		map[pkgmodel.GeneratorKey]string{{Label: "db-password", Stack: "default"}: ksuid},
		ds,
	)
	require.NoError(t, err)
	require.Len(t, draws, 1)
	assert.Equal(t, ksuid, draws[0].Generator.GetID())
}

func TestSynthesizeDrawGeneratorUpdates_AllStableDestinationsDrawNothing(t *testing.T) {
	const ksuid = "2abcDEFghiJKLmnoPQRstuVWxyz"

	stored := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 24}
	ds := &stubSpecLookup{byKey: map[pkgmodel.GeneratorKey]pkgmodel.Generator{
		{Label: "db-password", Stack: "default"}: stored,
	}}

	draws, err := SynthesizeDrawGeneratorUpdates(
		[]resource_update.ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Label: "app-secret", Properties: genEnvelopeProperties("password", ksuid)},
			Operation:    resource_update.OperationUpdate,
			ProvenanceRecords: []resource_update.OccurrenceRecord{{
				DestinationPath: "password",
				DesiredIdentity: resource_update.OccurrenceIdentity{
					Kind: resource_update.OccurrenceKindGenerator, Ksuid: ksuid, PropertyPath: "value",
				},
				Class: resource_update.OccurrenceStable,
			}},
		}},
		nil,
		map[pkgmodel.GeneratorKey]string{{Label: "db-password", Stack: "default"}: ksuid},
		ds,
	)
	require.NoError(t, err)
	assert.Empty(t, draws)
}

func TestSynthesizeDrawGeneratorUpdates_PrefersThisCommandsDeclaredSpec(t *testing.T) {
	// A generator created or edited by this same command is not yet (or no
	// longer) what the datastore holds, so its draw must be built from the
	// declaration this command carries.
	const ksuid = "2abcDEFghiJKLmnoPQRstuVWxyz"

	declared := &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 40, ID: ksuid}
	ds := &stubSpecLookup{byKey: map[pkgmodel.GeneratorKey]pkgmodel.Generator{
		{Label: "db-password", Stack: "default"}: &pkgmodel.PasswordGenerator{Label: "db-password", Stack: "default", Length: 24},
	}}

	draws, err := SynthesizeDrawGeneratorUpdates(
		[]resource_update.ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Label: "app-secret", Properties: genEnvelopeProperties("password", ksuid)},
			Operation:    resource_update.OperationCreate,
		}},
		[]GeneratorUpdate{{
			Generator:  declared,
			Operation:  GeneratorOperationUpdate,
			StackLabel: "default",
		}},
		map[pkgmodel.GeneratorKey]string{{Label: "db-password", Stack: "default"}: ksuid},
		ds,
	)
	require.NoError(t, err)
	require.Len(t, draws, 1)

	spec, ok := draws[0].Generator.(*pkgmodel.PasswordGenerator)
	require.True(t, ok)
	assert.Equal(t, 40, spec.Length)
	assert.Equal(t, ksuid, spec.ID)
}

func TestSynthesizeDrawGeneratorUpdates_UnresolvableGeneratorDrawsNothing(t *testing.T) {
	// A $gen envelope naming a KSUID this command can map to no generator
	// has nothing to draw from. The command is not rejected over it: the
	// destination goes on to be refused at the provider boundary, which
	// names the property rather than a bare KSUID.
	const ksuid = "2abcDEFghiJKLmnoPQRstuVWxyz"

	draws, err := SynthesizeDrawGeneratorUpdates(
		[]resource_update.ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Label: "app-secret", Properties: genEnvelopeProperties("password", ksuid)},
			Operation:    resource_update.OperationCreate,
		}},
		nil,
		nil,
		&stubSpecLookup{},
	)
	require.NoError(t, err)
	assert.Empty(t, draws)
}
