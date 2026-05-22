// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package descriptors

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "..")
}

func extractFakeawsDescriptors(t *testing.T) []plugin.ResourceTypeDescriptor {
	t.Helper()
	ctx := context.Background()

	formaeSchemaProject := filepath.Join(projectRoot(), "internal", "schema", "pkl", "schema", "PklProject")
	fakeawsProject := filepath.Join(projectRoot(), "internal", "testplugin", "fakeaws", "schema", "pkl", "PklProject")

	descriptors, err := ExtractSchema(ctx, []Dependency{
		{Name: "formae", Value: formaeSchemaProject},
		{Name: "fakeaws", Value: fakeawsProject},
	})
	if err != nil {
		t.Fatalf("ExtractSchema failed: %v", err)
	}
	return descriptors
}

func findDescriptor(t *testing.T, ds []plugin.ResourceTypeDescriptor, typeName string) plugin.ResourceTypeDescriptor {
	t.Helper()
	for _, d := range ds {
		if d.Type == typeName {
			return d
		}
	}
	t.Fatalf("descriptor %q not found in extracted descriptors", typeName)
	return plugin.ResourceTypeDescriptor{}
}

func TestExtractSchema_RecursiveSubResource(t *testing.T) {
	descriptors := extractFakeawsDescriptors(t)

	d := findDescriptor(t, descriptors, "FakeAWS::Recursive::TreeNode")
	if d.Schema.Hints == nil {
		t.Fatal("TreeNode schema hints should not be nil")
	}
	if _, ok := d.Schema.Hints["Root.Name"]; !ok {
		t.Error("expected hint for Root.Name (nested sub-resource field)")
	}
}

// TestExtractSchema_UnannotatedPathsSurface verifies that descriptors.ExtractSchema
// emits every reachable property path in Schema.Hints — annotated AND unannotated —
// with unannotated paths defaulting to FieldHint{CreateOnly: false}. This is the
// schema contract underpinning RFC-0042 (cascade-replace gating on createOnly):
// the planner must see every reference target's createOnly value, even when the
// PKL author did not write an explicit @FieldHint.
//
// Annotated values (createOnly=true on IAM::Role.RoleName-style fields) must be
// preserved exactly; unannotated paths must show up with createOnly=false at
// every nesting depth (top-level, SubResource leaf, deeply nested SubResource).
func TestExtractSchema_UnannotatedPathsSurface(t *testing.T) {
	descriptors := extractFakeawsDescriptors(t)

	d := findDescriptor(t, descriptors, "FakeAWS::Mixed::Versioned")
	if d.Schema.Hints == nil {
		t.Fatal("Versioned schema hints should not be nil")
	}

	cases := []struct {
		path       string
		wantExists bool
		wantCO     bool
		note       string
	}{
		{path: "VersionedId", wantExists: true, wantCO: false, note: "annotated leaf, empty body"},
		{path: "MutableTag", wantExists: true, wantCO: false, note: "UNANNOTATED top-level scalar"},
		{path: "ImmutableId", wantExists: true, wantCO: true, note: "annotated createOnly=true"},
		{path: "Block.Description", wantExists: true, wantCO: false, note: "UNANNOTATED SubResource leaf"},
		{path: "Block.KnownLeaf", wantExists: true, wantCO: false, note: "annotated SubResource leaf"},
		{path: "Block.Inner.Label", wantExists: true, wantCO: false, note: "UNANNOTATED deeply nested leaf"},
		{path: "Block.Inner.Tag", wantExists: true, wantCO: true, note: "annotated deeply nested createOnly=true"},
	}

	for _, tc := range cases {
		h, ok := d.Schema.Hints[tc.path]
		if !ok {
			if tc.wantExists {
				t.Errorf("path %q missing from Hints (%s); have keys: %v", tc.path, tc.note, hintKeys(d.Schema.Hints))
			}
			continue
		}
		if !tc.wantExists {
			t.Errorf("path %q unexpectedly present (%s): %+v", tc.path, tc.note, h)
			continue
		}
		if h.CreateOnly != tc.wantCO {
			t.Errorf("path %q (%s): got CreateOnly=%v, want %v", tc.path, tc.note, h.CreateOnly, tc.wantCO)
		}
	}
}

func hintKeys(m map[string]model.FieldHint) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
