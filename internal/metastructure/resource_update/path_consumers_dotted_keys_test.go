// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"testing"
)

// TargetPath escapes each literal map key as it is built, so the consumers that
// read a path back as a list of segments have to split on unescaped dots only.
// Reading an escaped path as a plain dot-separated list splits one key into
// several.

func TestStripArrayIndicesForHintLookup_EscapedSegments(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"escaped dots are one segment",
			`metadata.annotations.objectset\.rio\.cattle\.io\/applied`,
			"metadata.annotations.objectset.rio.cattle.io/applied",
		},
		{
			"indices strip around an escaped segment",
			`items.0.app\.kubernetes\.io\/name`,
			"items.app.kubernetes.io/name",
		},
		{
			"a numeric-looking literal key is not an index",
			`data.1\.2\.3`,
			"data.1.2.3",
		},
		{
			"plain paths are unchanged",
			"LoadBalancers.0.TargetGroupArn",
			"LoadBalancers.TargetGroupArn",
		},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripArrayIndicesForHintLookup(tc.in); got != tc.want {
				t.Errorf("stripArrayIndicesForHintLookup(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A JSON Pointer segment has its own escaping, which the dot-path conversion has
// to apply: "~" becomes "~0" and "/" becomes "~1" (RFC 6901), in that order.
func TestJsonPointerFromDotPath_EscapedSegments(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "Refs.0.Target", "/Refs/0/Target"},
		{"escaped dot is one pointer segment", `a\.b`, "/a.b"},
		{"tilde is escaped as ~0", `a\~b`, "/a~0b"},
		{"slash is escaped as ~1", `a\/b`, "/a~1b"},
		{
			"a k8s annotation is one segment",
			`metadata.annotations.app\.kubernetes\.io\/name`,
			"/metadata/annotations/app.kubernetes.io~1name",
		},
		{"tilde before slash", `a\~\/b`, "/a~0~1b"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonPointerFromDotPath(tc.in); got != tc.want {
				t.Errorf("jsonPointerFromDotPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
