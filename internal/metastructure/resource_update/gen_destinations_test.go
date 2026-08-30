// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

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

	t.Run("a destination being torn down is never drawn for", func(t *testing.T) {
		// A destroy sets DesiredState to the stored resource, so a delete
		// carries the stored $gen envelope and no provenance records. Drawing
		// for it would advance the generation and rotate the credential out
		// from under every consumer that survives.
		for _, op := range []OperationType{OperationDelete, OperationReaped} {
			updates := []ResourceUpdate{{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				PriorState:   pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    op,
			}}

			assert.Empty(t, GeneratorsNeedingDraw(updates), "operation %s", op)
		}
	})

	t.Run("a surviving consumer still draws when a sibling is deleted", func(t *testing.T) {
		updates := []ResourceUpdate{
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    OperationDelete,
			},
			{
				DesiredState: pkgmodel.Resource{Properties: genProperties("password", "gen1")},
				Operation:    OperationCreate,
			},
		}

		assert.Equal(t, []string{"gen1"}, GeneratorsNeedingDraw(updates))
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

// storedGenDestination is the same destination at rest: the drawn value
// hashed, and the generation it was drawn from recorded on the envelope.
func storedGenDestination(path, generatorKsuid, digest, resolvedFrom string) json.RawMessage {
	return json.RawMessage(`{"` + path + `":{"$gen":true,"$generator":"` + generatorKsuid +
		`","$output":"value","$visibility":"Opaque","$hashed":true,"$value":"` + digest +
		`","$resolvedFrom":"` + resolvedFrom + `"}}`)
}

func TestCarryStableGeneratorBindingForward(t *testing.T) {
	const ksuid = "2abcDEFghiJKLmnoPQRstuVWxyz"
	const digest = "9f2c1a0b"
	const writtenProvenance = "v1:aaaabbbbcccc"

	stableRecords := []OccurrenceRecord{{
		DestinationPath: "password",
		DesiredIdentity: OccurrenceIdentity{
			Kind: OccurrenceKindGenerator, Ksuid: ksuid, PropertyPath: "value",
		},
		WrittenProvenance: writtenProvenance,
		HasStoredWritten:  true,
		Class:             OccurrenceStable,
	}}

	t.Run("a stable destination keeps the digest it holds and the generation it holds it under", func(t *testing.T) {
		desired, seeds, err := CarryStableGeneratorBindingForward(
			genProperties("password", ksuid),
			storedGenDestination("password", ksuid, digest, writtenProvenance),
			stableRecords)
		require.NoError(t, err)

		envelope := gjson.GetBytes(desired, "password")
		assert.Equal(t, digest, envelope.Get("$value").String(),
			"the desired document must carry what the row already holds, or the row loses it")
		assert.True(t, envelope.Get("$hashed").Bool(),
			"without the marker the digest is hashed again as if it were a secret")
		assert.False(t, envelope.Get("$resolvedFrom").Exists(),
			"provenance is stamped by the write-origin merge, never written into the desired document")

		assert.Equal(t, map[string]string{
			generatorSourceKey(ksuid, "value"): writtenProvenance,
		}, seeds, "the merge needs the generation digest to stamp back")
	})

	// The gate. An occurrence that is NOT stable is one whose destination may
	// no longer hold the generation the row records, and a draw is planned for
	// it. Carrying the old digest and the old provenance forward would assert
	// that it did not move, and the next apply would read it as settled and
	// never rotate it.
	t.Run("an unstable destination is left exactly as it was", func(t *testing.T) {
		unstable := []OccurrenceRecord{{
			DestinationPath: "password",
			DesiredIdentity: OccurrenceIdentity{
				Kind: OccurrenceKindGenerator, Ksuid: ksuid, PropertyPath: "value",
			},
			WrittenProvenance: writtenProvenance,
			HasStoredWritten:  true,
			Class:             OccurrenceDeferredUpdate,
		}}

		input := genProperties("password", ksuid)
		desired, seeds, err := CarryStableGeneratorBindingForward(
			input,
			storedGenDestination("password", ksuid, digest, writtenProvenance),
			unstable)
		require.NoError(t, err)

		assert.JSONEq(t, string(input), string(desired),
			"nothing may be carried onto a destination that is about to be redrawn")
		assert.Empty(t, seeds,
			"stamping an unstable occurrence would suppress the rotation it exists to require")
	})

	t.Run("a destination with nothing stored is left alone", func(t *testing.T) {
		desired, seeds, err := CarryStableGeneratorBindingForward(
			genProperties("password", ksuid),
			json.RawMessage(`{}`),
			stableRecords)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(desired, "password.$value").Exists())
		assert.Empty(t, seeds)
	})
}
