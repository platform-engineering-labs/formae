// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDocsWithNamespace_FakeAWS(t *testing.T) {
	// Skip if running in CI without the full repo
	fakeAWSSchema := "../../../plugins/fake-aws/schema/pkl"
	if _, err := os.Stat(fakeAWSSchema); os.IsNotExist(err) {
		t.Skip("fake-aws schema not found, skipping integration test")
	}

	result, err := GenerateDocsWithNamespace(fakeAWSSchema, "fakeaws")
	if err != nil {
		t.Fatalf("GenerateDocsWithNamespace failed: %v", err)
	}

	if result.TotalResources == 0 {
		t.Error("expected at least one resource")
	}

	// Check that resources have expected fields
	for _, res := range result.Resources {
		if res.Type == "" {
			t.Error("resource type should not be empty")
		}
		if res.Identifier == "" {
			t.Error("resource identifier should not be empty")
		}
	}

	t.Logf("Generated docs for %d resources", result.TotalResources)
}

func TestDocsResult_FormatMarkdownTable(t *testing.T) {
	comment := "Test comment"
	result := &DocsResult{
		Resources: []ResourceDoc{
			{Type: "AWS::EC2::Instance", Discoverable: true, Extractable: true, DocComment: nil},
			{Type: "AWS::EC2::VPC", Discoverable: false, Extractable: true, DocComment: &comment},
		},
		TotalResources: 2,
	}

	table := result.FormatMarkdownTable()

	if !strings.Contains(table, "| Type | Discoverable | Extractable | Comment |") {
		t.Error("table missing header")
	}
	if !strings.Contains(table, "AWS::EC2::Instance") {
		t.Error("table missing Instance")
	}
	if !strings.Contains(table, "AWS::EC2::VPC") {
		t.Error("table missing VPC")
	}
	if !strings.Contains(table, "✅") {
		t.Error("table missing checkmark")
	}
	if !strings.Contains(table, "❌") {
		t.Error("table missing x mark")
	}
	if !strings.Contains(table, "Test comment") {
		t.Error("table missing comment")
	}
}

func TestCopyEmbeddedFiles_IncludesDocs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "testutil-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if err := copyEmbeddedFiles(tempDir); err != nil {
		t.Fatalf("copyEmbeddedFiles failed: %v", err)
	}

	// Check Docs.pkl exists
	docsPath := filepath.Join(tempDir, "Docs.pkl")
	if _, err := os.Stat(docsPath); os.IsNotExist(err) {
		t.Error("expected Docs.pkl not found")
	}
}
