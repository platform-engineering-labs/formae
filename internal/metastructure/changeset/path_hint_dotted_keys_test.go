// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package changeset

import (
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// TargetPath escapes each literal map key as it is built, so a path may carry
// escaped dots. Reading it as a plain dot-separated list splits one key into
// several: array indices are then stripped from the wrong places and the hint
// key that comes out names a field nobody declared.

func TestStripArrayIndices_EscapedSegments(t *testing.T) {
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
			if got := stripArrayIndices(tc.in); got != tc.want {
				t.Errorf("stripArrayIndices(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A hint declared on a dotted field name must be found from the escaped path
// that names it, and prefix-colliding field names must stay distinct.
func TestFieldHintForPath_EscapedSegments(t *testing.T) {
	schema := pkgmodel.Schema{
		Hints: map[string]pkgmodel.FieldHint{
			"metadata.annotations.app.kubernetes.io/name": {WriteOnly: true},
			"Spec":          {Opaque: true},
			"Specification": {CreateOnly: true},
			"items.tls.crt": {Opaque: true},
		},
	}

	if got := fieldHintForPath(schema, `metadata.annotations.app\.kubernetes\.io\/name`); !got.WriteOnly {
		t.Errorf("a hint on a dotted field name must be found from its escaped path, got %+v", got)
	}
	if got := fieldHintForPath(schema, `items.0.tls\.crt`); !got.Opaque {
		t.Errorf("indices must strip around an escaped segment, got %+v", got)
	}
	if got := fieldHintForPath(schema, "Spec"); !got.Opaque || got.CreateOnly {
		t.Errorf("Spec must not pick up Specification's hint, got %+v", got)
	}
	if got := fieldHintForPath(schema, "Specification"); !got.CreateOnly || got.Opaque {
		t.Errorf("Specification must not pick up Spec's hint, got %+v", got)
	}
}
