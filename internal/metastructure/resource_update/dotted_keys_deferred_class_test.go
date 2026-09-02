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
)

// Two path consumers deliberately keep reading dots as nesting: provenance
// occurrence keys, and the patch layer's dotted-identity machinery. Both are
// identity-class rather than addressing-class — changing them is a cross-layer
// contract change that belongs with the structured-segments redesign, not here.
// These pins record what they do today, so a change to either is a decision
// rather than an accident, and they name the consequence in each case.

// A reference envelope under a literal dotted key is keyed by a path that reads
// the dots as nesting, so it cannot match the stored occurrence at the same
// place. The consequence is bounded: the occurrence classifies as a first
// declaration, which suppresses nothing and re-emits an op it could have
// suppressed. It never delivers a wrong value.
func TestProvenanceOccurrenceKeys_DottedKeyDegradesToFirstDeclaration(t *testing.T) {
	props := json.RawMessage(`{"metadata":{"annotations":{"objectset.rio.cattle.io/applied":` +
		`{"$ref":"formae://CL#/Arn","$value":"v"}}}}`)

	occurrences := collectReferenceEnvelopes(props)
	require.Len(t, occurrences, 1)

	assert.Equal(t, "metadata.annotations.objectset.rio.cattle.io/applied", occurrences[0].Path,
		"occurrence keys read every dot as nesting; the key is not an addressable path")

	// Which is exactly why the key cannot be used to read the envelope back.
	assert.False(t, gjson.GetBytes(props, occurrences[0].Path).Exists(),
		"the occurrence key is an identity, not a path: it does not resolve")
}

// A plain-key occurrence is unaffected, and its key does resolve.
func TestProvenanceOccurrenceKeys_PlainKeysUnchanged(t *testing.T) {
	props := json.RawMessage(`{"spec":{"refs":[{"target":{"$ref":"formae://CL#/Arn"}}]}}`)

	occurrences := collectReferenceEnvelopes(props)
	require.Len(t, occurrences, 1)

	assert.Equal(t, "spec.refs.0.target", occurrences[0].Path)
	assert.True(t, gjson.GetBytes(props, occurrences[0].Path).Exists())
}
