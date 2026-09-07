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

func TestExtractSchema_ParentMappings_SingleParentRef(t *testing.T) {
	descriptors := extractFakeawsDescriptors(t)

	d := findDescriptor(t, descriptors, "FakeAWS::EFS::MountTarget")
	if d.Schema.Parent != "FakeAWS::EFS::FileSystem" {
		t.Errorf("expected Parent=%q, got %q", "FakeAWS::EFS::FileSystem", d.Schema.Parent)
	}
	if got, want := len(d.Schema.ParentMappings), 1; got != want {
		t.Fatalf("expected %d ParentMapping, got %d (%#v)", want, got, d.Schema.ParentMappings)
	}
	if d.Schema.ParentMappings[0].ParentProperty != "FileSystemId" {
		t.Errorf("expected ParentProperty=%q, got %q", "FileSystemId", d.Schema.ParentMappings[0].ParentProperty)
	}
	if d.Schema.ParentMappings[0].ChildProperty != "FileSystemId" {
		t.Errorf("expected ChildProperty=%q, got %q", "FileSystemId", d.Schema.ParentMappings[0].ChildProperty)
	}
}

func TestExtractSchema_ParentMappings_CompositeParentRefs(t *testing.T) {
	descriptors := extractFakeawsDescriptors(t)

	d := findDescriptor(t, descriptors, "FakeAWS::ECS::TaskSet")
	if d.Schema.Parent != "FakeAWS::ECS::Service" {
		t.Errorf("expected Parent=%q, got %q", "FakeAWS::ECS::Service", d.Schema.Parent)
	}
	if got, want := len(d.Schema.ParentMappings), 2; got != want {
		t.Fatalf("expected %d ParentMappings, got %d (%#v)", want, got, d.Schema.ParentMappings)
	}
	if d.Schema.ParentMappings[0].ParentProperty != "ServiceName" || d.Schema.ParentMappings[0].ChildProperty != "Service" {
		t.Errorf("first mapping = %+v; want {ServiceName, Service}", d.Schema.ParentMappings[0])
	}
	if d.Schema.ParentMappings[1].ParentProperty != "Cluster" || d.Schema.ParentMappings[1].ChildProperty != "Cluster" {
		t.Errorf("second mapping = %+v; want {Cluster, Cluster}", d.Schema.ParentMappings[1])
	}
}

func TestExtractSchema_ParentMappings_ListParamFallback(t *testing.T) {
	descriptors := extractFakeawsDescriptors(t)

	d := findDescriptor(t, descriptors, "FakeAWS::Legacy::LegacyChild")
	if d.Schema.Parent != "FakeAWS::Legacy::LegacyParent" {
		t.Errorf("expected Parent=%q, got %q", "FakeAWS::Legacy::LegacyParent", d.Schema.Parent)
	}
	if got, want := len(d.Schema.ParentMappings), 1; got != want {
		t.Fatalf("expected %d ParentMapping, got %d (%#v)", want, got, d.Schema.ParentMappings)
	}
	// listParam fallback: ParentProperty from listParam.parentProperty,
	// ChildProperty derived from listParam.listParameter.
	if d.Schema.ParentMappings[0].ParentProperty != "ParentId" {
		t.Errorf("expected ParentProperty=%q, got %q", "ParentId", d.Schema.ParentMappings[0].ParentProperty)
	}
	if d.Schema.ParentMappings[0].ChildProperty != "ParentRef" {
		t.Errorf("expected ChildProperty=%q (from listParameter), got %q", "ParentRef", d.Schema.ParentMappings[0].ChildProperty)
	}
}

func TestExtractSchema_ParentMappings_AbsentWhenUnset(t *testing.T) {
	descriptors := extractFakeawsDescriptors(t)

	// A resource without `parent` or `parentRefs` must emit an empty Parent
	// and nil/empty ParentMappings so the engine treats it as un-parented.
	d := findDescriptor(t, descriptors, "FakeAWS::Recursive::TreeNode")
	if d.Schema.Parent != "" {
		t.Errorf("expected empty Parent, got %q", d.Schema.Parent)
	}
	if len(d.Schema.ParentMappings) != 0 {
		t.Errorf("expected no ParentMappings, got %#v", d.Schema.ParentMappings)
	}
}

// TestExtractSchema_UnannotatedPathsSurface verifies that descriptors.ExtractSchema
// emits every reachable property path in Schema.Hints — annotated AND unannotated —
// with unannotated paths defaulting to FieldHint{CreateOnly: false}. This is the
// schema contract that lets the planner gate cascade-replace on createOnly: it
// must see every reference target's createOnly value, even when the PKL author
// did not write an explicit @FieldHint.
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
