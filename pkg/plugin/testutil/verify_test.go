// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testutil

import (
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
	fakeAWSSchema := "../../../plugins/fake-aws/schema/pkl"
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
