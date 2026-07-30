// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"strings"
	"testing"
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
