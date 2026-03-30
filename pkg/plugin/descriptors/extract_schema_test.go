// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package descriptors

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "..")
}

func TestExtractSchema_RecursiveSubResource(t *testing.T) {
	ctx := context.Background()

	formaeSchemaProject := filepath.Join(projectRoot(), "internal", "schema", "pkl", "schema", "PklProject")
	fakeawsProject := filepath.Join(projectRoot(), "internal", "testplugin", "fakeaws", "schema", "pkl", "PklProject")

	descriptors, err := ExtractSchema(ctx, []Dependency{
		{Name: "formae", Value: formaeSchemaProject},
		{Name: "fakeaws", Value: fakeawsProject},
	})
	if err != nil {
		t.Fatalf("ExtractSchema failed (possible stack overflow on recursive sub-resource types): %v", err)
	}

	// Verify the recursive TreeNode resource was extracted
	var found bool
	for _, d := range descriptors {
		if d.Type == "FakeAWS::Recursive::TreeNode" {
			found = true
			// Verify hints were extracted for the recursive sub-resource fields
			if d.Schema.Hints == nil {
				t.Fatal("TreeNode schema hints should not be nil")
			}
			if _, ok := d.Schema.Hints["Root.Name"]; !ok {
				t.Error("expected hint for Root.Name (nested sub-resource field)")
			}
			break
		}
	}
	if !found {
		t.Fatal("FakeAWS::Recursive::TreeNode not found in extracted descriptors")
	}
}
