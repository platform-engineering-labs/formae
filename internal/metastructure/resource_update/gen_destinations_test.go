// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func genProperties(path, generatorKsuid string) json.RawMessage {
	return json.RawMessage(`{"` + path + `":{"$gen":true,"$generator":"` + generatorKsuid + `","$output":"value","$visibility":"Opaque"}}`)
}

func TestIsGenDestinationStable(t *testing.T) {
	t.Run("a record classified stable at the destination path is stable", func(t *testing.T) {
		records := []OccurrenceRecord{{
			DestinationPath: "password",
			DesiredIdentity: OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: "gen1", PropertyPath: "value"},
			Class:           OccurrenceStable,
		}}

		assert.True(t, IsGenDestinationStable(records, "password"))
	})

	t.Run("a record that must plan is not stable", func(t *testing.T) {
		records := []OccurrenceRecord{{
			DestinationPath: "password",
			DesiredIdentity: OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: "gen1", PropertyPath: "value"},
			Class:           OccurrenceDeferredUpdate,
		}}

		assert.False(t, IsGenDestinationStable(records, "password"))
	})

	t.Run("a destination with no record at all is not stable", func(t *testing.T) {
		assert.False(t, IsGenDestinationStable(nil, "password"))
		assert.False(t, IsGenDestinationStable([]OccurrenceRecord{{
			DestinationPath: "other",
			Class:           OccurrenceStable,
		}}, "password"))
	})

	t.Run("a stable resource reference at the path is not a stable gen destination", func(t *testing.T) {
		records := []OccurrenceRecord{{
			DestinationPath: "password",
			DesiredIdentity: OccurrenceIdentity{Kind: OccurrenceKindResource, Ksuid: "res1"},
			Class:           OccurrenceStable,
		}}

		assert.False(t, IsGenDestinationStable(records, "password"))
	})
}

func TestGeneratorsNeedingDraw(t *testing.T) {
	t.Run("a non-stable destination needs its generator drawn", func(t *testing.T) {
		updates := []ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
			Operation:    OperationUpdate,
			ProvenanceRecords: []OccurrenceRecord{{
				DestinationPath: "password",
				DesiredIdentity: OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: "gen1", PropertyPath: "value"},
				Class:           OccurrenceDeferredUpdate,
			}},
		}}

		assert.Equal(t, []string{"gen1"}, GeneratorsNeedingDraw(updates))
	})

	t.Run("a generator whose every destination is stable is not drawn", func(t *testing.T) {
		updates := []ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
			Operation:    OperationUpdate,
			ProvenanceRecords: []OccurrenceRecord{{
				DestinationPath: "password",
				DesiredIdentity: OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: "gen1", PropertyPath: "value"},
				Class:           OccurrenceStable,
			}},
		}}

		assert.Empty(t, GeneratorsNeedingDraw(updates))
	})

	t.Run("one non-stable destination is enough when a sibling is stable", func(t *testing.T) {
		updates := []ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    OperationUpdate,
				ProvenanceRecords: []OccurrenceRecord{{
					DestinationPath: "password",
					DesiredIdentity: OccurrenceIdentity{Kind: OccurrenceKindGenerator, Ksuid: "gen1", PropertyPath: "value"},
					Class:           OccurrenceStable,
				}},
			},
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    OperationCreate,
			},
		}

		assert.Equal(t, []string{"gen1"}, GeneratorsNeedingDraw(updates))
	})

	t.Run("a create carries no provenance records and always needs a draw", func(t *testing.T) {
		updates := []ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
			Operation:    OperationCreate,
		}}

		assert.Equal(t, []string{"gen1"}, GeneratorsNeedingDraw(updates))
	})

	t.Run("an untranslated gen envelope names no generator and is skipped", func(t *testing.T) {
		updates := []ResourceUpdate{{
			DesiredState: pkgmodel.Resource{Properties: json.RawMessage(
				`{"password":{"$gen":true,"$label":"db-password","$stack":"default","$output":"value"}}`)},
			Operation: OperationCreate,
		}}

		assert.Empty(t, GeneratorsNeedingDraw(updates))
	})

	t.Run("distinct generators are reported once each in first-seen order", func(t *testing.T) {
		updates := []ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    OperationCreate,
			},
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen2")},
				Operation:    OperationCreate,
			},
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    OperationCreate,
			},
		}

		assert.Equal(t, []string{"gen1", "gen2"}, GeneratorsNeedingDraw(updates))
	})
}
