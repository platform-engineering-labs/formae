// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package canonicalize

import (
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/model"
)

func TestCanonicalizeJSON_EqualUnderReorderWhitespaceEscape(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{"key order", `{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{"whitespace", `{"a":  1}`, `{"a":1}`},
		{"pretty vs compact", "{\n  \"a\": 1\n}", `{"a":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ca, err := Canonicalize("json", c.a)
			if err != nil {
				t.Fatalf("a: %v", err)
			}
			cb, err := Canonicalize("json", c.b)
			if err != nil {
				t.Fatalf("b: %v", err)
			}
			if ca != cb {
				t.Fatalf("expected equal canonical forms, got %q vs %q", ca, cb)
			}
		})
	}
}

func TestCanonicalizeJSON_HTMLEscapeSymmetry(t *testing.T) {
	// A value containing & ; both sides canonicalize to the same escaped form.
	a := `{"title":"Throughput & outcomes"}`
	b := "{\n  \"title\": \"Throughput & outcomes\"\n}"
	ca, _ := Canonicalize("json", a)
	cb, _ := Canonicalize("json", b)
	if ca != cb {
		t.Fatalf("expected equal, got %q vs %q", ca, cb)
	}
}

func TestCanonicalizeJSON_DistinguishesContentChange(t *testing.T) {
	ca, _ := Canonicalize("json", `{"a":1}`)
	cb, _ := Canonicalize("json", `{"a":2}`)
	if ca == cb {
		t.Fatal("expected different canonical forms for different content")
	}
}

func TestCanonicalizeJSON_ArrayOrderSignificant(t *testing.T) {
	ca, _ := Canonicalize("json", `{"p":[1,2]}`)
	cb, _ := Canonicalize("json", `{"p":[2,1]}`)
	if ca == cb {
		t.Fatal("array order must be significant")
	}
}

func TestCanonicalizeJSON_BigIntFidelity(t *testing.T) {
	// Two distinct large ints must never alias (would collapse under float64).
	ca, _ := Canonicalize("json", `{"id":9007199254740993}`)
	cb, _ := Canonicalize("json", `{"id":9007199254740992}`)
	if ca == cb {
		t.Fatal("distinct large ints must not alias")
	}
	// And the literal is preserved.
	if got, _ := Canonicalize("json", `{"id":9007199254740993}`); got != `{"id":9007199254740993}` {
		t.Fatalf("big int not preserved: %q", got)
	}
}

func TestCanonicalizeJSON_NumericFormatIsADiff(t *testing.T) {
	ca, _ := Canonicalize("json", `{"a":1}`)
	cb, _ := Canonicalize("json", `{"a":1.0}`)
	if ca == cb {
		t.Fatal("1 and 1.0 must compare as a diff under UseNumber")
	}
}

func TestCanonicalizeJSON_RejectsDuplicateKeys(t *testing.T) {
	for _, raw := range []string{
		`{"a":1,"a":2}`,           // top-level
		`{"o":{"a":1,"a":2}}`,     // nested object
		`{"arr":[{"a":1,"a":2}]}`, // object inside array
	} {
		if _, err := Canonicalize("json", raw); err == nil {
			t.Fatalf("expected duplicate-key error for %q", raw)
		}
	}
}

func TestCanonicalizeJSON_AllowsRepeatedStringValuesThatLookLikeKeys(t *testing.T) {
	// "a" appears as a value, not a duplicate key — must NOT error.
	if _, err := Canonicalize("json", `{"x":"a","y":"a"}`); err != nil {
		t.Fatalf("string values must not be mistaken for keys: %v", err)
	}
}

func TestCanonicalize_InvalidJSONErrors(t *testing.T) {
	if _, err := Canonicalize("json", `{not json`); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestCanonicalize_UnregisteredFormatErrors(t *testing.T) {
	if _, err := Canonicalize("yaml", `{}`); err == nil {
		t.Fatal("expected error for unregistered format")
	}
}

func TestIsRegistered(t *testing.T) {
	if !IsRegistered("json") {
		t.Fatal("json must be registered")
	}
	if IsRegistered("js") {
		t.Fatal("js must not be registered")
	}
}

func TestValidateSchemaFormats(t *testing.T) {
	ok := model.Schema{Hints: map[string]model.FieldHint{"configJson": {Format: "json"}}}
	if err := ValidateSchemaFormats("X", ok); err != nil {
		t.Fatalf("registered format must validate: %v", err)
	}
	bad := model.Schema{Hints: map[string]model.FieldHint{"code": {Format: "js"}}}
	if err := ValidateSchemaFormats("X", bad); err == nil {
		t.Fatal("unregistered format must fail validation")
	}
	none := model.Schema{Hints: map[string]model.FieldHint{"name": {CreateOnly: true}}}
	if err := ValidateSchemaFormats("X", none); err != nil {
		t.Fatalf("no Format hints must validate: %v", err)
	}
}
