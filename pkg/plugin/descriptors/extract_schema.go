// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package descriptors

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// 1. Stages each plugin dependency into a temp dir with its formae dep
//    rewritten to the agent-supplied formae URL/path. Without this step
//    PKL would resolve `@formae` references inside plugin source files
//    via the plugin's own PklProject pin, leading to two formae packages
//    in scope (the agent's and the plugin's) and type-identity mismatches
//    on every annotation. Schema additions like AttachesTo would only
//    work after a coordinated cross-repo bump of every plugin's pin.
// 2. Generates a wrapper PklProject importing the staged plugin projects.
// 3. Generates the imports.pkl file.
// 4. Runs Extractor.pkl to extract ResourceDescriptors.
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

	// Stage plugin dependencies with rewritten formae deps so that
	// `@formae` references inside plugin sources resolve to the agent's
	// formae rather than the plugin's pinned one. Returns a possibly-
	// updated `dependencies` slice whose plugin entries point at the
	// staged copies; the formae entry is left as-is.
	stagedDeps, err := stagePluginDependencies(tempDir, dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to stage plugin dependencies: %w", err)
	}

	// Step 1: Generate PklProject
	if err := generatePklProject(ctx, tempDir, stagedDeps); err != nil {
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

// formaeDepPattern matches the `["formae"] { ... }` or `["formae"] = import(...)`
// entry in a plugin's PklProject file. Used by stagePluginDependencies to
// substitute the plugin's pinned formae with the agent-supplied one.
//
// The character classes inside the alternation (`[^}]*`, `[^)]*`) are
// permissive about whitespace and newlines, which is fine for our purpose:
// the entry is bounded by either `}` or `)` and PklProject syntax doesn't
// allow either inside string literals without escaping.
var formaeDepPattern = regexp.MustCompile(`\["formae"\]\s*(?:\{[^}]*\}|=\s*import\([^)]*\))`)

// stagePluginDependencies copies each plugin dependency's schema directory
// into tempDir and rewrites the staged copy's PklProject formae dep to point
// at the agent-supplied formae value. Returns the updated dependency list
// with plugin entries re-pointed at the staged copies; the formae entry is
// returned untouched so callers see a consistent value.
//
// The formae dep is identified by Name == "formae". Plugin deps are
// identified by their Value being a local filesystem path to a PklProject
// file (a non-package URI). Remote-only plugin deps (uris) are rare in
// practice but if encountered are passed through unchanged.
func stagePluginDependencies(tempDir string, deps []Dependency) ([]Dependency, error) {
	var formaeValue string
	for _, d := range deps {
		if d.Name == "formae" {
			formaeValue = d.Value
			break
		}
	}
	if formaeValue == "" {
		// No formae dep — nothing to override against. Pass through.
		return deps, nil
	}

	out := make([]Dependency, 0, len(deps))
	for _, d := range deps {
		if d.Name == "formae" || strings.HasPrefix(d.Value, "package://") {
			out = append(out, d)
			continue
		}

		// Plugin dep with a local PklProject path. Stage the dep's source
		// directory into tempDir/<depName>/ and rewrite the staged
		// PklProject's formae entry.
		srcDir := filepath.Dir(d.Value)
		stagedDir := filepath.Join(tempDir, d.Name)
		if err := copyTree(srcDir, stagedDir); err != nil {
			return nil, fmt.Errorf("failed to stage %q from %q: %w", d.Name, srcDir, err)
		}

		stagedPkl := filepath.Join(stagedDir, "PklProject")
		if err := rewriteFormaeDepInFile(stagedPkl, formaeValue); err != nil {
			return nil, fmt.Errorf("failed to rewrite formae dep in %q: %w", stagedPkl, err)
		}

		out = append(out, Dependency{Name: d.Name, Value: stagedPkl})
	}
	return out, nil
}

// rewriteFormaeDepInFile reads a PklProject file, replaces its formae dep
// entry with one pointing at agentFormaeValue, and writes it back.
//
// agentFormaeValue may be either a `package://...` URI or a local
// filesystem path to another PklProject. The replacement entry shape
// matches accordingly.
func rewriteFormaeDepInFile(path, agentFormaeValue string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var newEntry string
	if strings.HasPrefix(agentFormaeValue, "package://") {
		newEntry = fmt.Sprintf(`["formae"] {
    uri = "%s"
  }`, agentFormaeValue)
	} else {
		newEntry = fmt.Sprintf(`["formae"] = import("%s")`, agentFormaeValue)
	}

	rewritten := formaeDepPattern.ReplaceAllString(string(content), newEntry)
	if rewritten == string(content) {
		// No formae entry found — leave the file alone. Plugins without a
		// formae dep are unusual but valid (e.g., a self-contained schema
		// not extending formae types).
		return nil
	}
	return os.WriteFile(path, []byte(rewritten), 0644)
}

// copyTree recursively copies srcDir into dstDir. Used to stage plugin
// schema directories into the temp workdir so PKL finds the rewritten
// PklProject when evaluating plugin source files.
func copyTree(srcDir, dstDir string) error {
	return filepath.Walk(srcDir, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, srcPath)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}
		return copyFile(srcPath, dstPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
