// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package migration

import "testing"

// The predicate looks for the shape an older build's dot-expansion left behind:
// a literal key containing dots, sitting beside a nested tree that reproduces it
// exactly. Exact reproduction is what separates the corruption from a document
// that merely happens to carry both a dotted key and a same-named object, which
// is a legitimate shape nothing should touch.

func TestHasDottedKeyCorruption(t *testing.T) {
	cases := []struct {
		name  string
		props string
		want  bool
	}{
		{
			"the reported shape: annotations",
			`{"metadata":{"annotations":{
				"objectset.rio.cattle.io/applied":"v",
				"objectset":{"rio":{"cattle":{"io/applied":"v"}}}
			}}}`,
			true,
		},
		{
			"the reported shape: annotations and labels together",
			`{"metadata":{
				"annotations":{
					"objectset.rio.cattle.io/applied":"a",
					"objectset":{"rio":{"cattle":{"io/applied":"a"}}}
				},
				"labels":{
					"objectset.rio.cattle.io/hash":"h",
					"objectset":{"rio":{"cattle":{"io/hash":"h"}}}
				}
			}}`,
			true,
		},
		{
			"at the document root",
			`{"a.b":"v","a":{"b":"v"}}`,
			true,
		},
		{
			"inside an array element",
			`{"items":[{"a.b":"v","a":{"b":"v"}}]}`,
			true,
		},
		{
			"several dotted keys sharing one head",
			`{"x.one":"1","x.two":"2","x":{"one":"1","two":"2"}}`,
			true,
		},

		// Not corruption.
		{"nested only", `{"a":{"b":"v"}}`, false},
		{"literal only", `{"a.b":"v"}`, false},
		{
			"shared first segment, different leaves",
			`{"a.b":"v","a":{"c":"w"}}`,
			false,
		},
		{
			"same leaf name, different value",
			`{"a.b":"v","a":{"b":"different"}}`,
			false,
		},
		{
			"the nested tree carries a leaf the literal side does not",
			`{"a.b":"v","a":{"b":"v","c":"extra"}}`,
			false,
		},
		{
			"the literal side carries a key the nested tree does not",
			`{"a.b":"v","a.c":"w","a":{"b":"v"}}`,
			false,
		},
		{
			"a dotted key whose head is a scalar, not a tree",
			`{"a.b":"v","a":"scalar"}`,
			false,
		},
		{"no dots anywhere", `{"spec":{"replicas":3}}`, false},
		{"empty object", `{}`, false},
		{"empty input", ``, false},
		{"not an object", `[1,2,3]`, false},

		// Values other than strings must compare too.
		{"numeric leaves", `{"a.b":3,"a":{"b":3}}`, true},
		{"numeric leaves that differ", `{"a.b":3,"a":{"b":4}}`, false},
		{"null leaves", `{"a.b":null,"a":{"b":null}}`, true},
		{"object leaves", `{"a.b":{"deep":1},"a":{"b":{"deep":1}}}`, true},
		{"array leaves", `{"a.b":[1,2],"a":{"b":[1,2]}}`, true},
		{"array leaves that differ", `{"a.b":[1,2],"a":{"b":[1,3]}}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasDottedKeyCorruption([]byte(tc.props)); got != tc.want {
				t.Errorf("HasDottedKeyCorruption(%s) = %v, want %v", tc.props, got, tc.want)
			}
		})
	}
}

// The predicate may over-match without harm — the repair tombstones the row for
// re-ingest rather than editing it — but it must never miss the reported shape
// however deeply it is buried.
func TestHasDottedKeyCorruption_FindsItAtAnyDepth(t *testing.T) {
	deep := `{"a":{"b":[{"c":{"objectset.rio.cattle.io/applied":"v",` +
		`"objectset":{"rio":{"cattle":{"io/applied":"v"}}}}}]}}`
	if !HasDottedKeyCorruption([]byte(deep)) {
		t.Errorf("corruption nested under objects and arrays must still be found")
	}
}
