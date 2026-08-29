// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestFirstPointerSegment(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"/Spec", "Spec", true},
		{"/Spec/x", "Spec", true},
		{"/Spec/0/y", "Spec", true},
		{"/Specification", "Specification", true},
		{"/a~1b/c", "a/b", true},
		{"/a~0b", "a~b", true},
		{"/a~01", "a~1", true},
		{"", "", false},
		{"Spec", "", false},
		{"/", "", false},
	}
	for _, c := range cases {
		got, ok := firstPointerSegment(c.path)
		assert.Equal(t, c.ok, ok, "path %q ok", c.path)
		if c.ok {
			assert.Equal(t, c.want, got, "path %q", c.path)
		}
	}
}

func TestIsKeepableReferenceEnvelope(t *testing.T) {
	keep := []string{
		`{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/SecretString"}`,
		`{"$ref":"formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S","$value":"x","$visibility":"Opaque"}`,
		`{"$res":true,"$label":"a","$type":"T::B","$stack":"s","$property":"P"}`,
		`{"$res":true,"$label":"a","$type":"T::B","$stack":"s","$property":"P","$json":"host"}`,
	}
	drop := []string{
		`{"$res":false,"$label":"a","$type":"T::B","$stack":"s","$property":"P"}`,
		`{"$res":true,"$label":"a","$type":"T::B","$stack":"s"}`,
		`{"$res":true,"$label":"","$type":"T::B","$stack":"s","$property":"P"}`,
		`{"$ref":42}`,
		`{"$ref":"formae://#/S"}`,
		`{"$ref":"not-a-uri"}`,
		`{"$ref":"formae://#/S","$res":true,"$label":"a","$type":"T","$stack":"s","$property":"P"}`,
		`"plain"`,
		`{}`,
		`[]`,
	}
	for _, s := range keep {
		assert.True(t, isKeepableReferenceEnvelope(gjson.Parse(s)), "must keep %s", s)
	}
	for _, s := range drop {
		assert.False(t, isKeepableReferenceEnvelope(gjson.Parse(s)), "must drop %s", s)
	}
}

func TestReferenceEnvelopeFields(t *testing.T) {
	desired := []byte(`{
		"Token": {"$ref": "formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/S"},
		"Bad": {"$res": true},
		"Plain": "",
		"Extra": {"$ref": "formae://2ABcDeFgHiJkLmNoPqRsTuVwXyZ#/T"}
	}`)
	fields := referenceEnvelopeFields(desired, []string{"Token", "Bad", "Plain"})
	assert.Equal(t, map[string]bool{"Token": true}, fields,
		"only well-formed envelopes on schema fields enter the keep-set")
	assert.Empty(t, referenceEnvelopeFields(nil, []string{"Token"}))
	assert.Empty(t, referenceEnvelopeFields([]byte(`not json`), []string{"Token"}))
}
