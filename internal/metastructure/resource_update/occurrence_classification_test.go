// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func occIdentity(t *testing.T, envelope string) OccurrenceIdentity {
	t.Helper()
	id, ok := NormalizeOccurrenceIdentity(gjson.Parse(envelope), nil, nil)
	require.True(t, ok, "fixture envelope must normalize")
	return id
}

// The identity normalizer maps every envelope shape of the same reference to
// one identity, and distinguishes genuine repoints and selector changes.
func TestNormalizeOccurrenceIdentity(t *testing.T) {
	refEnv := `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/SecretString"}`
	resEnv := `{"$res":true,"$label":"s","$type":"Test::Secret","$stack":"default","$property":"SecretString"}`
	translate := map[string]string{"default\x00s\x00Test::Secret": "2abcdefghijklmnopqrstuvwxyz"}

	refID, ok := NormalizeOccurrenceIdentity(gjson.Parse(refEnv), nil, nil)
	require.True(t, ok)
	resID, ok := NormalizeOccurrenceIdentity(gjson.Parse(resEnv), func(stack, label, typ string) (string, bool) {
		k, found := translate[stack+"\x00"+label+"\x00"+typ]
		return k, found
	}, nil)
	require.True(t, ok)
	assert.Equal(t, refID, resID, "a $res to $ref lifecycle rewrite is the same identity, not a repoint")

	otherRef := occIdentity(t, `{"$ref":"formae://2zzzzzzzzzzzzzzzzzzzzzzzzzz#/SecretString"}`)
	assert.NotEqual(t, refID, otherRef, "a different source is a repoint")

	otherProp := occIdentity(t, `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/Other"}`)
	assert.NotEqual(t, refID, otherProp, "a different property is a repoint")

	withJSON := occIdentity(t, `{"$ref":"formae://2abcdefghijklmnopqrstuvwxyz#/SecretString","$json":"password"}`)
	assert.NotEqual(t, refID, withJSON, "the extraction selector is part of identity")

	// A $res that cannot translate fails closed.
	_, ok = NormalizeOccurrenceIdentity(gjson.Parse(resEnv), func(string, string, string) (string, bool) { return "", false }, nil)
	assert.False(t, ok)
}

// A $gen and a $ref that happen to carry the same KSUID and path are
// different occurrences, and must not compare equal.
func TestNormalizeOccurrenceIdentity_GenAndRefWithSameKsuidDiffer(t *testing.T) {
	const ksuid = "2abcdefghijklmnopqrstuvwxyz"
	refID, ok := NormalizeOccurrenceIdentity(gjson.Parse(`{"$ref":"formae://`+ksuid+`#/value"}`), nil, nil)
	require.True(t, ok)
	genID, ok := NormalizeOccurrenceIdentity(
		gjson.Parse(`{"$gen":true,"$generator":"`+ksuid+`","$output":"value","$visibility":"Opaque"}`), nil, nil)
	require.True(t, ok)

	require.Equal(t, refID.Ksuid, genID.Ksuid, "fixture must share a KSUID")
	require.Equal(t, refID.PropertyPath, genID.PropertyPath, "fixture must share a path")
	assert.NotEqual(t, refID, genID, "same KSUID and path from different tables are still different occurrences")
}

// A translated $gen normalizes to the generator's KSUID and its output name.
func TestNormalizeOccurrenceIdentity_TranslatedGen(t *testing.T) {
	env := gjson.Parse(`{"$gen":true,"$generator":"2abcdefghijklmnopqrstuvwxyz","$output":"value","$visibility":"Opaque"}`)
	id, ok := NormalizeOccurrenceIdentity(env, nil, nil)
	require.True(t, ok)
	assert.Equal(t, OccurrenceIdentity{
		Kind:         OccurrenceKindGenerator,
		Ksuid:        "2abcdefghijklmnopqrstuvwxyz",
		PropertyPath: "value",
	}, id)
}

// An authored $gen normalizes through the lookup, so a $gen envelope and its
// translated form compare equal — a lifecycle rewrite is not a repoint.
func TestNormalizeOccurrenceIdentity_AuthoredGenMatchesTranslated(t *testing.T) {
	authored := `{"$gen":true,"$label":"db-password","$stack":"default","$output":"value","$visibility":"Opaque"}`
	translated := `{"$gen":true,"$generator":"2abcdefghijklmnopqrstuvwxyz","$output":"value","$visibility":"Opaque"}`
	lookup := func(stack, label string) (string, bool) {
		if stack == "default" && label == "db-password" {
			return "2abcdefghijklmnopqrstuvwxyz", true
		}
		return "", false
	}

	authoredID, ok := NormalizeOccurrenceIdentity(gjson.Parse(authored), nil, lookup)
	require.True(t, ok)
	translatedID, ok := NormalizeOccurrenceIdentity(gjson.Parse(translated), nil, nil)
	require.True(t, ok)
	assert.Equal(t, translatedID, authoredID, "a $gen lifecycle rewrite is the same identity, not a repoint")
}

// A $gen that cannot be normalized fails closed.
func TestNormalizeOccurrenceIdentity_UnresolvableGenFailsClosed(t *testing.T) {
	authored := gjson.Parse(`{"$gen":true,"$label":"db-password","$stack":"default","$output":"value"}`)

	_, ok := NormalizeOccurrenceIdentity(authored, nil, nil)
	assert.False(t, ok, "no generator lookup provided")

	_, ok = NormalizeOccurrenceIdentity(authored, nil, func(string, string) (string, bool) { return "", false })
	assert.False(t, ok, "generator lookup misses")
}

// Every already-persisted OccurrenceRecord has no Kind key: it must
// deserialize as a resource occurrence, not silently read back as a
// generator reference.
func TestOccurrenceIdentity_ZeroValueIsResourceKind(t *testing.T) {
	var id OccurrenceIdentity
	require.NoError(t, json.Unmarshal([]byte(`{"Ksuid":"2abcdefghijklmnopqrstuvwxyz","PropertyPath":"SecretString","JSONPath":""}`), &id))
	assert.Equal(t, OccurrenceKindResource, id.Kind, "a Kind-less legacy record must read back as a resource occurrence")

	var rec OccurrenceRecord
	require.NoError(t, json.Unmarshal([]byte(`{
		"DestinationPath": "Password",
		"DesiredIdentity": {"Ksuid":"2abcdefghijklmnopqrstuvwxyz","PropertyPath":"SecretString","JSONPath":""},
		"StoredIdentity": {"Ksuid":"2abcdefghijklmnopqrstuvwxyz","PropertyPath":"SecretString","JSONPath":""},
		"HasStoredWritten": true,
		"Class": 1
	}`), &rec))
	assert.Equal(t, OccurrenceKindResource, rec.DesiredIdentity.Kind)
	assert.Equal(t, OccurrenceKindResource, rec.StoredIdentity.Kind)
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

// The API projection is a positive exclusion boundary: provenance state never
// reaches API consumers.
func TestAPIProjectionCarriesNoProvenance(t *testing.T) {
	typ := reflect.TypeOf(apimodel.ResourceUpdate{})
	for _, forbidden := range []string{"ProvenanceRecords", "ResolvedRootDigests"} {
		_, found := typ.FieldByName(forbidden)
		assert.False(t, found, "apimodel.ResourceUpdate must not carry %s", forbidden)
	}
}

func TestConvergenceOnly(t *testing.T) {
	occ := func(path string, class OccurrenceClass, repoint bool) OccurrenceRecord {
		stored := OccurrenceIdentity{Ksuid: "2ABcDeFgHiJkLmNoPqRsTuVwXyZ", PropertyPath: "SecretString"}
		desired := stored
		if repoint {
			desired.Ksuid = "9ZyXwVuTsRqPoNmLkJiHgFeDcBa"
		}
		return OccurrenceRecord{
			DestinationPath: path, DesiredIdentity: desired, StoredIdentity: stored,
			HasStoredWritten: true, Class: class,
		}
	}
	update := func(patch string, recs ...OccurrenceRecord) *ResourceUpdate {
		return &ResourceUpdate{
			Operation:         OperationUpdate,
			ProvenanceRecords: recs,
			DesiredState:      pkgmodel.Resource{PatchDocument: json.RawMessage(patch)},
		}
	}

	t.Run("all ops on converging occurrences", func(t *testing.T) {
		u := update(`[{"op":"replace","path":"/Settings/url","value":""}]`, occ("Settings.url", OccurrenceConvergeUnknown, false))
		assert.True(t, u.ConvergenceOnly())
	})
	t.Run("a repoint is a real change", func(t *testing.T) {
		u := update(`[{"op":"replace","path":"/Settings/url","value":""}]`, occ("Settings.url", OccurrenceDeferredUpdate, true))
		assert.False(t, u.ConvergenceOnly())
	})
	t.Run("an op outside the occurrences is a real change", func(t *testing.T) {
		u := update(`[{"op":"replace","path":"/Settings/url","value":""},{"op":"replace","path":"/Settings/recipient","value":"#x"}]`,
			occ("Settings.url", OccurrenceConvergeUnknown, false))
		assert.False(t, u.ConvergenceOnly())
	})
	t.Run("a stable occurrence grants no exemption", func(t *testing.T) {
		u := update(`[{"op":"replace","path":"/Settings/url","value":""}]`, occ("Settings.url", OccurrenceStable, false))
		assert.False(t, u.ConvergenceOnly())
	})
	t.Run("no records means no exemption", func(t *testing.T) {
		u := update(`[{"op":"replace","path":"/Name","value":"x"}]`)
		assert.False(t, u.ConvergenceOnly())
	})
}
