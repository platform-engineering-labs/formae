package changeset

import (
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestStripArrayIndices(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"LoadBalancers.0.TargetGroupArn", "LoadBalancers.TargetGroupArn"},
		{"Statement.0", "Statement"},
		{"a.0.b.1.c", "a.b.c"},
		{"no.indices.here", "no.indices.here"},
		{"", ""},
		{"42", "42"}, // a bare number at the root is unusual but should be preserved
		{"Tags.10.Key", "Tags.Key"},
	}
	for _, tc := range cases {
		got := stripArrayIndices(tc.in)
		if got != tc.want {
			t.Errorf("stripArrayIndices(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFieldHintForPath_MatchesAfterStripping(t *testing.T) {
	schema := pkgmodel.Schema{
		Hints: map[string]pkgmodel.FieldHint{
			"LoadBalancers.TargetGroupArn": {AttachesTo: true},
		},
	}
	got := fieldHintForPath(schema, "LoadBalancers.0.TargetGroupArn")
	if !got.AttachesTo {
		t.Fatalf("expected AttachesTo=true after index stripping, got %#v", got)
	}
}

func TestFieldHintForPath_ReturnsZeroWhenUnknown(t *testing.T) {
	schema := pkgmodel.Schema{Hints: map[string]pkgmodel.FieldHint{}}
	got := fieldHintForPath(schema, "Missing.Field")
	if got.AttachesTo || got.CreateOnly || got.Required {
		t.Fatalf("expected zero hint for unknown path, got %#v", got)
	}
}
