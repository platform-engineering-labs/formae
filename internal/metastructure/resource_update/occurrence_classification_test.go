// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
)

func occIdentity(t *testing.T, envelope string) OccurrenceIdentity {
	t.Helper()
	id, ok := NormalizeOccurrenceIdentity(gjson.Parse(envelope), nil)
	require.True(t, ok, "fixture envelope must normalize")
	return id
}

// The identity normalizer maps every envelope shape of the same reference to
// one identity, and distinguishes genuine repoints and selector changes.
func TestNormalizeOccurrenceIdentity(t *testing.T) {
	refEnv := `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/SecretString"}`
	resEnv := `{"$res":true,"$label":"s","$type":"Test::Secret","$stack":"default","$property":"SecretString"}`
	translate := map[string]string{"default\x00s\x00Test::Secret": "2abcdefghijklmnopqrstuvwxyz"}

	refID, ok := NormalizeOccurrenceIdentity(gjson.Parse(refEnv), nil)
	require.True(t, ok)
	resID, ok := NormalizeOccurrenceIdentity(gjson.Parse(resEnv), func(stack, label, typ string) (string, bool) {
		k, found := translate[stack+"\x00"+label+"\x00"+typ]
		return k, found
	})
	require.True(t, ok)
	assert.Equal(t, refID, resID, "a $res to $ref lifecycle rewrite is the same identity, not a repoint")

	otherRef := occIdentity(t, `{"$ref":"formae://2zzzzzzzzzzzzzzzzzzzzzzzzzz#/SecretString"}`)
	assert.NotEqual(t, refID, otherRef, "a different source is a repoint")

	otherProp := occIdentity(t, `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/Other"}`)
	assert.NotEqual(t, refID, otherProp, "a different property is a repoint")

	withJSON := occIdentity(t, `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/SecretString","$json":"password"}`)
	assert.NotEqual(t, refID, withJSON, "the extraction selector is part of identity")

	// A $res that cannot translate fails closed.
	_, ok = NormalizeOccurrenceIdentity(gjson.Parse(resEnv), func(string, string, string) (string, bool) { return "", false })
	assert.False(t, ok)
}

func TestClassifyOccurrence(t *testing.T) {
	rootA := provenance.DigestOfString("secret-a")
	rootB := provenance.DigestOfString("secret-b")
	writtenLeaf := provenance.DigestOfString("leaf-value")
	id := occIdentity(t, `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/S"}`)
	otherID := occIdentity(t, `{"$ref":"formae://2zzzzzzzzzzzzzzzzzzzzzzzzzz#/S"}`)

	base := func() *OccurrenceRecord {
		return &OccurrenceRecord{
			DestinationPath:   "Password",
			DesiredIdentity:   id,
			StoredIdentity:    id,
			HasStoredWritten:  true,
			SourceRootDigest:  rootA,
			WrittenProvenance: rootA,
			WrittenDigest:     writtenLeaf,
		}
	}
	noLeaf := func() (string, bool) { return "", false }

	t.Run("zero value never suppresses", func(t *testing.T) {
		assert.NotEqual(t, OccurrenceStable, OccurrenceClass(0))
	})

	t.Run("first declaration defers before any unknown fallback", func(t *testing.T) {
		rec := base()
		rec.HasStoredWritten = false
		rec.WrittenProvenance = ""
		rec.SourceRootDigest = ""
		ClassifyOccurrence(rec, true, true, false, noLeaf)
		assert.Equal(t, OccurrenceDeferredUpdate, rec.Class,
			"nothing was ever written: normal diff semantics decide, even on createOnly")
	})

	t.Run("non-opaque defers", func(t *testing.T) {
		rec := base()
		ClassifyOccurrence(rec, false, false, false, noLeaf)
		assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	})

	t.Run("force bypasses suppression", func(t *testing.T) {
		rec := base()
		ClassifyOccurrence(rec, true, false, true, noLeaf)
		assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	})

	t.Run("repoint defers despite equal digests", func(t *testing.T) {
		rec := base()
		rec.DesiredIdentity = otherID
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	})

	t.Run("equal roots and identity are stable", func(t *testing.T) {
		rec := base()
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceStable, rec.Class)
	})

	t.Run("empty secret is a real stable value", func(t *testing.T) {
		rec := base()
		empty := provenance.DigestOfString("")
		rec.SourceRootDigest = empty
		rec.WrittenProvenance = empty
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceStable, rec.Class)
	})

	t.Run("moved root on a bare reference defers", func(t *testing.T) {
		rec := base()
		rec.SourceRootDigest = rootB
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	})

	t.Run("moved root with an unmoved json leaf is stable", func(t *testing.T) {
		rec := base()
		rec.SourceRootDigest = rootB
		ClassifyOccurrence(rec, true, false, false, func() (string, bool) { return writtenLeaf, true })
		assert.Equal(t, OccurrenceStable, rec.Class)
	})

	t.Run("moved root with a moved json leaf defers", func(t *testing.T) {
		rec := base()
		rec.SourceRootDigest = rootB
		ClassifyOccurrence(rec, true, false, false, func() (string, bool) { return provenance.DigestOfString("new-leaf"), true })
		assert.Equal(t, OccurrenceDeferredUpdate, rec.Class)
	})

	t.Run("forged or malformed provenance is unknown, not comparable", func(t *testing.T) {
		rec := base()
		rec.WrittenProvenance = "v1:not-hex-at-all"
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceConvergeUnknown, rec.Class, "mutable destination converges once")
	})

	t.Run("legacy bare-hex provenance is unknown", func(t *testing.T) {
		rec := base()
		rec.WrittenProvenance = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceConvergeUnknown, rec.Class)
	})

	t.Run("unknown on a createOnly destination stays stable", func(t *testing.T) {
		rec := base()
		rec.WrittenProvenance = ""
		ClassifyOccurrence(rec, true, true, false, noLeaf)
		assert.Equal(t, OccurrenceStable, rec.Class, "never a replacement on unknown")
	})

	t.Run("unknown source digest on mutable converges", func(t *testing.T) {
		rec := base()
		rec.SourceRootDigest = ""
		ClassifyOccurrence(rec, true, false, false, noLeaf)
		assert.Equal(t, OccurrenceConvergeUnknown, rec.Class)
	})
}
