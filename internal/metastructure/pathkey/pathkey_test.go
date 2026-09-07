// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package pathkey

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// nasties covers the gjson/sjson path grammar plus the shapes that occur in
// real resource data: Kubernetes annotation keys, numeric-looking map keys and
// keys carrying the wildcard, modifier and multipath characters.
var nasties = []struct {
	name string
	key  string
}{
	{"plain", "name"},
	{"plain with underscore and dash", "my_field-name"},
	{"dotted k8s annotation", "objectset.rio.cattle.io/applied"},
	{"dotted with slash", "app.kubernetes.io/name"},
	{"backslash", `back\slash`},
	{"wildcard star", "star*key"},
	{"wildcard question", "question?key"},
	{"hash", "hash#key"},
	{"at", "at@key"},
	{"colon", "colon:key"},
	{"leading colon", ":leading"},
	{"pipe", "pipe|key"},
	{"numeric looking", "123"},
	{"numeric dotted", "1.2.3"},
	{"bracket", "bracket[0]"},
	{"space", "with space"},
	{"unicode", "ünïcodé.kéy"},
	{"literal null text", "null"},
}

// The rule is the union of the two engines' grammars: everything gjson.Escape
// escapes, plus the colon sjson reads as its force marker.
func TestEscapeExtendsGjsonEscapeWithTheColon(t *testing.T) {
	for _, tc := range nasties {
		t.Run(tc.name, func(t *testing.T) {
			want := strings.ReplaceAll(gjson.Escape(tc.key), ":", `\:`)
			if got := Escape(tc.key); got != want {
				t.Fatalf("Escape(%q) = %q, want %q", tc.key, got, want)
			}
		})
	}
}

// A key containing no path-grammar characters must escape to itself, so that
// escaping at construction leaves every ordinary path byte-identical.
func TestEscapeIsIdentityForPlainKeys(t *testing.T) {
	plain := []string{"name", "my_field-name", "123", "with space", "ünïcodé"}
	for _, key := range plain {
		if got := Escape(key); got != key {
			t.Fatalf("Escape(%q) = %q, want identity", key, got)
		}
	}
}

// An escaped key addresses exactly the literal key it names, both for reads and
// for writes, so no dotted key is ever exploded into a nested tree.
func TestEscapedKeyAddressesTheLiteralKey(t *testing.T) {
	for _, tc := range nasties {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := sjson.Set(`{}`, Escape(tc.key), "value")
			if err != nil {
				t.Fatalf("sjson.Set: %v", err)
			}

			parsed := gjson.Parse(doc)
			if !parsed.IsObject() {
				t.Fatalf("document is not an object: %s", doc)
			}
			keys := parsed.Map()
			if len(keys) != 1 {
				t.Fatalf("document has %d keys, want 1: %s", len(keys), doc)
			}
			if _, ok := keys[tc.key]; !ok {
				t.Fatalf("literal key %q absent, document is %s", tc.key, doc)
			}

			if got := gjson.Get(doc, Escape(tc.key)); got.String() != "value" {
				t.Fatalf("read back %q, want \"value\" (document %s)", got.String(), doc)
			}
		})
	}
}

func TestJoinEscapesEverySegment(t *testing.T) {
	tests := []struct {
		name     string
		segments []string
		want     string
	}{
		{"empty", nil, ""},
		{"single plain", []string{"metadata"}, "metadata"},
		{"two plain", []string{"metadata", "name"}, "metadata.name"},
		{
			"dotted leaf",
			[]string{"metadata", "annotations", "objectset.rio.cattle.io/applied"},
			`metadata.annotations.objectset\.rio\.cattle\.io\/applied`,
		},
		{"array index segment", []string{"spec", "0", "name"}, "spec.0.name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Join(tc.segments...); got != tc.want {
				t.Fatalf("Join(%q) = %q, want %q", tc.segments, got, tc.want)
			}
		})
	}
}

// Split is the inverse of Join: it splits on unescaped dots only and returns
// the unescaped segments.
func TestSplitIsInverseOfJoin(t *testing.T) {
	segmentSets := [][]string{
		{"metadata"},
		{"metadata", "name"},
		{"metadata", "annotations", "objectset.rio.cattle.io/applied"},
		{"spec", "0", "containers"},
		{"a.b", "c.d"},
		{`back\slash`, "star*key"},
		{"hash#key", "at@key", "pipe|key"},
		{"1.2.3"},
		{":leading", "colon:key"},
	}
	for _, segments := range segmentSets {
		joined := Join(segments...)
		got := Split(joined)
		if len(got) != len(segments) {
			t.Fatalf("Split(%q) = %q (%d segments), want %q (%d)", joined, got, len(got), segments, len(segments))
		}
		for i := range segments {
			if got[i] != segments[i] {
				t.Fatalf("Split(%q)[%d] = %q, want %q", joined, i, got[i], segments[i])
			}
		}
	}
}

// Paths built without escaping still split the way strings.Split would, so
// migrating an existing consumer to Split cannot change its behavior on the
// plain-key paths it handles today.
func TestSplitOnUnescapedPathsMatchesPlainSplit(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"", []string{""}},
		{"metadata", []string{"metadata"}},
		{"metadata.name", []string{"metadata", "name"}},
		{"spec.containers.0.image", []string{"spec", "containers", "0", "image"}},
	}
	for _, tc := range tests {
		got := Split(tc.path)
		if len(got) != len(tc.want) {
			t.Fatalf("Split(%q) = %q, want %q", tc.path, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("Split(%q)[%d] = %q, want %q", tc.path, i, got[i], tc.want[i])
			}
		}
	}
}

// The colon is what gjson's own escaping grammar does not cover: sjson reads
// one at the start of any path segment as its force marker, so leaving it
// unescaped silently drops it from the key being written.
func TestUnescapedLeadingColonIsConsumedBySjson(t *testing.T) {
	underEscaped, err := sjson.Set(`{}`, "annotations."+gjson.Escape(":leading"), "value")
	if err != nil {
		t.Fatalf("sjson.Set: %v", err)
	}
	if !gjson.Get(underEscaped, "annotations.leading").Exists() {
		t.Fatalf("expected gjson's grammar alone to lose the colon, got %s", underEscaped)
	}

	escaped, err := sjson.Set(`{}`, Join("annotations", ":leading"), "value")
	if err != nil {
		t.Fatalf("sjson.Set: %v", err)
	}
	if got := gjson.Get(escaped, Join("annotations", ":leading")); got.String() != "value" {
		t.Fatalf("read back %q, want \"value\" (document %s)", got.String(), escaped)
	}
}

// A trailing backslash cannot be produced by Join, but Split must not panic or
// lose data if it meets one in a hand-written path.
func TestSplitToleratesTrailingEscape(t *testing.T) {
	got := Split(`a\`)
	if len(got) != 1 || got[0] != `a\` {
		t.Fatalf(`Split("a\\") = %q, want ["a\\"]`, got)
	}
}
