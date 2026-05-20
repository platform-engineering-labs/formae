// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

func TestFindNamespace(t *testing.T) {
	// Create temp directory structure
	tempDir, err := os.MkdirTemp("", "testutil-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create plugin directory structure: plugin/schema/pkl
	schemaDir := filepath.Join(tempDir, "schema", "pkl")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("failed to create schema dir: %v", err)
	}

	// Create formae-plugin.pkl at plugin root
	manifestContent := `
name = "test-plugin"
version = "1.0.0"
namespace = "TestNS"
license = "Apache-2.0"
minFormaeVersion = "0.75.0"
`
	manifestPath := filepath.Join(tempDir, plugin.DefaultManifestPath)
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// Test findNamespace
	ns, err := findNamespace(schemaDir)
	if err != nil {
		t.Fatalf("findNamespace failed: %v", err)
	}

	// Should be lowercase
	expected := "testns"
	if ns != expected {
		t.Errorf("expected namespace %q, got %q", expected, ns)
	}
}

func TestFindNamespace_NotFound(t *testing.T) {
	// Create temp directory without manifest
	tempDir, err := os.MkdirTemp("", "testutil-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	_, err = findNamespace(tempDir)
	if err == nil {
		t.Error("expected error when manifest not found")
	}
}

func TestCopyEmbeddedFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "testutil-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if err := copyEmbeddedFiles(tempDir); err != nil {
		t.Fatalf("copyEmbeddedFiles failed: %v", err)
	}

	// Check expected files exist
	expectedFiles := []string{"PklProjectGenerator.pkl", "ImportsGenerator.pkl", "Verify.pkl"}
	for _, f := range expectedFiles {
		path := filepath.Join(tempDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s not found", f)
		}
	}
}

func TestVerifyResult_FormatReport(t *testing.T) {
	result := &VerifyResult{
		Status:             "PASSED",
		HasErrors:          false,
		TotalModules:       10,
		TotalResourceTypes: 5,
		DuplicateFileCount: 0,
		DuplicateTypeCount: 0,
		DuplicateFiles:     []DuplicateFileError{},
		DuplicateTypes:     []string{},
	}

	report := result.FormatReport("aws")

	if report == "" {
		t.Error("expected non-empty report")
	}

	// Check key elements are present
	if !strings.Contains(report, "AWS PKL Schema Verification Report") {
		t.Error("report missing title")
	}
	if !strings.Contains(report, "Status: PASSED") {
		t.Error("report missing status")
	}
	if !strings.Contains(report, "Total modules: 10") {
		t.Error("report missing module count")
	}
	if !strings.Contains(report, "VERIFICATION PASSED") {
		t.Error("report missing pass message")
	}
}

func TestVerifyResult_FormatReport_WithErrors(t *testing.T) {
	result := &VerifyResult{
		Status:             "FAILED",
		HasErrors:          true,
		TotalModules:       10,
		TotalResourceTypes: 5,
		DuplicateFileCount: 1,
		DuplicateTypeCount: 1,
		DuplicateFiles: []DuplicateFileError{
			{FileName: "Instance.pkl", Modules: []string{"@aws/ec2/Instance.pkl", "@aws/compute/Instance.pkl"}},
		},
		DuplicateTypes: []string{"Duplicate resource type 'AWS::EC2::Instance' found in: @aws/ec2/Instance.pkl, @aws/compute/Instance.pkl"},
	}

	report := result.FormatReport("aws")

	if !strings.Contains(report, "DUPLICATE FILES DETECTED") {
		t.Error("report missing duplicate files section")
	}
	if !strings.Contains(report, "DUPLICATE RESOURCE TYPES DETECTED") {
		t.Error("report missing duplicate types section")
	}
	if !strings.Contains(report, "VERIFICATION FAILED") {
		t.Error("report missing fail message")
	}
}

func TestVerifySchemaWithNamespace_FakeAWS(t *testing.T) {
	// Skip if running in CI without the full repo
	fakeAWSSchema := "../../../internal/testplugin/fakeaws/schema/pkl"
	if _, err := os.Stat(fakeAWSSchema); os.IsNotExist(err) {
		t.Skip("fake-aws schema not found, skipping integration test")
	}

	result, err := VerifySchemaWithNamespace(fakeAWSSchema, "fakeaws")
	if err != nil {
		t.Fatalf("VerifySchemaWithNamespace failed: %v", err)
	}

	// fake-aws should pass verification
	if result.HasErrors {
		t.Errorf("expected fake-aws to pass verification, got errors: %v", result)
	}

	if result.TotalModules == 0 {
		t.Error("expected at least one module")
	}

	t.Logf("Verified %d modules, %d resource types", result.TotalModules, result.TotalResourceTypes)
}

// TestGenerateImports_TopLevelFileIsIncluded verifies that ImportsGenerator.pkl matches
// PKL files placed directly under the namespace root (e.g., @ns/resource.pkl), not
// only files in subdirectories (e.g., @ns/subdir/resource.pkl).
//
// Bug (G-7): the current generator uses import*("@<ns>/**/*.pkl") which requires at
// least one intermediate directory. Top-level files are silently missed.
//
// This test will FAIL until the fix (adding import*("@<ns>/*.pkl")) is applied in
// pkg/plugin/testutil/pkl/ImportsGenerator.pkl.
func TestGenerateImports_TopLevelFileIsIncluded(t *testing.T) {
	// Set up a minimal self-contained schema with ONLY a top-level resource.pkl.
	schemaDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(schemaDir, "PklProject"), []byte(`amends "pkl:Project"

package {
  name = "minimaltest"
  baseUri = "package://test.local/minimaltest"
  version = "0.0.1"
  packageZipUrl = "https://test.local/minimaltest@0.0.1.zip"
}
`), 0644); err != nil {
		t.Fatalf("failed to write schema PklProject: %v", err)
	}
	// Only a top-level file — no subdirectory.
	if err := os.WriteFile(filepath.Join(schemaDir, "resource.pkl"), []byte("module minimaltest.resource\n"), 0644); err != nil {
		t.Fatalf("failed to write resource.pkl: %v", err)
	}

	// Prepare the generator workdir with the embedded PKL files.
	workDir := t.TempDir()
	if err := copyEmbeddedFiles(workDir); err != nil {
		t.Fatalf("copyEmbeddedFiles failed: %v", err)
	}

	// Write a PklProject that maps "minimaltest" -> local schema.
	pklProjectContent := "amends \"pkl:Project\"\n\ndependencies {\n  [\"minimaltest\"] = import(\"" + filepath.Join(schemaDir, "PklProject") + "\")\n}\n"
	if err := os.WriteFile(filepath.Join(workDir, "PklProject"), []byte(pklProjectContent), 0644); err != nil {
		t.Fatalf("failed to write PklProject: %v", err)
	}

	// Resolve the project (no network needed — local path dependency).
	if err := resolvePklProject(workDir); err != nil {
		t.Fatalf("pkl project resolve failed: %v", err)
	}

	// Generate imports.pkl using ImportsGenerator.pkl.
	ctx := context.Background()
	if err := generateImports(ctx, workDir); err != nil {
		t.Fatalf("generateImports failed: %v", err)
	}

	// Read the generated imports.pkl content.
	importsContent, err := os.ReadFile(filepath.Join(workDir, "imports.pkl"))
	if err != nil {
		t.Fatalf("failed to read generated imports.pkl: %v", err)
	}

	// The fix requires imports.pkl to include the top-level glob @ns/*.pkl.
	// Currently only @ns/**/*.pkl is present, which misses @minimaltest/resource.pkl.
	if !strings.Contains(string(importsContent), `import*("@minimaltest/*.pkl")`) {
		t.Errorf("imports.pkl is missing top-level glob:\n"+
			"  want: import*(\"@minimaltest/*.pkl\") to be present\n"+
			"  got:\n%s\n\n"+
			"Bug G-7: ImportsGenerator.pkl only emits @ns/**/*.pkl which misses top-level files.\n"+
			"Fix: add import*(\"@ns/*.pkl\") union in pkg/plugin/testutil/pkl/ImportsGenerator.pkl",
			string(importsContent))
	}
}
