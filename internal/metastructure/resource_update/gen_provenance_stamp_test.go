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
	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const (
	stampGeneratorKsuid = "2abc123def456ghi7jk8lmno9p0"
	stampGenerationID   = "2zyx987wvu654tsr321qponmlkj"
)

// The digest a delivered generator value is stamped with is compared, on the
// next apply, against the digest the planner computes for the generation the
// generator holds. The two are computed by different packages on different
// sides of a persist, so they are asserted equal here directly rather than
// inferred from a quiet re-apply: a mismatch of any kind — a wrapper, a
// prefix, a different domain — makes the root-versus-root rule unable to
// fire, and every re-apply then redraws with nothing reporting a fault.
func TestResolveGeneratorValue_StampsTheDigestThePlannerComputes(t *testing.T) {
	ru := genBoundCreate(stampGeneratorKsuid)
	require.NoError(t, ru.ResolveGeneratorValue(stampGeneratorKsuid, "drawn-credential", stampGenerationID, pkgmodel.FormaApplyModeReconcile))

	stamped := ru.ResolvedRootDigests[generatorSourceKey(stampGeneratorKsuid, "value")]
	require.True(t, provenance.Valid(stamped),
		"the delivered destination must carry a current-domain digest, got %q", stamped)

	// The planner's side, from the resolver's own answer for the same $gen
	// occurrence against a generator holding this generation.
	planned, err := resolver.LoadResolvablePropertiesFromStacks(
		genBoundCreate(stampGeneratorKsuid).DesiredState,
		map[string][]*pkgmodel.Resource{},
		map[string]json.RawMessage{},
		func(ksuid string) (pkgmodel.GeneratorIdentity, pkgmodel.Generator) {
			return pkgmodel.GeneratorIdentity{ID: ksuid, GenerationID: stampGenerationID}, nil
		},
	)
	require.NoError(t, err)
	answer, ok := planned.Answer(stampGeneratorKsuid, "value")
	require.True(t, ok, "the planner must answer the generator occurrence")

	assert.Equal(t, answer.SourceRootDigest, stamped,
		"the stamped digest and the planner's generation digest must be byte-identical")
}

// The stamp is only useful if it reaches the envelope that lands at rest: the
// write-origin merge of the provider's echo is what carries it from the
// update's digest map into the destination's $resolvedFrom.
func TestResolveGeneratorValue_StampReachesTheStoredEnvelope(t *testing.T) {
	ru := genBoundCreate(stampGeneratorKsuid)
	ru.DesiredState.Schema = pkgmodel.Schema{Identifier: "Name", Fields: []string{"Name", "SecretString"}}

	require.NoError(t, ru.ResolveGeneratorValue(stampGeneratorKsuid, "drawn-credential", stampGenerationID, pkgmodel.FormaApplyModeReconcile))
	require.NoError(t, ru.updateResourceProperties(`{"Name":"db","SecretString":"drawn-credential"}`, true))

	envelope := gjson.GetBytes(ru.DesiredState.Properties, "SecretString")
	require.True(t, envelope.IsObject(), "the envelope must survive the merge")
	assert.Equal(t, provenance.DigestOfString(stampGenerationID), envelope.Get("$resolvedFrom").String(),
		"the merged envelope must carry the generation the value was drawn from")
}

// A read-origin merge — a sync or discovery read, not the echo of formae's
// own write — attests nothing, so it must never stamp provenance a
// destination did not earn.
func TestResolveGeneratorValue_ReadOriginMergeStampsNothing(t *testing.T) {
	ru := genBoundCreate(stampGeneratorKsuid)
	ru.DesiredState.Schema = pkgmodel.Schema{Identifier: "Name", Fields: []string{"Name", "SecretString"}}

	require.NoError(t, ru.ResolveGeneratorValue(stampGeneratorKsuid, "drawn-credential", stampGenerationID, pkgmodel.FormaApplyModeReconcile))
	require.NoError(t, ru.updateResourceProperties(`{"Name":"db","SecretString":"drawn-credential"}`, false))

	assert.False(t, gjson.GetBytes(ru.DesiredState.Properties, "SecretString.$resolvedFrom").Exists(),
		"only the echo of formae's own write may attest where a value came from")
}

// A draw that names no generation leaves the destination unstamped rather
// than stamping a digest of nothing, which would be a well-formed digest
// attesting a generation that does not exist.
func TestResolveGeneratorValue_UnnamedGenerationStampsNothing(t *testing.T) {
	ru := genBoundCreate(stampGeneratorKsuid)

	require.NoError(t, ru.ResolveGeneratorValue(stampGeneratorKsuid, "drawn-credential", "", pkgmodel.FormaApplyModeReconcile))

	assert.Empty(t, ru.ResolvedRootDigests)
}

// The key a generator occurrence is stamped under is the key the merge reads
// back off the envelope. They are derived in different functions, so they are
// asserted to agree.
func TestReferenceURIOf_KeysATranslatedGeneratorEnvelope(t *testing.T) {
	envelope := gjson.Parse(`{"$gen":true,"$generator":"` + stampGeneratorKsuid + `","$output":"value","$visibility":"Opaque"}`)

	assert.Equal(t, generatorSourceKey(stampGeneratorKsuid, "value"), referenceURIOf(envelope))

	authored := gjson.Parse(`{"$gen":true,"$label":"db-password","$stack":"s","$output":"value"}`)
	assert.Empty(t, referenceURIOf(authored),
		"an untranslated envelope names no generator, so there is nothing to key on")
}
