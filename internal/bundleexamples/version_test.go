// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bundleexamples

import "testing"

func TestLatestStable(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"ignores dev and v-prefix", []string{"0.1.0", "0.2.0", "0.2.0-dev.1", "0.10.0", "v0.3.0"}, "0.10.0"},
		{"numeric not lexical", []string{"0.1.9", "0.1.10", "0.1.2"}, "0.1.10"},
		{"dup annotated tags ok", []string{"0.1.4", "0.1.4", "0.1.3"}, "0.1.4"},
		{"none stable", []string{"0.1.0-dev.1", "v0.2.0", "garbage"}, ""},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LatestStable(c.in); got != c.want {
				t.Fatalf("LatestStable(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
