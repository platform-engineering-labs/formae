// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package aurora

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
)

func TestRefsToSQL(t *testing.T) {
	t.Run("non-empty param produces string_to_array expression", func(t *testing.T) {
		got := refsToSQL(":refs")
		// Must contain the CASE … WHEN … = '' … string_to_array form.
		want := `CASE WHEN :refs = '' THEN '{}'::text[] ELSE string_to_array(:refs, ',') END`
		if got != want {
			t.Errorf("refsToSQL(\":refs\") = %q, want %q", got, want)
		}
	})

	t.Run("different param name is substituted correctly", func(t *testing.T) {
		got := refsToSQL(":frontier")
		want := `CASE WHEN :frontier = '' THEN '{}'::text[] ELSE string_to_array(:frontier, ',') END`
		if got != want {
			t.Errorf("refsToSQL(\":frontier\") = %q, want %q", got, want)
		}
	})
}

func TestGetStringArrayField(t *testing.T) {
	t.Run("ArrayValueMemberStringValues returns the slice", func(t *testing.T) {
		field := &types.FieldMemberArrayValue{
			Value: &types.ArrayValueMemberStringValues{
				Value: []string{"ksuid1", "ksuid2"},
			},
		}
		got, err := getStringArrayField(field)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0] != "ksuid1" || got[1] != "ksuid2" {
			t.Errorf("got %v, want [ksuid1 ksuid2]", got)
		}
	})

	t.Run("empty ArrayValueMemberStringValues returns empty slice", func(t *testing.T) {
		field := &types.FieldMemberArrayValue{
			Value: &types.ArrayValueMemberStringValues{
				Value: []string{},
			},
		}
		got, err := getStringArrayField(field)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty slice", got)
		}
	})

	t.Run("FieldMemberIsNull returns nil without error", func(t *testing.T) {
		field := &types.FieldMemberIsNull{Value: true}
		got, err := getStringArrayField(field)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("unexpected field type returns error", func(t *testing.T) {
		field := &types.FieldMemberStringValue{Value: "not-an-array"}
		_, err := getStringArrayField(field)
		if err == nil {
			t.Error("expected error for unexpected field type, got nil")
		}
	})

	t.Run("unexpected ArrayValue member type returns error", func(t *testing.T) {
		field := &types.FieldMemberArrayValue{
			Value: &types.ArrayValueMemberLongValues{
				Value: []int64{1, 2},
			},
		}
		_, err := getStringArrayField(field)
		if err == nil {
			t.Error("expected error for unexpected array member type, got nil")
		}
	})
}
