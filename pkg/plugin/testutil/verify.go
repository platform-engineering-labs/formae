// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package testutil provides testing utilities for formae plugins.
package testutil

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apple/pkl-go/pkl"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

//go:embed pkl/*.pkl
var pklFiles embed.FS

// VerifyResult contains the results of schema verification.
type VerifyResult struct {
	Status             string               `json:"status"`
	HasErrors          bool                 `json:"hasErrors"`
	TotalModules       int                  `json:"totalModules"`
	TotalResourceTypes int                  `json:"totalResourceTypes"`
	DuplicateFileCount int                  `json:"duplicateFileCount"`
	DuplicateTypeCount int                  `json:"duplicateTypeCount"`
	DuplicateFiles     []DuplicateFileError `json:"duplicateFiles"`
	DuplicateTypes     []string             `json:"duplicateTypes"`
}

// DuplicateFileError represents a file that appears multiple times in the schema.
type DuplicateFileError struct {
	FileName string   `json:"fileName"`
	Modules  []string `json:"modules"`
}

// VerifySchema runs PKL schema verification for a plugin.
// schemaDir should be the path to the schema/pkl directory containing PklProject.
// It automatically discovers the namespace from formae-plugin.pkl by walking up directories.
func VerifySchema(schemaDir string) (*VerifyResult, error) {
	// Find namespace by walking up to find formae-plugin.pkl
	namespace, err := findNamespace(schemaDir)
	if err != nil {
		return nil, err
	}

	return VerifySchemaWithNamespace(schemaDir, namespace)
}

// VerifySchemaWithNamespace runs verification with an explicit namespace.
func VerifySchemaWithNamespace(schemaDir, namespace string) (*VerifyResult, error) {
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
	tempDir, err := os.MkdirTemp("", "formae-verify-*")
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

	// Step 4: Run verification
	result, err := runVerification(ctx, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to run verification: %w", err)
	}

	return result, nil
}

// findNamespace walks up from schemaDir to find formae-plugin.pkl and extracts namespace.
func findNamespace(schemaDir string) (string, error) {
	absPath, err := filepath.Abs(schemaDir)
	if err != nil {
		return "", err
	}

	// Walk up directory tree looking for formae-plugin.pkl
	current := absPath
	for {
		manifestPath := filepath.Join(current, plugin.DefaultManifestPath)
		if _, err := os.Stat(manifestPath); err == nil {
			manifest, err := plugin.ReadManifest(manifestPath)
			if err != nil {
				return "", fmt.Errorf("failed to read manifest: %w", err)
			}
			return strings.ToLower(manifest.Namespace), nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached root
			return "", fmt.Errorf("formae-plugin.pkl not found (searched up from %s)", absPath)
		}
		current = parent
	}
}

// copyEmbeddedFiles copies the embedded PKL files to the target directory.
func copyEmbeddedFiles(targetDir string) error {
	entries, err := pklFiles.ReadDir("pkl")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		content, err := pklFiles.ReadFile("pkl/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", entry.Name(), err)
		}

		targetPath := filepath.Join(targetDir, entry.Name())
		if err := os.WriteFile(targetPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", targetPath, err)
		}
	}

	return nil
}

// generatePklProject generates a PklProject file for verification.
func generatePklProject(ctx context.Context, workDir, namespace, schemaPath string) error {
	evaluator, err := pkl.NewEvaluator(
		ctx,
		pkl.PreconfiguredOptions,
		func(opts *pkl.EvaluatorOptions) {
			opts.Properties = map[string]string{
				"Namespace":  namespace,
				"SchemaPath": schemaPath,
			}
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create evaluator: %w", err)
	}
	defer evaluator.Close()

	generatorPath := filepath.Join(workDir, "PklProjectGenerator.pkl")
	result, err := evaluator.EvaluateOutputText(ctx, pkl.FileSource(generatorPath))
	if err != nil {
		return fmt.Errorf("failed to evaluate PklProjectGenerator: %w", err)
	}

	projectPath := filepath.Join(workDir, "PklProject")
	if err := os.WriteFile(projectPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write PklProject: %w", err)
	}

	return nil
}

// resolvePklProject runs pkl project resolve to fetch dependencies.
func resolvePklProject(workDir string) error {
	cmd := exec.Command("pkl", "project", "resolve", workDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pkl project resolve failed: %s", string(output))
	}
	return nil
}

// generateImports generates imports.pkl from PklProject dependencies.
func generateImports(ctx context.Context, workDir string) error {
	evaluator, cleanup, err := newSafeProjectEvaluator(
		ctx,
		&url.URL{Scheme: "file", Path: workDir},
		pkl.PreconfiguredOptions,
	)
	if err != nil {
		return fmt.Errorf("failed to create evaluator: %w", err)
	}
	defer cleanup()

	generatorPath := filepath.Join(workDir, "ImportsGenerator.pkl")
	result, err := evaluator.EvaluateOutputText(ctx, pkl.FileSource(generatorPath))
	if err != nil {
		return fmt.Errorf("failed to evaluate ImportsGenerator: %w", err)
	}

	importsPath := filepath.Join(workDir, "imports.pkl")
	if err := os.WriteFile(importsPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write imports.pkl: %w", err)
	}

	return nil
}

// runVerification runs Verify.pkl and parses the result.
func runVerification(ctx context.Context, workDir string) (*VerifyResult, error) {
	evaluator, cleanup, err := newSafeProjectEvaluator(
		ctx,
		&url.URL{Scheme: "file", Path: workDir},
		pkl.PreconfiguredOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create evaluator: %w", err)
	}
	defer cleanup()

	verifyPath := filepath.Join(workDir, "Verify.pkl")
	jsonOutput, err := evaluator.EvaluateOutputText(ctx, pkl.FileSource(verifyPath))
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate Verify.pkl: %w", err)
	}

	var result VerifyResult
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		return nil, fmt.Errorf("failed to parse verification result: %w", err)
	}

	return &result, nil
}

// FormatReport returns a human-readable verification report.
func (r *VerifyResult) FormatReport(namespace string) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "%s PKL Schema Verification Report\n", strings.ToUpper(namespace))
	sb.WriteString(strings.Repeat("=", 40) + "\n\n")
	fmt.Fprintf(&sb, "Status: %s\n\n", r.Status)
	sb.WriteString("Summary:\n")
	fmt.Fprintf(&sb, "- Total modules: %d\n", r.TotalModules)
	fmt.Fprintf(&sb, "- Total resource types: %d\n", r.TotalResourceTypes)
	fmt.Fprintf(&sb, "- Duplicate files found: %d\n", r.DuplicateFileCount)
	fmt.Fprintf(&sb, "- Duplicate types found: %d\n\n", r.DuplicateTypeCount)

	if len(r.DuplicateFiles) > 0 {
		sb.WriteString("DUPLICATE FILES DETECTED:\n")
		for _, dup := range r.DuplicateFiles {
			fmt.Fprintf(&sb, "  File '%s' appears %d times:\n", dup.FileName, len(dup.Modules))
			for _, mod := range dup.Modules {
				fmt.Fprintf(&sb, "    - %s\n", mod)
			}
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("No duplicate files found\n\n")
	}

	if len(r.DuplicateTypes) > 0 {
		sb.WriteString("DUPLICATE RESOURCE TYPES DETECTED:\n")
		for _, dup := range r.DuplicateTypes {
			fmt.Fprintf(&sb, "  %s\n", dup)
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("No duplicate resource types found\n\n")
	}

	if r.HasErrors {
		sb.WriteString("VERIFICATION FAILED - Please resolve duplicate files/types before proceeding\n")
	} else {
		sb.WriteString("VERIFICATION PASSED - Schema is valid\n")
	}

	return sb.String()
}

// newSafeProjectEvaluator creates a project-aware PKL evaluator without the race
// condition in pkl-go's NewProjectEvaluator. That function internally creates two
// evaluators on the same manager and defer-closes the first one. If the pkl subprocess
// sends a late message for the closed evaluator, the manager's listen loop exits
// entirely (calls return instead of continue), killing all message processing.
// See: https://github.com/apple/pkl-go/blob/v0.12.0/pkl/evaluator_exec.go#L57-L84
//
// This function keeps both evaluators alive until the returned cleanup function is
// called, which closes the entire manager.
func newSafeProjectEvaluator(ctx context.Context, projectBaseURL *url.URL, opts ...func(*pkl.EvaluatorOptions)) (pkl.Evaluator, func(), error) {
	manager := pkl.NewEvaluatorManager()

	projectEvaluator, err := manager.NewEvaluator(ctx, opts...)
	if err != nil {
		manager.Close()
		return nil, nil, fmt.Errorf("failed to create project evaluator: %w", err)
	}

	projectPath := projectBaseURL.JoinPath("PklProject")
	project, err := pkl.LoadProjectFromEvaluator(ctx, projectEvaluator, &pkl.ModuleSource{Uri: projectPath})
	if err != nil {
		manager.Close()
		return nil, nil, fmt.Errorf("failed to load project: %w", err)
	}

	newOpts := []func(*pkl.EvaluatorOptions){pkl.WithProject(project)}
	newOpts = append(newOpts, opts...)
	evaluator, err := manager.NewEvaluator(ctx, newOpts...)
	if err != nil {
		manager.Close()
		return nil, nil, fmt.Errorf("failed to create evaluator: %w", err)
	}

	return evaluator, func() { manager.Close() }, nil
}
