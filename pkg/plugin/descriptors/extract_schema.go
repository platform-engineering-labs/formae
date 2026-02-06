// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package descriptors

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

//go:embed *.pkl
var pklFiles embed.FS

// Dependency represents a name-value pair for PKL dependencies.
// Name is the dependency alias (e.g., "aws", "formae")
// Value is either a package URI (package://...) or a local path to PklProject
type Dependency struct {
	Name  string
	Value string
}

// ExtractSchemaFromDependencies extracts ResourceDescriptors from a list of dependencies.
// Each dependency is a Dependency struct with Name and Value (package URI or absolute path).
func ExtractSchemaFromDependencies(ctx context.Context, dependencies []Dependency) ([]plugin.ResourceTypeDescriptor, error) {
	return ExtractSchema(ctx, dependencies)
}

// ExtractSchema extracts ResourceDescriptors from PKL schema packages.
// It orchestrates the PKL evaluation pipeline:
// 1. Generates a PklProject file from the provided dependencies
// 2. Generates the imports.pkl file
// 3. Runs Extractor.pkl to extract ResourceDescriptors
func ExtractSchema(ctx context.Context, dependencies []Dependency) ([]plugin.ResourceTypeDescriptor, error) {
	if len(dependencies) == 0 {
		return nil, fmt.Errorf("at least one dependency is required")
	}

	// Create a temporary directory for generated files
	tempDir, err := os.MkdirTemp("", "formae-schema-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy embedded PKL files to temp directory
	if err := copyEmbeddedPklFiles(tempDir); err != nil {
		return nil, fmt.Errorf("failed to copy embedded PKL files: %w", err)
	}

	// Step 1: Generate PklProject
	if err := generatePklProject(ctx, tempDir, dependencies); err != nil {
		return nil, fmt.Errorf("failed to generate PklProject: %w", err)
	}

	// Step 2: Run pkl project resolve to fetch dependencies
	if err := resolvePklProject(tempDir); err != nil {
		return nil, fmt.Errorf("failed to resolve PklProject dependencies: %w", err)
	}

	// Step 3: Generate imports.pkl from PklProject dependencies
	if err := generateImports(ctx, tempDir); err != nil {
		return nil, fmt.Errorf("failed to generate imports.pkl: %w", err)
	}

	// Step 4: Run Extractor.pkl to extract ResourceDescriptors
	descriptors, err := runExtractor(ctx, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to extract resource descriptors: %w", err)
	}

	return descriptors, nil
}

// copyEmbeddedPklFiles copies the embedded PKL files to the target directory
func copyEmbeddedPklFiles(targetDir string) error {
	entries, err := pklFiles.ReadDir(".")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		content, err := pklFiles.ReadFile(entry.Name())
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

// generatePklProject generates a PklProject file from the provided dependencies
func generatePklProject(ctx context.Context, workDir string, dependencies []Dependency) error {
	// Build the dependencies string: "name1,value1,name2,value2,..."
	var parts []string
	for _, dep := range dependencies {
		parts = append(parts, dep.Name, dep.Value)
	}
	depsString := strings.Join(parts, ",")

	evaluator, err := pkl.NewEvaluator(
		ctx,
		pkl.PreconfiguredOptions,
		func(opts *pkl.EvaluatorOptions) {
			opts.Properties = map[string]string{
				"Dependencies": depsString,
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

	// Write the generated PklProject
	projectPath := filepath.Join(workDir, "PklProject")
	if err := os.WriteFile(projectPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write PklProject: %w", err)
	}

	return nil
}

// resolvePklProject runs pkl project resolve to fetch dependencies
func resolvePklProject(workDir string) error {
	// Use pkl CLI to resolve dependencies
	// This creates PklProject.deps.json with resolved versions
	cmd := newPklCommand("project", "resolve", workDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pkl project resolve failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// generateImports generates imports.pkl from PklProject dependencies using ImportsGenerator.pkl
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

	// Write the generated imports.pkl
	importsPath := filepath.Join(workDir, "imports.pkl")
	if err := os.WriteFile(importsPath, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write imports.pkl: %w", err)
	}

	return nil
}

// runExtractor runs the Extractor.pkl to extract ResourceDescriptors
func runExtractor(ctx context.Context, workDir string) ([]plugin.ResourceTypeDescriptor, error) {
	evaluator, cleanup, err := newSafeProjectEvaluator(
		ctx,
		&url.URL{Scheme: "file", Path: workDir},
		pkl.PreconfiguredOptions,
		func(opts *pkl.EvaluatorOptions) {
			opts.OutputFormat = "json"
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create project evaluator: %w", err)
	}
	defer cleanup()

	extractorPath := filepath.Join(workDir, "Extractor.pkl")

	// Get JSON output from PKL
	jsonOutput, err := evaluator.EvaluateOutputText(ctx, pkl.FileSource(extractorPath))
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate Extractor: %w", err)
	}

	// Parse JSON into Go structs
	var pklResult struct {
		Descriptors []plugin.ResourceTypeDescriptor `json:"descriptors"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &pklResult); err != nil {
		return nil, fmt.Errorf("failed to parse Extractor JSON output: %w", err)
	}

	return pklResult.Descriptors, nil
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

// newPklCommand creates a new exec.Cmd for running pkl CLI commands
func newPklCommand(args ...string) *exec.Cmd {
	return exec.Command("pkl", args...)
}
