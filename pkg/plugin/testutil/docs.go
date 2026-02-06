// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/apple/pkl-go/pkl"
)

// DocsResult contains the documentation for a schema.
type DocsResult struct {
	Resources      []ResourceDoc `json:"resources"`
	TotalResources int           `json:"totalResources"`
}

// ResourceDoc contains documentation for a single resource type.
type ResourceDoc struct {
	Type             string  `json:"type"`
	Discoverable     bool    `json:"discoverable"`
	Extractable      bool    `json:"extractable"`
	Nonprovisionable bool    `json:"nonprovisionable"`
	Identifier       string  `json:"identifier"`
	DocComment       *string `json:"docComment"`
	ModuleName       string  `json:"moduleName"`
	ClassName        string  `json:"className"`
}

// GenerateDocs generates documentation for a plugin schema.
// schemaDir should be the path to the schema/pkl directory containing PklProject.
// It automatically discovers the namespace from formae-plugin.pkl by walking up directories.
func GenerateDocs(schemaDir string) (*DocsResult, error) {
	namespace, err := findNamespace(schemaDir)
	if err != nil {
		return nil, err
	}

	return GenerateDocsWithNamespace(schemaDir, namespace)
}

// GenerateDocsWithNamespace generates documentation with an explicit namespace.
func GenerateDocsWithNamespace(schemaDir, namespace string) (*DocsResult, error) {
	ctx := context.Background()

	// Resolve absolute path
	absSchemaDir, err := filepath.Abs(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve schema directory: %w", err)
	}

	// Check PklProject exists
	pklProjectPath := filepath.Join(absSchemaDir, "PklProject")
	if _, err := os.Stat(pklProjectPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("PklProject not found in %s", absSchemaDir)
	}

	// Create temp directory for generated files
	tempDir, err := os.MkdirTemp("", "formae-docs-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy embedded PKL files to temp directory
	if err := copyEmbeddedFiles(tempDir); err != nil {
		return nil, fmt.Errorf("failed to copy embedded PKL files: %w", err)
	}

	// Step 1: Generate PklProject
	if err := generatePklProject(ctx, tempDir, namespace, pklProjectPath); err != nil {
		return nil, fmt.Errorf("failed to generate PklProject: %w", err)
	}

	// Step 2: Run pkl project resolve
	if err := resolvePklProject(tempDir); err != nil {
		return nil, fmt.Errorf("failed to resolve PklProject: %w", err)
	}

	// Step 3: Generate imports.pkl
	if err := generateImports(ctx, tempDir); err != nil {
		return nil, fmt.Errorf("failed to generate imports.pkl: %w", err)
	}

	// Step 4: Run docs generation
	result, err := runDocs(ctx, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to generate docs: %w", err)
	}

	return result, nil
}

// runDocs runs Docs.pkl and parses the result.
func runDocs(ctx context.Context, workDir string) (*DocsResult, error) {
	evaluator, cleanup, err := newSafeProjectEvaluator(
		ctx,
		&url.URL{Scheme: "file", Path: workDir},
		pkl.PreconfiguredOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create evaluator: %w", err)
	}
	defer cleanup()

	docsPath := filepath.Join(workDir, "Docs.pkl")
	jsonOutput, err := evaluator.EvaluateOutputText(ctx, pkl.FileSource(docsPath))
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate Docs.pkl: %w", err)
	}

	var result DocsResult
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		return nil, fmt.Errorf("failed to parse docs result: %w", err)
	}

	// Sort resources by type
	sort.Slice(result.Resources, func(i, j int) bool {
		return result.Resources[i].Type < result.Resources[j].Type
	})

	return &result, nil
}

// FormatMarkdownTable returns a markdown table of resource capabilities.
func (r *DocsResult) FormatMarkdownTable() string {
	var sb strings.Builder

	sb.WriteString("| Type | Discoverable | Extractable | Comment |\n")
	sb.WriteString("|------|--------------|-------------|----------|\n")

	for _, res := range r.Resources {
		discoverable := "❌"
		if res.Discoverable {
			discoverable = "✅"
		}

		extractable := "❌"
		if res.Extractable {
			extractable = "✅"
		}

		comment := ""
		if res.DocComment != nil && *res.DocComment != "" {
			comment = *res.DocComment
		}

		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			res.Type, discoverable, extractable, comment))
	}

	return sb.String()
}

// FormatPlainTable returns a plain text table of resource capabilities.
func (r *DocsResult) FormatPlainTable() string {
	var sb strings.Builder

	sb.WriteString("Type\tDiscoverable\tExtractable\tComment\n")

	for _, res := range r.Resources {
		discoverable := "❌"
		if res.Discoverable {
			discoverable = "✅"
		}

		extractable := "❌"
		if res.Extractable {
			extractable = "✅"
		}

		comment := ""
		if res.DocComment != nil && *res.DocComment != "" {
			comment = *res.DocComment
		}

		sb.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\n",
			res.Type, discoverable, extractable, comment))
	}

	return sb.String()
}
