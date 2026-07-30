// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestExtractJSONPath(t *testing.T) {
	const secret = "super-secret-token"
	doc := `{"creds":{"token":"` + secret + `","port":5432,"tls":true,"nested":{"k":"v"},"arr":[1,2],"empty":null}}`

	t.Run("string leaf", func(t *testing.T) {
		got, err := extractJSONPath(doc, "creds.token")
		if err != nil || got != secret {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("number coerces to string", func(t *testing.T) {
		got, err := extractJSONPath(doc, "creds.port")
		if err != nil || got != "5432" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("bool coerces to string", func(t *testing.T) {
		got, err := extractJSONPath(doc, "creds.tls")
		if err != nil || got != "true" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("object is an error", func(t *testing.T) {
		if _, err := extractJSONPath(doc, "creds.nested"); err == nil {
			t.Fatal("expected error for object")
		}
	})
	t.Run("array is an error", func(t *testing.T) {
		if _, err := extractJSONPath(doc, "creds.arr"); err == nil {
			t.Fatal("expected error for array")
		}
	})
	t.Run("missing key is an error distinct from null", func(t *testing.T) {
		_, err := extractJSONPath(doc, "creds.nope")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found error, got %v", err)
		}
	})
	t.Run("explicit null is an error", func(t *testing.T) {
		_, err := extractJSONPath(doc, "creds.empty")
		if err == nil || !strings.Contains(err.Error(), "null") {
			t.Fatalf("expected null error, got %v", err)
		}
	})
	t.Run("invalid json is an error", func(t *testing.T) {
		if _, err := extractJSONPath("not json", "a"); err == nil {
			t.Fatal("expected invalid-json error")
		}
	})
	t.Run("errors never contain the plaintext", func(t *testing.T) {
		for _, p := range []string{"creds.nested", "creds.arr", "creds.nope", "creds.empty"} {
			if _, err := extractJSONPath(doc, p); err != nil && strings.Contains(err.Error(), secret) {
				t.Fatalf("path %q: error leaked plaintext: %v", p, err)
			}
		}
	})
}

// firstRefURI returns the single URI stored in pr.refs; fails the test if
// the map is empty or has more than one key.
func firstRefURI(t *testing.T, pr *propertyResolver) pkgmodel.FormaeURI {
	t.Helper()
	if len(pr.refs) != 1 {
		t.Fatalf("expected exactly 1 ref URI in propertyResolver, got %d", len(pr.refs))
	}
	for uri := range pr.refs {
		return uri
	}
	t.Fatal("unreachable")
	return ""
}

// TestResolveReference_AppliesJSONPath verifies that when a $ref envelope carries
// a $json key the resolver extracts the scalar at that dotted path from the
// resolved JSON document and stores it as $value, instead of using the whole doc.
func TestResolveReference_AppliesJSONPath(t *testing.T) {
	secretRef := newTestRef("SecretString")
	props := json.RawMessage(`{"Password":{"$ref":"` + secretRef + `","$visibility":"Opaque","$json":"db.password"}}`)
	pr := newPropertyResolver(props)

	uri := firstRefURI(t, pr)
	resolved := `{"db":{"password":"super-secret"}}`
	if err := pr.setRefValue(uri, resolved); err != nil {
		t.Fatal(err)
	}
	out, err := pr.resolveReferences(props)
	if err != nil {
		t.Fatal(err)
	}
	// The consumer property must hold the extracted scalar, not the whole doc.
	got := gjson.GetBytes(out, "Password.$value").String()
	if got != "super-secret" {
		t.Fatalf("expected extracted value %q, got %q (full: %s)", "super-secret", got, out)
	}
}

// TestResolveReference_JSONPathPreservedOnStoredValue verifies that the $json key
// is preserved on the stored Value so that resolveReferences can re-apply it
// idempotently (the round-trip form still carries $json on the envelope).
func TestResolveReference_JSONPathPreservedOnStoredValue(t *testing.T) {
	secretRef := newTestRef("SecretString")
	props := json.RawMessage(`{"Password":{"$ref":"` + secretRef + `","$visibility":"Opaque","$json":"db.password"}}`)
	pr := newPropertyResolver(props)

	uri := firstRefURI(t, pr)
	if err := pr.setRefValue(uri, `{"db":{"password":"s3cr3t"}}`); err != nil {
		t.Fatal(err)
	}

	// After setRefValue the stored ref must carry JSONPath.
	for _, bucket := range pr.refs {
		for _, ref := range bucket {
			if ref.ResolvedValue.JSONPath != "db.password" {
				t.Fatalf("JSONPath not preserved on stored Value; got %q", ref.ResolvedValue.JSONPath)
			}
		}
	}
}

// TestResolveReference_NoJSONPath verifies that a plain $ref (no $json) continues
// to work exactly as before: no extraction, the resolved scalar is stored as $value.
func TestResolveReference_NoJSONPath(t *testing.T) {
	vpcRef := newTestRef("VpcId")
	props := json.RawMessage(`{"VpcId":{"$ref":"` + vpcRef + `"}}`)
	pr := newPropertyResolver(props)

	uri := firstRefURI(t, pr)
	if err := pr.setRefValue(uri, `{"VpcId":"vpc-123"}`); err != nil {
		t.Fatal(err)
	}
	out, err := pr.resolveReferences(props)
	if err != nil {
		t.Fatal(err)
	}
	got := gjson.GetBytes(out, "VpcId.$value").String()
	if got != "vpc-123" {
		t.Fatalf("expected vpc-123, got %q", got)
	}
}
