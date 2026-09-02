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

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const provTestURI = "formae://2abcdefghijklmnopqrstuvwxyz#/SecretString"

// Stamping: only the write-origin merge, and only from a carried digest.
func TestMerge_ResolvedFromStamping(t *testing.T) {
	digest := provenance.DigestOfString("hunter2")
	user := json.RawMessage(`{"Password":{"$ref":"` + provTestURI + `","$value":"hunter2"}}`)
	plugin := json.RawMessage(`{"Password":"hunter2"}`)
	carrier := map[string]string{provTestURI: digest}

	t.Run("write-origin with carrier stamps", func(t *testing.T) {
		merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Password"}}, true, carrier)
		require.NoError(t, err)
		assert.Equal(t, digest, gjson.GetBytes(merged, "Password.$resolvedFrom").String())
	})

	t.Run("write-origin without a carrier entry stamps nothing", func(t *testing.T) {
		merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Password"}}, true, nil)
		require.NoError(t, err)
		assert.False(t, gjson.GetBytes(merged, "Password.$resolvedFrom").Exists(),
			"a missing digest degrades to no provenance, never to attesting a recomputed value")
	})

	t.Run("non-write merge never stamps", func(t *testing.T) {
		merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Password"}}, false, carrier)
		require.NoError(t, err)
		assert.False(t, gjson.GetBytes(merged, "Password.$resolvedFrom").Exists())
	})
}

// Invalidation is domain-correct: only an ADOPTED, genuinely differing
// observation drops the witness.
func TestMerge_ResolvedFromInvalidation(t *testing.T) {
	digest := provenance.DigestOfString("root-value")
	storedHash := pkgmodel.ComputeValueHash("written-value")
	envelope := `{"$ref":"` + provTestURI + `","$value":"` + storedHash + `","$hashed":true,"$visibility":"Opaque","$resolvedFrom":"` + digest + `"}`
	user := json.RawMessage(`{"Password":` + envelope + `}`)
	schema := pkgmodel.Schema{Fields: []string{"Password"}}

	t.Run("enriching read of the unchanged secret keeps the witness", func(t *testing.T) {
		plugin := json.RawMessage(`{"Password":"written-value"}`)
		merged, err := mergeRefsPreservingUserRefs(user, plugin, schema, false, nil)
		require.NoError(t, err)
		assert.Equal(t, digest, gjson.GetBytes(merged, "Password.$resolvedFrom").String(),
			"plaintext echo equal to the stored hash in the written domain must not invalidate")
	})

	t.Run("adopted differing observation drops the witness", func(t *testing.T) {
		plugin := json.RawMessage(`{"Password":"tampered-value"}`)
		merged, err := mergeRefsPreservingUserRefs(user, plugin, schema, false, nil)
		require.NoError(t, err)
		assert.False(t, gjson.GetBytes(merged, "Password.$resolvedFrom").Exists())
	})

	t.Run("absent plugin value keeps the witness", func(t *testing.T) {
		plugin := json.RawMessage(`{}`)
		merged, err := mergeRefsPreservingUserRefs(user, plugin, schema, false, nil)
		require.NoError(t, err)
		assert.Equal(t, digest, gjson.GetBytes(merged, "Password.$resolvedFrom").String())
	})

	t.Run("write-origin re-stamp overwrites", func(t *testing.T) {
		newDigest := provenance.DigestOfString("rotated-root")
		plugin := json.RawMessage(`{"Password":"new-written"}`)
		merged, err := mergeRefsPreservingUserRefs(user, plugin, schema, true, map[string]string{provTestURI: newDigest})
		require.NoError(t, err)
		assert.Equal(t, newDigest, gjson.GetBytes(merged, "Password.$resolvedFrom").String())
	})
}

// Regeneration reproduces planning's suppression from the immutable records:
// a stable nested occurrence mints no op through execution-time regeneration,
// while the genuinely changed sibling field still does.
func TestRegenerate_StableOccurrenceStaysSuppressed(t *testing.T) {
	storedHash := pkgmodel.ComputeValueHash("hunter2")
	schema := pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Settings"},
		Hints:      map[string]pkgmodel.FieldHint{"Name": {CreateOnly: true}},
	}
	ru := ResourceUpdate{
		Operation: OperationUpdate,
		PriorState: pkgmodel.Resource{
			Label: "contact", Type: "Test::Contact", Schema: schema,
			Properties: json.RawMessage(`{"Name":"contact","Settings":{"url":{"$ref":"` + provTestURI + `","$value":"` + storedHash + `","$hashed":true,"$visibility":"Opaque"},"note":"old"}}`),
		},
		DesiredState: pkgmodel.Resource{
			Label: "contact", Type: "Test::Contact", Schema: schema,
			Properties: json.RawMessage(`{"Name":"contact","Settings":{"url":{"$ref":"` + provTestURI + `"},"note":"new"}}`),
		},
		ProvenanceRecords: []OccurrenceRecord{{
			DestinationPath: "Settings.url",
			Class:           OccurrenceStable,
		}},
	}

	patchDoc, createOnly, err := ru.regeneratePatchDocument(pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnly)
	assert.Contains(t, string(patchDoc), "note", "the real sibling change still flows")
	assert.NotContains(t, string(patchDoc), "url", "the stable occurrence stays suppressed through regeneration")
}
