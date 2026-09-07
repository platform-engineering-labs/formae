// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package changeset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The resolve cache computes the root digest while it still holds the typed
// result: strings digest as strings (even JSON-looking ones), structured
// values as their JSON form, and wrapped opaque results are unwrapped first -
// never digested as the wrapper.
func TestRootDigestOf(t *testing.T) {
	assert.Equal(t, provenance.DigestOfString("hunter2"), rootDigestOf(gjson.Parse(`"hunter2"`)))
	assert.Equal(t, provenance.DigestOfJSON(`{"a":1}`), rootDigestOf(gjson.Parse(`{"a":1}`)))
	assert.Equal(t, provenance.DigestOfJSON(`1e3`), rootDigestOf(gjson.Parse(`1e3`)))

	wrapped := gjson.Parse(`{"$value":"hunter2","$visibility":"Opaque"}`)
	assert.Equal(t, provenance.DigestOfString("hunter2"), rootDigestOf(wrapped),
		"a wrapped opaque result digests as its value, never as the wrapper")
	assert.NotEqual(t, provenance.DigestOfJSON(wrapped.Raw), rootDigestOf(wrapped))

	// Cross-domain sanity: the digest equals the at-rest digest of the same
	// scalar, so undeclared-source comparisons stay comparable.
	assert.Equal(t, provenance.FromStored(pkgmodel.ComputeValueHash("hunter2")), rootDigestOf(gjson.Parse(`"hunter2"`)))
}
